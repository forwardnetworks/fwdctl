package skills_test

import (
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

// A fake Forward with snapshot s1, a failing check 9, a passing check 8, an org ServiceNow integration and incidents linked per check.
type incFake struct {
	mu       sync.Mutex
	links    []map[string]any // checkId, number, state
	actions  []string         // the action query of each POST to a check
	closed   []string
	noSN     bool
	dropLink bool // Forward answers a link but the list never shows it
	incState string
}

func (f *incFake) routes() map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		snapsPath: ready1(),
		"GET " + checksPath: func(r *http.Request, _ []byte) (int, any) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if r.URL.Query().Get("view") == "serviceNowIncidents" {
				if len(r.URL.Query()["type"]) == 0 {
					return 400, map[string]any{"message": "type required"}
				}
				return 200, f.links
			}
			return 200, []any{}
		},
		"POST " + checksPath: func(r *http.Request, _ []byte) (int, any) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.actions = append(f.actions, r.URL.Query().Get("action"))
			return 204, nil
		},
		"GET " + checksPath + "/9": func(r *http.Request, _ []byte) (int, any) {
			if r.URL.Query().Get("view") == "serviceNowIncidentUpdateHistory" {
				return 200, []any{map[string]any{"snapshotCreationTime": 1767225600000, "totalFailures": 3}}
			}
			return 200, map[string]any{"id": "9", "name": "no telnet", "status": "FAIL", "definition": map[string]any{"checkType": "Predefined"}}
		},
		"GET " + checksPath + "/404": fwdtest.Const(404, map[string]any{"message": "no such check"}),
		"GET " + checksPath + "/8":   fwdtest.Const(200, map[string]any{"id": "8", "name": "fine", "status": "PASS", "definition": map[string]any{"checkType": "Predefined"}}),
		"POST " + checksPath + "/9": func(r *http.Request, b []byte) (int, any) {
			f.mu.Lock()
			defer f.mu.Unlock()
			a := r.URL.Query().Get("action")
			f.actions = append(f.actions, a+" "+string(b))
			switch a {
			case "addServiceNowIncident":
				l := map[string]any{"checkId": "9", "number": "INC0000010", "state": "New", "stateLastUpdated": 1767225600000}
				f.links = append(f.links, l)
				return 200, l
			case "linkServiceNowIncident":
				l := map[string]any{"checkId": "9", "number": r.URL.Query().Get("incident"), "state": "New"}
				if !f.dropLink {
					f.links = append(f.links, l)
				}
				return 200, l
			case "unlinkServiceNowIncident":
				n := r.URL.Query().Get("incident")
				kept := f.links[:0]
				for _, l := range f.links {
					if l["number"] != n {
						kept = append(kept, l)
					}
				}
				f.links = kept
				return 204, nil
			}
			return 400, map[string]any{"message": "unknown action"}
		},
		"GET /api/integrations/servicenow": func(*http.Request, []byte) (int, any) {
			if f.noSN {
				return 404, map[string]any{"message": "not found"}
			}
			return 200, map[string]any{"instanceUrl": "https://example.invalid", "enabled": true}
		},
		"GET /api/integrations/servicenow/incidents/INC0000001": func(*http.Request, []byte) (int, any) {
			f.mu.Lock()
			defer f.mu.Unlock()
			return 200, map[string]any{"number": "INC0000001", "sysId": "i1", "summary": "telnet on", "impact": "2", "urgency": "3", "state": firstOr(f.incState, "New"), "url": "https://example.invalid/inc/i1"}
		},
		"POST /api/integrations/servicenow/incidents/INC0000001": func(r *http.Request, _ []byte) (int, any) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.closed = append(f.closed, r.URL.Query().Get("action"))
			f.incState = "Closed"
			return 204, nil
		},
	}
}

func firstOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func TestComplianceViewIncidentsListsLinksAndReadsOneChecksFromServiceNow(t *testing.T) {
	f := &incFake{links: []map[string]any{{"checkId": "9", "number": "INC0000001", "state": "New"}, {"checkId": "7", "number": "INC0000002", "state": "Closed"}}}
	r, _ := mustRun(t, "check-network-compliance", f.routes(), `{"network_id":"n1","view":"incidents"}`)
	if b := jsonOf(r); r.Status != result.OK || !strings.Contains(r.Finding, "2 ServiceNow incident(s)") || !strings.Contains(r.Finding, "1 not resolved") || !strings.Contains(b, "INC0000002") {
		t.Fatalf("list: %s %s %s", r.Status, r.Finding, b)
	}
	r, _ = mustRun(t, "check-network-compliance", f.routes(), `{"network_id":"n1","view":"incidents","check_id":"9"}`)
	b := jsonOf(r)
	for _, want := range []string{"https://example.invalid/inc/i1", `"total_failures":3`, `"urgency":"3"`} {
		if !strings.Contains(b, want) {
			t.Errorf("one check's incidents missing %q: %s", want, b)
		}
	}
	if strings.Contains(b, "INC0000002") {
		t.Errorf("check_id must filter to that check: %s", b)
	}
}

func TestComplianceViewIncidentsWithoutServiceNowIsUnknownAndEmptySaysItsScope(t *testing.T) {
	r, _ := mustRun(t, "check-network-compliance", (&incFake{noSN: true}).routes(), `{"network_id":"n1","view":"incidents"}`)
	if r.Status != result.Unknown {
		t.Fatalf("no integration must be unknown: %s %s", r.Status, r.Finding)
	}
	r, _ = mustRun(t, "check-network-compliance", (&incFake{}).routes(), `{"network_id":"n1","view":"incidents"}`)
	if !strings.Contains(r.Finding, "No ServiceNow incident") || !strings.Contains(strings.Join(r.Limits, " "), "raised in ServiceNow by hand") {
		t.Fatalf("empty: %s %v", r.Finding, r.Limits)
	}
	if _, _, err := runSkill(t, "check-network-compliance", (&incFake{}).routes(), `{"network_id":"n1","view":"incidents","catalogue":true}`); err == nil {
		t.Fatalf("catalogue is not an input of view incidents")
	}
}

const raiseInc = `{"network_id":"n1","action":"raise_incident","check_id":"9","incident":{"summary":"telnet on","impact":"high","urgency":"low"}`

func TestRaiseIncidentDryRunConfirmAndOnlyForAFailingCheck(t *testing.T) {
	f := &incFake{}
	r, srv := mustRun(t, "edit-checks", f.routes(), raiseInc+`}`)
	if writes(srv) != 0 || r.Changes[0].Applied || r.Changes[0].Reversible {
		t.Fatalf("dry run: writes=%d %+v", writes(srv), r.Changes)
	}
	if r, _ = mustRun(t, "edit-checks", f.routes(), raiseInc+`,"apply":true}`); r.Status == result.OK || len(f.actions) != 0 {
		t.Fatalf("raise without confirm must be refused: %s %v", r.Finding, f.actions)
	}
	r, _ = mustRun(t, "edit-checks", f.routes(), raiseInc+`,"apply":true,"confirm":"9"}`)
	if r.Status != result.OK || len(f.actions) != 1 || !strings.Contains(f.actions[0], `"impact":"1"`) || !strings.Contains(f.actions[0], `"urgency":"3"`) || !strings.Contains(r.Finding, "INC0000010") {
		t.Fatalf("raise: %s %s %v", r.Status, r.Finding, f.actions)
	}
	passing := strings.Replace(raiseInc, `"check_id":"9"`, `"check_id":"8"`, 1) + `,"apply":true,"confirm":"8"}`
	if r, _ = mustRun(t, "edit-checks", f.routes(), passing); r.Status != result.Failed || len(f.actions) != 1 {
		t.Fatalf("a passing check must be refused before anything is sent: %s %s", r.Status, r.Finding)
	}
	bad := strings.Replace(raiseInc, `"high"`, `"4"`, 1) + `}`
	if _, _, err := runSkill(t, "edit-checks", f.routes(), bad); err == nil {
		t.Fatalf("impact 4 must be refused (Forward would store it as LOW)")
	}
}

func TestLinkAndUnlinkIncidentUndoEachOtherAndALinkNotReadBackIsUnknown(t *testing.T) {
	f := &incFake{}
	link := `{"network_id":"n1","action":"link_incident","check_id":"9","incident":{"number":"INC0000005"},"apply":true}`
	r, _ := mustRun(t, "edit-checks", f.routes(), link)
	if r.Status != result.OK || !r.Changes[0].Reversible || !strings.Contains(r.Changes[0].Undo, "unlink_incident") {
		t.Fatalf("link: %s %s %+v", r.Status, r.Finding, r.Changes)
	}
	if r, _ = mustRun(t, "edit-checks", f.routes(), link); !strings.Contains(r.Finding, "already linked") || len(f.actions) != 1 {
		t.Fatalf("a second link is a no-op: %s %v", r.Finding, f.actions)
	}
	unlink := strings.Replace(link, "link_incident", "unlink_incident", 1)
	if r, _ = mustRun(t, "edit-checks", f.routes(), unlink); r.Status != result.OK || len(f.links) != 0 {
		t.Fatalf("unlink: %s %s %v", r.Status, r.Finding, f.links)
	}
	g := &incFake{dropLink: true}
	if r, _ = mustRun(t, "edit-checks", g.routes(), link); r.Status != result.Unknown {
		t.Fatalf("a link that does not read back is unknown: %s %s", r.Status, r.Finding)
	}
}

func TestCloseIncidentNeedsTheNumberAsConfirmAndReadsTheStateBack(t *testing.T) {
	f := &incFake{}
	in := `{"network_id":"n1","action":"close_incident","incident":{"number":"INC0000001"},"apply":true`
	if r, _ := mustRun(t, "edit-checks", f.routes(), in+`}`); r.Status == result.OK || len(f.closed) != 0 {
		t.Fatalf("close without confirm: %s %v", r.Finding, f.closed)
	}
	r, _ := mustRun(t, "edit-checks", f.routes(), in+`,"confirm":"INC0000001"}`)
	if r.Status != result.OK || len(f.closed) != 1 || f.closed[0] != "close" || r.Changes[0].Reversible {
		t.Fatalf("close: %s %s %v", r.Status, r.Finding, f.closed)
	}
	if r, _ = mustRun(t, "edit-checks", f.routes(), in+`,"confirm":"INC0000001"}`); !strings.Contains(r.Finding, "already closed") || len(f.closed) != 1 {
		t.Fatalf("closing a closed incident is a no-op: %s", r.Finding)
	}
}

func TestCloseIncidentStillLinkedIsRefusedBeforeSending(t *testing.T) {
	f := &incFake{links: []map[string]any{{"checkId": "9", "number": "INC0000001", "state": "New"}}}
	for _, extra := range []string{`}`, `,"apply":true,"confirm":"INC0000001"}`} {
		r, _ := mustRun(t, "edit-checks", f.routes(), `{"network_id":"n1","action":"close_incident","incident":{"number":"inc0000001"}`+extra)
		if r.Status != result.Failed || len(f.closed) != 0 || !strings.Contains(r.Finding, "unlink") {
			t.Fatalf("a linked incident must be refused, dry run too: %s %s %v", r.Status, r.Finding, f.closed)
		}
	}
}

func TestRefreshIncidentsIsADryRunThenOnePost(t *testing.T) {
	f := &incFake{}
	in := `{"network_id":"n1","action":"refresh_incidents"`
	if r, srv := mustRun(t, "edit-checks", f.routes(), in+`}`); writes(srv) != 0 || r.Changes[0].Applied {
		t.Fatalf("dry run wrote: %d", writes(srv))
	}
	r, _ := mustRun(t, "edit-checks", f.routes(), in+`,"apply":true}`)
	if r.Status != result.OK || len(f.actions) != 1 || f.actions[0] != "refreshServiceNowIncidents" {
		t.Fatalf("refresh: %s %s %v", r.Status, r.Finding, f.actions)
	}
}

func TestViewIncidentsForACheckThatDoesNotExistIsUnknown(t *testing.T) {
	r, _ := mustRun(t, "check-network-compliance", (&incFake{}).routes(), `{"network_id":"n1","view":"incidents","check_id":"404"}`)
	if r.Status != result.Unknown || r.Confidence != result.NoBasis {
		t.Fatalf("missing check: %s %s", r.Status, r.Finding)
	}
}

// Seen live: the dry run said ok for a check that already had an open incident, then apply was refused with a 400. Both modes are refused up
// front now, and nothing is sent; a linked incident that is closed does not block.
func TestRaiseIncidentOnACheckWithAnOpenIncidentIsRefusedBeforeAnythingIsSent(t *testing.T) {
	f := &incFake{links: []map[string]any{{"checkId": "9", "number": "INC0000001", "state": "New"}}}
	for _, tail := range []string{`}`, `,"apply":true,"confirm":"9"}`} {
		r, srv := mustRun(t, "edit-checks", f.routes(), raiseInc+tail)
		if r.Status != result.Failed || !strings.Contains(r.Finding, "already linked to incident INC0000001") || writes(srv) != 0 || len(f.actions) != 0 {
			t.Fatalf("%s: %s %s writes=%d actions=%v", tail, r.Status, r.Finding, writes(srv), f.actions)
		}
	}
	for name, link := range map[string]map[string]any{
		"closed":                {"checkId": "9", "number": "INC0000001", "state": "Closed"},
		"lowercase closed":      {"checkId": "9", "number": "INC0000001", "state": "closed"},
		"open on a other check": {"checkId": "7", "number": "INC0000001", "state": "New"},
	} {
		g := &incFake{links: []map[string]any{link}}
		if r, _ := mustRun(t, "edit-checks", g.routes(), raiseInc+`,"apply":true,"confirm":"9"}`); r.Status != result.OK || len(g.actions) != 1 {
			t.Fatalf("%s does not block: %s %s %v", name, r.Status, r.Finding, g.actions)
		}
	}
	h := &incFake{links: []map[string]any{{"checkId": "9", "number": "INC0000001"}}}
	if r, _ := mustRun(t, "edit-checks", h.routes(), raiseInc+`,"apply":true,"confirm":"9"}`); r.Status != result.Failed || len(h.actions) != 0 {
		t.Fatalf("a link with no reported state counts as open: %s %s %v", r.Status, r.Finding, h.actions)
	}
}
