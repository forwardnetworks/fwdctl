package skills_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

// tagRoutes is a network with tag "edge" on r1 and tag "lab" on nobody; a POST changes membership the way Forward does.
func tagRoutes(held bool) (map[string]fwdtest.Handler, *map[string][]string) {
	state := map[string][]string{"edge": {"r1"}, "lab": {}}
	list := func(*http.Request, []byte) (int, any) {
		out := []any{}
		for _, n := range []string{"edge", "lab"} {
			ds := []any{}
			for _, d := range state[n] {
				ds = append(ds, d)
			}
			out = append(out, map[string]any{"name": n, "devices": ds})
		}
		return 200, out
	}
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/device-tags": list,
		"POST /api/networks/n1/device-tags": func(r *http.Request, body []byte) (int, any) {
			if !held {
				return 204, nil
			}
			var in struct{ Devices, Tags []string }
			_ = jsonUnmarshal(body, &in)
			for _, t := range in.Tags {
				for _, d := range in.Devices {
					cur := state[t]
					has := false
					for _, x := range cur {
						has = has || x == d
					}
					switch r.URL.Query().Get("action") {
					case "addBatchTo":
						if !has {
							state[t] = append(cur, d)
						}
					case "removeBatchFrom":
						var keep []string
						for _, x := range cur {
							if x != d {
								keep = append(keep, x)
							}
						}
						state[t] = keep
					}
				}
			}
			return 204, nil
		},
	}, &state
}

func TestEditDeviceTagsChangesOnlyThePairsThatDifferAndUndoesExactly(t *testing.T) {
	routes, _ := tagRoutes(true)
	r, srv := mustRun(t, "edit-device-tags", routes, `{"network_id":"n1","action":"add","devices":["r1","r2"],"tags":["edge"]}`)
	if r.Status != result.OK || r.Mode != result.ModeDryRun || len(r.Changes) != 1 || !strings.Contains(r.Changes[0].Target, "r2") || strings.Contains(r.Changes[0].Target, "r1") || writes(srv) != 0 {
		t.Fatalf("dry run must list only r2 and write nothing: %s %+v", r.Status, r.Changes)
	}
	if !strings.Contains(r.Changes[0].Undo, `"remove"`) || !strings.Contains(r.Changes[0].Undo, `"r2"`) {
		t.Errorf("undo: %s", r.Changes[0].Undo)
	}
	r, srv = mustRun(t, "edit-device-tags", routes, `{"network_id":"n1","action":"add","devices":["r1","r2"],"tags":["edge"],"apply":true}`)
	if r.Status != result.OK || !r.Changes[0].Applied || writes(srv) != 1 {
		t.Fatalf("%s %+v writes=%d", r.Status, r.Changes, writes(srv))
	}
	// already in the requested state: nothing to do
	r, srv = mustRun(t, "edit-device-tags", routes, `{"network_id":"n1","action":"add","devices":["r1"],"tags":["edge"],"apply":true}`)
	if len(r.Changes) != 0 || writes(srv) != 0 {
		t.Fatalf("%+v", r.Changes)
	}
}

func TestEditDeviceTagsRefusesAMissingTagAndFailsWhenForwardDoesNotKeepIt(t *testing.T) {
	routes, _ := tagRoutes(true)
	r, srv := mustRun(t, "edit-device-tags", routes, `{"network_id":"n1","action":"add","devices":["r1"],"tags":["nope"],"apply":true}`)
	if r.Status != result.Failed || writes(srv) != 0 || !strings.Contains(r.Finding, "never creates") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	routes, _ = tagRoutes(false)
	r, _ = mustRun(t, "edit-device-tags", routes, `{"network_id":"n1","action":"remove","devices":["r1"],"tags":["edge"],"apply":true}`)
	if r.Status != result.Failed {
		t.Fatalf("a removal Forward did not keep must be failed: %s %s", r.Status, r.Finding)
	}
	if _, _, err := runSkill(t, "edit-device-tags", routes, `{"network_id":"n1","action":"toggle","devices":["r1"],"tags":["edge"]}`); err == nil {
		t.Error("an unknown action must be refused")
	}
}

func overrideRoutes(held bool) map[string]fwdtest.Handler {
	present := []any{map[string]any{"port1": "r1:Gi0/1", "port2": "sw1:Gi0/24"}}
	sn := fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")
	return map[string]fwdtest.Handler{
		snapsPath: fwdtest.Snapshots(sn),
		"GET /api/snapshots/s1/topology/overrides": func(*http.Request, []byte) (int, any) {
			return 200, map[string]any{"present": present, "absent": []any{}}
		},
		"POST /api/snapshots/s1/topology/overrides": func(_ *http.Request, body []byte) (int, any) {
			if held && strings.Contains(string(body), `"presentAdditions"`) {
				present = append(present, map[string]any{"port1": "r2:Gi0/1", "port2": "sw1:Gi0/23"})
			}
			return 204, nil
		},
	}
}

func TestEditLinkOverridesSendsOnlyRealChangesAndGivesTheInverse(t *testing.T) {
	in := `{"network_id":"n1","snapshot_id":"s1","add_present":[{"port1":"sw1:Gi0/24","port2":"r1:Gi0/1"},{"port1":"r2:Gi0/1","port2":"sw1:Gi0/23"}]}`
	r, srv := mustRun(t, "edit-link-overrides", overrideRoutes(true), in)
	if r.Status != result.OK || r.Mode != result.ModeDryRun || len(r.Changes) != 1 || r.Changes[0].After != "1 link edit(s)" || writes(srv) != 0 {
		t.Fatalf("a link already present (in either order) must not count: %s %+v", r.Status, r.Changes)
	}
	if !strings.Contains(r.Changes[0].Undo, "presentRemovals") || !strings.Contains(r.Changes[0].Undo, "r2:Gi0/1") {
		t.Errorf("undo: %s", r.Changes[0].Undo)
	}
	r, srv = mustRun(t, "edit-link-overrides", overrideRoutes(true), strings.TrimSuffix(in, "}")+`,"apply":true}`)
	if r.Status != result.OK || !r.Changes[0].Applied || writes(srv) != 1 {
		t.Fatalf("%s %+v writes=%d", r.Status, r.Changes, writes(srv))
	}
	r, _ = mustRun(t, "edit-link-overrides", overrideRoutes(false), strings.TrimSuffix(in, "}")+`,"apply":true}`)
	if r.Status != result.Failed {
		t.Fatalf("overrides Forward did not keep must be failed: %s", r.Status)
	}
	r, srv = mustRun(t, "edit-link-overrides", overrideRoutes(true), `{"network_id":"n1","snapshot_id":"nope","add_absent":[{"port1":"a","port2":"b"}],"apply":true}`)
	if r.Status != result.Unknown || writes(srv) != 0 {
		t.Fatalf("%s", r.Status)
	}
	if _, _, err := runSkill(t, "edit-link-overrides", overrideRoutes(true), `{"network_id":"n1","snapshot_id":"s1","add_absent":[{"port1":"a","port2":"a"}]}`); err == nil {
		t.Error("a link to itself must be refused")
	}
}

// Results point at the owning playbook when they are not a plain success, and a dry run points at the write protocol.
func TestResultsNamePlaybooksAndTheWriteProtocol(t *testing.T) {
	routes, _ := tagRoutes(true)
	r, _ := mustRun(t, "edit-device-tags", routes, `{"network_id":"n1","action":"add","devices":["r2"],"tags":["edge"]}`)
	if !slicesContains(r.NextActions, "plan-safe-write") {
		t.Errorf("a dry run must point at plan-safe-write: %v", r.NextActions)
	}
	r, _ = mustRun(t, "edit-device-tags", routes, `{"network_id":"n1","action":"add","devices":["r2"],"tags":["nope"]}`)
	if r.Status != result.Failed || !slicesContains(r.NextActions, "plan-device-audit") {
		t.Errorf("a failed result must point at its playbook: %s %v", r.Status, r.NextActions)
	}
}

func slicesContains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// A synthetic node driven by an NQE query: the dry run previews with Forward's compute and writes nothing; a query Forward cannot use is refused; apply
// attaches it and reads the node back; the undo names the previous query.
func syntheticRoutes(computeErr string, keep bool) map[string]fwdtest.Handler {
	queryID := "Q_old"
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/l3-vpns/dir": func(*http.Request, []byte) (int, any) {
			return 200, map[string]any{"name": "dir", "connections": []any{}, "queryId": queryID}
		},
		"POST /api/networks/n1/l3-vpns": func(r *http.Request, _ []byte) (int, any) {
			if computeErr != "" {
				return 200, map[string]any{"error": map[string]any{"errorMsg": "wrong row type", "status": computeErr}}
			}
			return 200, map[string]any{"connections": []any{map[string]any{"uplinkPort": map[string]any{"device": "r1", "port": "e1"}}}}
		},
		"PATCH /api/networks/n1/l3-vpns/dir": func(_ *http.Request, body []byte) (int, any) {
			if keep {
				if strings.Contains(string(body), "null") {
					queryID = ""
				} else {
					queryID = "Q_new"
				}
			}
			return 200, map[string]any{"name": "dir", "connections": []any{}, "queryId": queryID}
		},
		"GET /api/synthetic-device-queries": fwdtest.Const(200, map[string]any{"queries": []any{map[string]any{"queryId": "Q_new", "path": "/Team/WAN connections"}}}),
	}
}

func TestEditSyntheticQueryPreviewsRefusesAWrongQueryAndVerifiesTheWrite(t *testing.T) {
	in := `{"network_id":"n1","kind":"l3vpn","name":"dir","query_id":"Q_new"`
	r, srv := mustRun(t, "edit-synthetic-query", syntheticRoutes("", true), in+`}`)
	if r.Status != result.OK || r.Mode != result.ModeDryRun || len(r.Changes) != 1 || r.Changes[0].Before != "Q_old" || !strings.Contains(r.Finding, "1 connection") || writes(srv) != 1 {
		// the compute is a POST by wire but a preview by meaning: it is the only "write" call in a dry run
		t.Fatalf("%s %s %+v writes=%d", r.Status, r.Finding, r.Changes, writes(srv))
	}
	for _, c := range srv.Calls() {
		if c.Method == "PATCH" {
			t.Fatalf("a dry run must not PATCH: %+v", srv.Calls())
		}
	}
	if !strings.Contains(r.Changes[0].Undo, `"Q_old"`) {
		t.Errorf("undo: %s", r.Changes[0].Undo)
	}
	r, srv = mustRun(t, "edit-synthetic-query", syntheticRoutes("COLUMN_DATATYPE_MISMATCH", true), in+`,"apply":true}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "COLUMN_DATATYPE_MISMATCH") {
		t.Fatalf("a query Forward cannot use must be refused: %s %s", r.Status, r.Finding)
	}
	for _, c := range srv.Calls() {
		if c.Method == "PATCH" {
			t.Fatalf("a refused query must not be attached")
		}
	}
	r, _ = mustRun(t, "edit-synthetic-query", syntheticRoutes("", true), in+`,"apply":true}`)
	if r.Status != result.OK || !r.Changes[0].Applied {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	r, _ = mustRun(t, "edit-synthetic-query", syntheticRoutes("", false), in+`,"apply":true}`)
	if r.Status != result.Failed {
		t.Fatalf("a node that does not hold the new query is failed: %s", r.Status)
	}
	// detach, and the listing mode
	r, _ = mustRun(t, "edit-synthetic-query", syntheticRoutes("", true), `{"network_id":"n1","kind":"l3vpn","name":"dir","detach":true,"apply":true}`)
	if r.Status != result.OK || r.Changes[0].Action != "detach_query" {
		t.Fatalf("detach: %s %+v", r.Status, r.Changes)
	}
	r, srv = mustRun(t, "edit-synthetic-query", syntheticRoutes("", true), `{"network_id":"n1","kind":"l3vpn","name":"dir"}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "1 saved query") || writes(srv) != 0 {
		t.Fatalf("listing: %s %s", r.Status, r.Finding)
	}
	if _, _, err := runSkill(t, "edit-synthetic-query", syntheticRoutes("", true), `{"network_id":"n1","kind":"l3vpn","name":"dir","query_id":"not-an-id"}`); err == nil {
		t.Error("a query id that is not Q_ or FQ_ must be refused")
	}
}

// WAN circuits: exactly two connections, read before and after, undo is the prior circuit; the optional backdate lists what it would invalidate
// and is only sent on apply, after the circuit change.
func wanRoutes(held bool) map[string]fwdtest.Handler {
	circuit := map[string]any{"name": "wan-01", "connection1": map[string]any{"device": "r1", "port": "Gi0/1", "vlan": 100}, "connection2": map[string]any{"device": "r2", "port": "Gi0/1", "vlan": 200}}
	snap := func(id, at string) map[string]any {
		x := fwdtest.Snap(id, "PROCESSED", "COLLECTION", at)
		return x
	}
	return map[string]fwdtest.Handler{
		snapsPath: fwdtest.Snapshots(snap("s1", "2026-09-01T00:00:00.000Z"), snap("s2", "2026-09-02T00:00:00.000Z"), snap("s3", "2026-09-03T00:00:00.000Z")),
		"GET /api/networks/n1/wan-circuits/wan-01": func(*http.Request, []byte) (int, any) {
			if circuit == nil {
				return 404, map[string]any{}
			}
			return 200, circuit
		},
		"PUT /api/networks/n1/wan-circuits/wan-01": func(_ *http.Request, body []byte) (int, any) {
			if held {
				var c map[string]any
				_ = jsonUnmarshal2(body, &c)
				circuit = c
			}
			return 204, nil
		},
		"DELETE /api/networks/n1/wan-circuits/wan-01": func(*http.Request, []byte) (int, any) {
			if held {
				circuit = nil
			}
			return 204, nil
		},
		"POST /api/networks/n1/wan-circuits": fwdtest.Const(204, nil),
	}
}

func TestEditWanCircuitReadsBeforeWritesVerifiesAndBackdatesOnlyWhenAsked(t *testing.T) {
	in := `{"network_id":"n1","name":"wan-01","connection1":{"device":"r1","port":"Gi0/1","vlan":150},"connection2":{"device":"r2","port":"Gi0/1","vlan":200}`
	r, srv := mustRun(t, "edit-wan-circuit", wanRoutes(true), in+`}`)
	if r.Status != result.OK || r.Mode != result.ModeDryRun || len(r.Changes) != 1 || !strings.Contains(fmt.Sprint(r.Changes[0].Before), `"vlan":100`) || !strings.Contains(fmt.Sprint(r.Changes[0].After), `"vlan":150`) || writes(srv) != 0 {
		t.Fatalf("%s %s %+v writes=%d", r.Status, r.Finding, r.Changes, writes(srv))
	}
	r, srv = mustRun(t, "edit-wan-circuit", wanRoutes(true), in+`,"apply":true}`)
	if r.Status != result.OK || !r.Changes[0].Applied || writes(srv) != 1 {
		t.Fatalf("a plain apply writes the circuit once and never backdates: %s %s writes=%d", r.Status, r.Finding, writes(srv))
	}
	// backdate: the dry run names the snapshots from s2 onward and sends nothing; apply sends the circuit change then the backdate
	r, srv = mustRun(t, "edit-wan-circuit", wanRoutes(true), in+`,"backdate_snapshot_id":"s2"}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "invalidating 2 snapshot") || writes(srv) != 0 {
		t.Fatalf("dry run with backdate: %s %s", r.Status, r.Finding)
	}
	r, srv = mustRun(t, "edit-wan-circuit", wanRoutes(true), in+`,"backdate_snapshot_id":"s2","apply":true}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "backdated to snapshot s2") || writes(srv) != 2 {
		t.Fatalf("apply with backdate: %s %s writes=%d", r.Status, r.Finding, writes(srv))
	}
	if r, _ := mustRun(t, "edit-wan-circuit", wanRoutes(true), in+`,"backdate_snapshot_id":"nope"}`); r.Status != result.Unknown {
		t.Fatalf("an unknown snapshot to backdate to is unknown: %s", r.Status)
	}
	if r, _ := mustRun(t, "edit-wan-circuit", wanRoutes(false), in+`,"apply":true}`); r.Status != result.Failed {
		t.Fatalf("a circuit Forward did not keep is failed: %s", r.Status)
	}
	r, _ = mustRun(t, "edit-wan-circuit", wanRoutes(true), `{"network_id":"n1","name":"wan-01","delete":true,"apply":true}`)
	if r.Status != result.OK || r.Changes[0].Action != "delete_wan_circuit" {
		t.Fatalf("delete: %s %+v", r.Status, r.Changes)
	}
	for _, bad := range []string{`{"network_id":"n1","name":"w","connection1":{"device":"r1","port":"p"}}`, `{"network_id":"n1","name":"w","delete":true,"connection1":{"device":"r1","port":"p"}}`, `{"network_id":"n1","name":"w","connection1":{"device":"r1","port":"p","vlan":0},"connection2":{"device":"r2","port":"p"}}`} {
		if _, _, err := runSkill(t, "edit-wan-circuit", wanRoutes(true), bad); err == nil {
			t.Errorf("must refuse: %s", bad)
		}
	}
}

func jsonUnmarshal2(b []byte, v any) error { return json.Unmarshal(b, v) }

// A node driven by a long query: the summary says what it is made of, and one node can be read in full and paged in a stable order.
func TestExternalNodeViewSummarisesAndPagesTheQueryConnections(t *testing.T) {
	conns := []any{}
	for i := 0; i < 60; i++ {
		vrf := []string{"BLUE", "ASU", "HHSC"}[i%3]
		dev := []string{"NXWAN-01", "NXWAN-02"}[i%2]
		conns = append(conns, map[string]any{"uplinkPort": map[string]any{"device": dev, "port": "po1000"}, "gatewayPort": map[string]any{"device": dev, "port": "po1000." + fmt.Sprint(700+i)}, "vlan": 700 + i, "vrf": vrf, "subnetAutoDiscovery": "BGP_ROUTES", "source": "NQE"})
	}
	routes := map[string]fwdtest.Handler{
		snapsPath:                                 fwdtest.Snapshots(fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")),
		"GET /api/networks/n1/l3-vpns/dir":        fwdtest.Const(200, map[string]any{"name": "dir", "connections": []any{}, "queryId": "Q_gone", "queryResult": map[string]any{"connections": conns}}),
		"GET /api/nqe/repos/org/commits/head":     fwdtest.Const(200, "c1"),
		"GET /api/nqe/queries/Q_gone/source-code": fwdtest.Const(404, map[string]any{}),
	}
	r, _ := mustRun(t, "inspect-topology", routes, `{"network_id":"n1","kind":"external","device":"dir","limit":10,"offset":20}`)
	if r.Status != result.OK {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	b, _ := json.Marshal(r.Evidence)
	text := string(b)
	for _, want := range []string{`"connection_count":60`, `"distinct_uplinks":2`, `"distinct_vrfs":3`, `"returned":10`, `"offset":20`, `"gateway"`, `"subnet_discovery":"BGP_ROUTES"`, `"in_library":false`} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %s in %s", want, text[:min(len(text), 600)])
		}
	}
	limitText := strings.Join(r.Limits, " ")
	if !strings.Contains(limitText, "NOT in the organization's committed library") {
		t.Errorf("must say the query is not in the library: %s", limitText)
	}
	if r, _ := mustRun(t, "inspect-topology", routes, `{"network_id":"n1","kind":"external","device":"nope"}`); r.Status != result.Unknown {
		t.Errorf("an unknown node is unknown: %s", r.Status)
	}
}

// Forward answers {} when it has no internet-connection suggestions; that is "none", not an error, and the edge reader must still work.
func TestExternalReaderTreatsAnEmptySuggestionsAnswerAsNone(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		snapsPath:                            fwdtest.Snapshots(fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")),
		"GET /api/networks/n1/internet-node": fwdtest.Const(200, map[string]any{"name": "internet", "connections": []any{}}),
		"GET /api/networks/n1/internet-node/connection-suggestions": fwdtest.Const(200, map[string]any{}),
	}
	r, _ := mustRun(t, "inspect-topology", routes, `{"network_id":"n1","kind":"external"}`)
	for _, l := range r.Limits {
		if strings.Contains(l, "suggested internet connections could not be read") {
			t.Fatalf("an empty {} must not be reported as unreadable: %v", r.Limits)
		}
	}
}

func inetRoutes(held bool, initial []any) map[string]fwdtest.Handler {
	excl := initial
	node := func() map[string]any {
		return map[string]any{"name": "internet", "connections": []any{map[string]any{"uplinkPort": map[string]any{"device": "r1", "port": "Gi0/0"}, "subnetAutoDiscovery": "IP_ROUTES"}}, "subnetsToExclude": excl}
	}
	return map[string]fwdtest.Handler{
		snapsPath:                            fwdtest.Snapshots(fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")),
		"GET /api/networks/n1/internet-node": func(*http.Request, []byte) (int, any) { return 200, node() },
		"PATCH /api/networks/n1/internet-node": func(_ *http.Request, body []byte) (int, any) {
			var b map[string]any
			_ = jsonUnmarshal2(body, &b)
			if held {
				excl, _ = b["subnetsToExclude"].([]any)
			}
			return 200, node()
		},
	}
}

func TestEditInternetExclusionsShowsTheWholeListWritesItOnceAndVerifies(t *testing.T) {
	cur := []any{"198.51.100.0/24"}
	r, srv := mustRun(t, "edit-internet-exclusions", inetRoutes(true, cur), `{"network_id":"n1","add":["198.18.0.0/16","198.19.0.0/16"]}`)
	if r.Status != result.OK || r.Mode != result.ModeDryRun || writes(srv) != 0 || !strings.Contains(fmt.Sprint(r.Changes[0].Before), "198.51.100.0/24") ||
		!strings.Contains(fmt.Sprint(r.Changes[0].After), "198.51.100.0/24") || !strings.Contains(fmt.Sprint(r.Changes[0].After), "198.19.0.0/16") {
		t.Fatalf("dry run keeps the existing entry and sends nothing: %s %s %+v writes=%d", r.Status, r.Finding, r.Changes, writes(srv))
	}
	r, srv = mustRun(t, "edit-internet-exclusions", inetRoutes(true, cur), `{"network_id":"n1","add":["198.18.0.0/16"],"apply":true}`)
	if r.Status != result.OK || !r.Changes[0].Applied || writes(srv) != 1 {
		t.Fatalf("apply: %s %s writes=%d", r.Status, r.Finding, writes(srv))
	}
	for _, c := range srv.Calls() {
		if c.Method == "PATCH" && fmt.Sprint(c.Body["subnetsToExclude"]) != "[198.18.0.0/16 198.51.100.0/24]" {
			t.Errorf("the write must carry the WHOLE list, got %v", c.Body)
		}
		if c.Method == "PATCH" && len(c.Body) != 1 {
			t.Errorf("only subnetsToExclude may be sent: %v", c.Body)
		}
	}
	if r, _ := mustRun(t, "edit-internet-exclusions", inetRoutes(false, cur), `{"network_id":"n1","add":["198.18.0.0/16"],"apply":true}`); r.Status != result.Failed {
		t.Fatalf("a list Forward did not keep is failed: %s", r.Status)
	}
	if r, srv := mustRun(t, "edit-internet-exclusions", inetRoutes(true, cur), `{"network_id":"n1","add":["198.51.100.0/24"],"apply":true}`); r.Status != result.OK || writes(srv) != 0 {
		t.Fatalf("already excluded is a no-op: %s writes=%d", r.Status, writes(srv))
	}
	r, _ = mustRun(t, "edit-internet-exclusions", inetRoutes(true, cur), `{"network_id":"n1","set":[]}`)
	if r.Status != result.OK || !strings.Contains(strings.Join(r.Limits, " "), "CLEARS the whole list") {
		t.Fatalf("clearing is stated: %v", r.Limits)
	}
	for _, bad := range []string{`"add":["10.0.0.0/8"]`, `"add":["198.18.0.1/16"]`, `"add":["nonsense"]`, `"add":["8.8.8.0/24"],"set":[]`, `"apply":true`} {
		if _, _, err := runSkill(t, "edit-internet-exclusions", inetRoutes(true, cur), `{"network_id":"n1",`+bad+`}`); err == nil {
			t.Errorf("%s must be refused before anything is sent", bad)
		}
	}
}

// The dry run proves what a candidate query changes (by uplink, VRF, VLAN) and says up front when the node's current query is gone from the library.
func TestEditSyntheticQueryDiffsTheConnectionsAndWarnsWhenTheUndoIsGone(t *testing.T) {
	conn := func(dev, vrf string, vlan int) map[string]any {
		return map[string]any{"uplinkPort": map[string]any{"device": dev, "port": "po1"}, "vlan": vlan, "vrf": vrf, "subnetAutoDiscovery": "BGP_ROUTES"}
	}
	routes := syntheticRoutes("", true)
	routes["GET /api/networks/n1/l3-vpns/dir"] = fwdtest.Const(200, map[string]any{"name": "dir", "connections": []any{}, "queryId": "Q_old",
		"queryResult": map[string]any{"connections": []any{conn("a", "ASU", 1), conn("a", "INET", 2), conn("b", "INET", 2), conn("b", "HHSC", 3)}}})
	routes["POST /api/networks/n1/l3-vpns"] = fwdtest.Const(200, map[string]any{"connections": []any{conn("a", "ASU", 1), conn("b", "HHSC", 3), conn("c", "NEW", 4)}})
	routes["GET /api/nqe/repos/org/commits/head"] = fwdtest.Const(200, "c1")
	routes["GET /api/nqe/queries/Q_old/source-code"] = fwdtest.Const(404, map[string]any{})
	r, _ := mustRun(t, "edit-synthetic-query", routes, `{"network_id":"n1","kind":"l3vpn","name":"dir","query_id":"Q_new"}`)
	b, _ := json.Marshal(r.Evidence)
	text := string(b)
	for _, want := range []string{`"before_count":4`, `"after_count":3`, `"added_count":1`, `"removed_count":2`, `"changed_count":0`, `"undo_possible":false`} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %s in %s", want, text[:min(len(text), 900)])
		}
	}
	if r.Changes[0].Reversible || !strings.Contains(r.Changes[0].Undo, "cannot be undone") || !strings.Contains(strings.Join(r.Limits, " "), "UNDO IS NOT POSSIBLE") {
		t.Errorf("an undo to a query the library lost must be stated before the change: %+v %v", r.Changes[0], r.Limits)
	}
}
