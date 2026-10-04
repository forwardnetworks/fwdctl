package skills_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

const advPost = "POST /api/snapshots/s1"

// advSnap is a PROCESSED snapshot with the given advanced reachability state ("" leaves the field out, as an older Forward does).
func advSnap(state string) map[string]any {
	sn := fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")
	sn["totalDevices"] = 40
	if state != "" {
		sn["advancedReachabilityState"] = state
	}
	return sn
}

// advRoutes serves the snapshot list; after the POST the list reports afterState. The fakes never reach a real Forward.
func advRoutes(before, afterState string, post fwdtest.Handler) map[string]fwdtest.Handler {
	posted := false
	return map[string]fwdtest.Handler{
		snapsPath: func(r *http.Request, b []byte) (int, any) {
			if posted {
				return fwdtest.Snapshots(advSnap(afterState))(r, b)
			}
			return fwdtest.Snapshots(advSnap(before))(r, b)
		},
		advPost: func(r *http.Request, b []byte) (int, any) {
			posted = true
			return post(r, b)
		},
	}
}

func accept() fwdtest.Handler { return func(*http.Request, []byte) (int, any) { return 204, nil } }

func TestAdvancedReachabilityDryRunWritesNothingAndNamesTheCost(t *testing.T) {
	r, srv := mustRun(t, "edit-advanced-reachability", advRoutes("UNPROCESSED", "PROCESSING", accept()), `{"network_id":"n1","snapshot_id":"s1"}`)
	if r.Status != result.OK || r.Mode != result.ModeDryRun || len(r.Changes) != 1 || r.Changes[0].Applied || r.Changes[0].Reversible || writes(srv) != 0 {
		t.Fatalf("%s %s %+v writes=%d", r.Status, r.Mode, r.Changes, writes(srv))
	}
	l := strings.Join(r.Limits, "|")
	for _, want := range []string{"asynchronous", "compute-heavy", "40 devices", "ADVANCED_REACHABILITY_ANALYSIS"} {
		if !strings.Contains(l, want) {
			t.Errorf("limits miss %q: %s", want, l)
		}
	}
}

func TestAdvancedReachabilityApplyPostsOnceAndReadsTheStateBack(t *testing.T) {
	r, srv := mustRun(t, "edit-advanced-reachability", advRoutes("UNPROCESSED", "PROCESSING", accept()), `{"network_id":"n1","snapshot_id":"s1","apply":true}`)
	if r.Status != result.OK || r.Mode != result.ModeApplied || !r.Changes[0].Applied || r.Changes[0].After != "PROCESSING" || writes(srv) != 1 {
		t.Fatalf("%s %s %+v writes=%d", r.Status, r.Mode, r.Changes, writes(srv))
	}
	sent := false
	for _, c := range srv.Calls() {
		if c.Method == "POST" && c.Query["action"] == "computeAdvancedReachability" {
			sent = true
		}
	}
	if !sent {
		t.Errorf("the write must be action=computeAdvancedReachability: %+v", srv.Calls())
	}
	// Forward accepted but still says UNPROCESSED: said, not hidden
	r, _ = mustRun(t, "edit-advanced-reachability", advRoutes("UNPROCESSED", "UNPROCESSED", accept()), `{"network_id":"n1","snapshot_id":"s1","apply":true}`)
	if !strings.Contains(strings.Join(r.Limits, "|"), "still reports UNPROCESSED") {
		t.Errorf("%v", r.Limits)
	}
}

func TestAdvancedReachabilityRefusesEveryStateButUnprocessedAndWritesNothing(t *testing.T) {
	for state, want := range map[string]string{"PROCESSING": "already computing", "PROCESSED": "already computed", "FAILED": "final state", "CANCELED": "final state", "TIMED_OUT": "final state"} {
		r, srv := mustRun(t, "edit-advanced-reachability", advRoutes(state, state, accept()), `{"network_id":"n1","snapshot_id":"s1","apply":true}`)
		if r.Status != result.Failed || writes(srv) != 0 || !strings.Contains(r.Finding, want) {
			t.Errorf("%s: %s %q writes=%d", state, r.Status, r.Finding, writes(srv))
		}
	}
	if r, srv := mustRun(t, "edit-advanced-reachability", advRoutes("", "", accept()), `{"network_id":"n1","snapshot_id":"s1","apply":true}`); r.Status != result.Unknown || writes(srv) != 0 {
		t.Errorf("no reported state is unknown: %s", r.Status)
	}
	if r, srv := mustRun(t, "edit-advanced-reachability", advRoutes("UNPROCESSED", "", accept()), `{"network_id":"n1","snapshot_id":"nope","apply":true}`); r.Status != result.Unknown || writes(srv) != 0 {
		t.Errorf("a missing snapshot is unknown: %s", r.Status)
	}
	// a snapshot that is not PROCESSED cannot be asked
	routes := map[string]fwdtest.Handler{snapsPath: fwdtest.Snapshots(fwdtest.Snap("s1", "PROCESSING", "COLLECTION", "2026-09-01T00:00:00.000Z"))}
	if r, srv := mustRun(t, "edit-advanced-reachability", routes, `{"network_id":"n1","snapshot_id":"s1","apply":true}`); r.Status != result.Failed || writes(srv) != 0 {
		t.Errorf("not PROCESSED: %s", r.Status)
	}
}

func TestAdvancedReachabilityMapsForwardsRefusals(t *testing.T) {
	cases := []struct {
		status int
		body   any
		want   result.Status
		text   string
	}{
		{409, map[string]any{"message": "snapshot not processed", "errorCode": "SNAPSHOT_UNAVAILABLE", "snapshotState": "PROCESSING"}, result.Failed, ""},
		{404, map[string]any{"message": "no such snapshot"}, result.Unknown, ""},
	}
	for _, c := range cases {
		c := c
		r, _ := mustRun(t, "edit-advanced-reachability", advRoutes("UNPROCESSED", "UNPROCESSED", func(*http.Request, []byte) (int, any) { return c.status, c.body }), `{"network_id":"n1","snapshot_id":"s1","apply":true}`)
		if r.Status != c.want {
			t.Errorf("%d: %s %s", c.status, r.Status, r.Finding)
		}
	}
}

func TestVulnerabilitiesSaysWhyExposureIsUnavailableInsteadOfFailing(t *testing.T) {
	sn := advSnap("UNPROCESSED")
	routes := map[string]fwdtest.Handler{
		snapsPath: fwdtest.Snapshots(sn),
		vulnList:  fwdtest.Const(400, map[string]any{"message": "Cannot filter by internetAddressable: Internet exposure analysis is unavailable (PENDING_ADVANCED_REACHABILITY)"}),
	}
	r, _ := vuln(t, routes, netIn+`,"internet_addressable":true}`)
	l := strings.Join(r.Limits, "|")
	if r.Status != result.Unknown || !strings.Contains(r.Finding, "never computed") || !strings.Contains(l, "unfiltered view still works") || !strings.Contains(strings.Join(r.NextActions, ","), "edit-advanced-reachability") {
		t.Fatalf("%s %q %v %v", r.Status, r.Finding, r.NextActions, r.Limits)
	}
	// a state that is final points at a reprocess; no internet node and disabled computation have their own reasons
	for code, want := range map[string]string{"INTERNET_NODE_NOT_DEFINED": "no internet node", "REACHABILITY_COMPUTATION_DISABLED": "DISABLE_FLOW_COMPUTATION"} {
		routes[vulnList] = fwdtest.Const(400, map[string]any{"message": "Cannot filter by internetAddressable: Internet exposure analysis is unavailable (" + code + ")"})
		if r, _ := vuln(t, routes, netIn+`,"internet_addressable":false}`); r.Status != result.Unknown || !strings.Contains(r.Finding, want) {
			t.Errorf("%s: %s %q", code, r.Status, r.Finding)
		}
	}
	routes[vulnList] = fwdtest.Const(400, map[string]any{"message": "Cannot filter by internetAddressable: Internet exposure analysis is unavailable (PENDING_ADVANCED_REACHABILITY)"})
	routes[snapsPath] = fwdtest.Snapshots(advSnap("FAILED"))
	if r, _ := vuln(t, routes, netIn+`,"internet_addressable":true}`); !strings.Contains(strings.Join(r.NextActions, ","), "edit-snapshot") {
		t.Errorf("a FAILED state points at a reprocess: %v", r.NextActions)
	}
	// a different 400 is still an error: only Forward's own exposure refusal is mapped
	routes[vulnList] = fwdtest.Const(400, map[string]any{"message": "bad request"})
	if _, _, err := runSkill(t, "inspect-vulnerabilities", routes, netIn+`,"internet_addressable":true}`); err == nil {
		t.Errorf("an unrelated 400 must stay an error")
	}
}

func TestVulnerabilitiesCVEViewWithNoInternetValueIsUnknownNotZero(t *testing.T) {
	dev := func(name string, internet any) map[string]any {
		d := map[string]any{"name": name, "osVersion": "1", "result": "VULNERABLE", "status": "VULNERABLE"}
		if internet != nil {
			d["internetAddressable"] = internet
		}
		return d
	}
	get := func(devs ...map[string]any) fwdtest.Handler {
		o := osInfo("HIGH", map[string]int{"VULNERABLE": len(devs)}, false)
		o["devices"] = devs
		return fwdtest.Const(200, map[string]any{"id": "CVE-2024-0001", "description": "d", "osInfos": []map[string]any{o}})
	}
	routes := map[string]fwdtest.Handler{snapsPath: fwdtest.Snapshots(advSnap("UNPROCESSED")), vulnGet: get(dev("a", nil), dev("b", nil))}
	r, _ := vuln(t, routes, netIn+`,"cve_id":"CVE-2024-0001","internet_addressable":true}`)
	if r.Status != result.Unknown || !strings.Contains(r.Finding, "never computed") {
		t.Fatalf("every value null with a filter is unknown with the reason: %s %q", r.Status, r.Finding)
	}
	routes[vulnGet] = get(dev("a", true), dev("b", nil))
	r, _ = vuln(t, routes, netIn+`,"cve_id":"CVE-2024-0001"}`)
	if !strings.Contains(strings.Join(r.Limits, "|"), "null on 1 of 2 device rows") {
		t.Errorf("a null value is said, not shown as no: %v", r.Limits)
	}
}

func TestInspectSnapshotsReportsAdvancedReachabilityForEverySnapshot(t *testing.T) {
	s1, s2 := advSnap("PROCESSED"), fwdtest.Snap("s2", "PROCESSED", "COLLECTION", "2026-09-03T00:00:00.000Z")
	s2["advancedReachabilityState"] = "UNPROCESSED"
	stage := func(id, state string) fwdtest.Handler {
		return fwdtest.Const(200, map[string]any{"networkId": "n1", "snapshotId": id, "done": true, "stages": []any{
			map[string]any{"stage": "REACHABILITY", "operationState": "SUCCEEDED", "startedAt": 1788000000000, "updatedAt": 1788000100000},
			map[string]any{"stage": "ADVANCED_REACHABILITY", "operationState": state, "startedAt": 1788000200000, "updatedAt": 1788000300000},
		}})
	}
	routes := map[string]fwdtest.Handler{snapsPath: fwdtest.Snapshots(s1, s2), "GET /api/snapshots/s1/progress": stage("s1", "SUCCEEDED"), "GET /api/snapshots/s2/progress": stage("s2", "NOT_TRIGGERED")}
	r, _ := mustRun(t, "inspect-snapshots", routes, `{"network_id":"n1"}`)
	d := r.Evidence[0].Detail
	rows := d["snapshots"].([]map[string]any)
	got := map[string]map[string]any{}
	for _, row := range rows {
		got[row["id"].(string)] = row["advanced_reachability"].(map[string]any)
	}
	if got["s1"]["state"] != "PROCESSED" || got["s2"]["state"] != "UNPROCESSED" || got["s1"]["stage_state"] != "SUCCEEDED" || got["s2"]["stage_state"] != "NOT_TRIGGERED" || got["s1"]["updated_at"] == nil {
		t.Fatalf("every processed snapshot says whether advanced reachability ran: %+v", got)
	}
	if !strings.Contains(strings.Join(r.Limits, "|"), "never computed") || !strings.Contains(strings.Join(r.Limits, "|"), "not 'still running'") {
		t.Errorf("UNPROCESSED is explained: %v", r.Limits)
	}
	// the one-snapshot view
	r, _ = mustRun(t, "inspect-snapshots", routes, `{"network_id":"n1","snapshot_id":"s1"}`)
	if a, _ := r.Evidence[0].Detail["advanced_reachability"].(map[string]any); a["state"] != "PROCESSED" || a["started_at"] == nil {
		t.Errorf("detail: %+v", r.Evidence[0].Detail["advanced_reachability"])
	}
	// a failed progress read leaves the state and says so
	delete(routes, "GET /api/snapshots/s1/progress")
	r, _ = mustRun(t, "inspect-snapshots", routes, `{"network_id":"n1"}`)
	if !strings.Contains(strings.Join(r.Limits, "|"), "ADVANCED_REACHABILITY stage could not be read") {
		t.Errorf("%v", r.Limits)
	}
}

func TestAdvancedReachabilityWarnsWhenTheInternetNodeCannotProduceExposure(t *testing.T) {
	routes := advRoutes("UNPROCESSED", "PROCESSING", accept())
	routes["GET /api/networks/n1/internet-node"] = fwdtest.Const(200, map[string]any{"name": "internet", "connections": []any{}})
	r, _ := mustRun(t, "edit-advanced-reachability", routes, `{"network_id":"n1","snapshot_id":"s1"}`)
	if !strings.Contains(strings.Join(r.Limits, "|"), "NO connection") {
		t.Errorf("zero exposed must not be read as safe: %v", r.Limits)
	}
	routes["GET /api/networks/n1/internet-node"] = fwdtest.Const(404, map[string]any{"message": "none"})
	if r, _ := mustRun(t, "edit-advanced-reachability", routes, `{"network_id":"n1","snapshot_id":"s1"}`); !strings.Contains(strings.Join(r.Limits, "|"), "no internet node") {
		t.Errorf("no node is said too: %v", r.Limits)
	}
}
