package skills_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

const (
	csListPath  = "GET /api/networks/n1/change-sets"
	csPredPath  = "GET /api/networks/n1/change-sets/cs1/predicted-snapshots"
	csCheckPath = "GET /api/networks/n1/change-sets/cs1/checks"
)

func csRoutes(preds any, base, after any) map[string]fwdtest.Handler {
	r := map[string]fwdtest.Handler{
		csListPath: fwdtest.Const(200, map[string]any{"changeSets": []any{map[string]any{"id": "cs1", "name": "open port", "networkId": "n1", "snapshotId": "s1",
			"deviceToChanges": map[string]any{"fw1": map[string]any{}, "r1": map[string]any{}}}}}),
		csPredPath: fwdtest.Const(200, map[string]any{"snapshots": preds}),
	}
	r["GET /api/networks/n1/change-sets/cs1/commits/head"] = fwdtest.Const(200, map[string]any{"id": "cs1", "commitId": "c1", "deviceToChanges": map[string]any{"fw1": map[string]any{"hasSecurityRuleChanges": true}, "r1": map[string]any{"commands": "x"}}})
	r["GET /api/networks/n1/change-sets/cs1/draft"] = fwdtest.Const(200, map[string]any{"draft": nil})
	r[csCheckPath] = func(req *http.Request, _ []byte) (int, any) {
		if req.URL.Query().Get("snapshotId") == "p1" {
			return 200, after
		}
		return 200, base
	}
	return r
}

func csChk(id, status string, viol int) map[string]any {
	return map[string]any{"id": id, "name": "check " + id, "status": status, "numViolations": viol}
}

func TestReviewChangeSetFailsWhenACheckGotWorseOnThePrediction(t *testing.T) {
	preds := []any{map[string]any{"id": "p1", "state": "PROCESSED", "createdAt": "2026-09-30T00:00:00Z"}}
	r, srv := mustRun(t, "review-change-set", csRoutes(preds,
		map[string]any{"checks": []any{csChk("c1", "PASS", 0), csChk("c2", "FAIL", 3)}},
		map[string]any{"checks": []any{csChk("c1", "FAIL", 2), csChk("c2", "FAIL", 3)}}), `{"network_id":"n1","change_set_id":"cs1"}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "check c1") || strings.Contains(r.Finding, "check c2") {
		t.Fatalf("only the check that got worse is named: %s %s", r.Status, r.Finding)
	}
	if !strings.Contains(fmt.Sprint(r.Evidence[0].Detail), "devices_edited:[fw1 r1]") {
		t.Errorf("%v", r.Evidence[0].Detail)
	}
	for _, c := range srv.Calls() {
		if c.Method != "GET" {
			t.Errorf("a review must only read, saw %s %s", c.Method, c.Path)
		}
	}
}

func TestReviewChangeSetIsUnknownWhenNeverPredictedOrMissing(t *testing.T) {
	r, _ := mustRun(t, "review-change-set", csRoutes([]any{}, map[string]any{"checks": []any{}}, nil), `{"network_id":"n1","change_set_id":"cs1"}`)
	if r.Status != result.Unknown || !strings.Contains(strings.Join(r.Limits, "|"), "has not been predicted") {
		t.Fatalf("%s %v", r.Status, r.Limits)
	}
	r, _ = mustRun(t, "review-change-set", csRoutes(nil, nil, nil), `{"network_id":"n1","change_set_id":"nope"}`)
	if r.Status != result.Unknown {
		t.Fatalf("a missing change set is unknown: %s", r.Status)
	}
}

func TestReviewChangeSetOKNeedsAPredictionAndSaysWhenThereAreNoChecks(t *testing.T) {
	preds := []any{map[string]any{"id": "p1", "state": "PROCESSED", "createdAt": "2026-09-30T00:00:00Z"}}
	r, _ := mustRun(t, "review-change-set", csRoutes(preds, map[string]any{"checks": []any{csChk("c1", "PASS", 0)}},
		map[string]any{"checks": []any{}}), `{"network_id":"n1","change_set_id":"cs1"}`)
	if r.Status != result.OK || !strings.Contains(strings.Join(r.Limits, "|"), "no checks attached") {
		t.Fatalf("%s %v", r.Status, r.Limits)
	}
}

// describe reads the change set's content from its head commit and its draft (the change-set list never carries the per-device changes): the devices it edits, and per device the staged commands, a whole-config replacement, pending firewall rule changes, and the BGP advertisements it injects, modifies or withdraws. A change set that holds only BGP advertisements lists its devices.
func TestDescribeListsTheStagedAdvertisementsPerDevice(t *testing.T) {
	r := csRoutes([]any{}, []any{}, []any{})
	r[csListPath] = fwdtest.Const(200, map[string]any{"changeSets": []any{map[string]any{"id": "cs1", "name": "withdraw", "networkId": "n1", "snapshotId": "s1"}}})
	r["GET /api/networks/n1/change-sets/cs1/commits/head"] = fwdtest.Const(200, map[string]any{"id": "cs1", "commitId": "c1", "deviceToChanges": map[string]any{
		"r1": map[string]any{"hasConfig": false, "hasSecurityRuleChanges": false, "removedAdvertisements": []any{map[string]any{"vrf": "default", "externalPeer": "192.0.2.1", "prefix": "203.0.113.0/24"}}},
		"r2": map[string]any{"commands": "interface lo1\n description x", "hasConfig": false, "hasSecurityRuleChanges": false,
			"addedAdvertisements": []any{map[string]any{"vrf": "default", "externalPeer": "192.0.2.9", "prefix": "198.51.100.0/24", "nextHop": "192.0.2.9", "type": "EBGP"}}},
		"fw1": map[string]any{"hasConfig": false, "hasSecurityRuleChanges": true},
		"r3":  map[string]any{"hasConfig": false, "hasSecurityRuleChanges": false}, // an empty entry (a restore left nothing): not edited
	}})
	r["GET /api/networks/n1/change-sets/cs1/draft"] = fwdtest.Const(200, map[string]any{"draft": nil})
	res, _ := mustRun(t, "verify-change", r, `{"network_id":"n1","view":"describe","change_set_id":"cs1"}`)
	b := jsonOf(res)
	for _, want := range []string{`"devices_edited":["fw1","r1","r2"]`, `"withdrawn_advertisements"`, `203.0.113.0/24`, `"injected_or_modified_advertisements"`, `198.51.100.0/24`, `"has_security_rule_changes":true`, `"commands":2`} {
		if !strings.Contains(b, want) {
			t.Errorf("missing %s in %s", want, b)
		}
	}
	if strings.Contains(b, `"r3"`) && strings.Contains(b, `"devices_edited":["fw1","r1","r2","r3"]`) {
		t.Errorf("an empty entry is not an edited device")
	}
	// a change set with only a draft (nothing committed) shows its draft devices
	r["GET /api/networks/n1/change-sets/cs1/commits/head"] = fwdtest.Const(404, map[string]any{"message": "no commit"})
	r["GET /api/networks/n1/change-sets/cs1/draft"] = fwdtest.Const(200, map[string]any{"draft": map[string]any{"id": "cs1", "deviceToChanges": map[string]any{
		"r1": map[string]any{"removedAdvertisements": []any{map[string]any{"vrf": "default", "externalPeer": "192.0.2.1", "prefix": "203.0.113.0/24"}}}}}})
	res, _ = mustRun(t, "verify-change", r, `{"network_id":"n1","view":"describe","change_set_id":"cs1"}`)
	b = jsonOf(res)
	if !strings.Contains(b, `"draft_devices":1`) || !strings.Contains(b, `"devices_edited":[]`) || !strings.Contains(strings.Join(res.Limits, " "), "uncommitted") {
		t.Errorf("draft only: %s %v", b, res.Limits)
	}
}

// A predicted snapshot reports UNPROCESSED while it is still being created or computed (inspect-snapshots shows the stage). list and describe mark each prediction finished or not, so a waiter does not treat "not PROCESSING" as done.
func TestPredictionStatesSayWhichAreFinished(t *testing.T) {
	r := csRoutes([]any{map[string]any{"id": "p1", "state": "UNPROCESSED"}, map[string]any{"id": "p0", "state": "PROCESSED"}}, []any{}, []any{})
	res, _ := mustRun(t, "verify-change", r, `{"network_id":"n1","view":"describe","change_set_id":"cs1"}`)
	b := jsonOf(res)
	if !strings.Contains(b, `"finished":false`) || !strings.Contains(b, `"finished":true`) || !strings.Contains(strings.Join(res.Limits, " "), "UNPROCESSED") {
		t.Errorf("finished flags: %s %v", b, res.Limits)
	}
	l := map[string]fwdtest.Handler{
		"GET /api/networks/n1/change-sets": fwdtest.Const(200, map[string]any{"changeSets": []any{map[string]any{"id": "cs1", "name": "x", "snapshotId": "s1"}}}),
		csPredPath:                         fwdtest.Const(200, map[string]any{"snapshots": []any{map[string]any{"id": "p1", "state": "UNPROCESSED"}}}),
	}
	res, _ = mustRun(t, "verify-change", l, `{"network_id":"n1","view":"list"}`)
	if !strings.Contains(jsonOf(res), `"finished":false`) || !strings.Contains(strings.Join(res.Limits, " "), "UNPROCESSED") {
		t.Errorf("list: %s %v", jsonOf(res), res.Limits)
	}
}

// A prediction that has not finished carries where its processing stands (stage, state, elapsed against Forward's estimate, a past-the-estimate flag), in describe and in list, so a stuck one shows early. A finished prediction carries none.
func TestUnfinishedPredictionsCarryTheirProcessingProgress(t *testing.T) {
	start := time.Now().Add(-405 * time.Second).UnixMilli()
	r := csRoutes([]any{map[string]any{"id": "p1", "state": "UNPROCESSED"}, map[string]any{"id": "p0", "state": "PROCESSED"}}, []any{}, []any{})
	r["GET /api/snapshots/p1/progress"] = fwdtest.Const(200, map[string]any{"snapshotId": "p1", "done": false, "stages": []any{
		map[string]any{"stage": "CREATION", "operationState": "COMPUTING", "startedAt": start, "numObjects": 4500}}})
	r["GET /api/snapshots/p1/processEstimate"] = fwdtest.Const(200, map[string]any{"stageToDuration": map[string]any{"CREATION": 216000}})
	res, _ := mustRun(t, "verify-change", r, `{"network_id":"n1","view":"describe","change_set_id":"cs1"}`)
	b := jsonOf(res)
	for _, want := range []string{`"stage":"CREATION"`, `"stage_state":"COMPUTING"`, `"estimate_total_seconds":216`, `"over_estimate":true`, `"objects":4500`} {
		if !strings.Contains(b, want) {
			t.Errorf("describe: missing %s in %s", want, b)
		}
	}
	if strings.Count(b, `"processing"`) != 1 {
		t.Errorf("only the unfinished prediction carries processing: %s", b)
	}
	l := map[string]fwdtest.Handler{
		"GET /api/networks/n1/change-sets": fwdtest.Const(200, map[string]any{"changeSets": []any{map[string]any{"id": "cs1", "name": "x", "snapshotId": "s1"}}}),
		csPredPath:                         fwdtest.Const(200, map[string]any{"snapshots": []any{map[string]any{"id": "p1", "state": "UNPROCESSED"}}}),
		"GET /api/networks/n1/change-sets/cs1/commits/head": fwdtest.Const(404, map[string]any{"message": "none"}),
		"GET /api/networks/n1/change-sets/cs1/draft":        fwdtest.Const(200, map[string]any{"draft": nil}),
		"GET /api/snapshots/p1/progress":                    r["GET /api/snapshots/p1/progress"],
		"GET /api/snapshots/p1/processEstimate":             r["GET /api/snapshots/p1/processEstimate"],
	}
	res, _ = mustRun(t, "verify-change", l, `{"network_id":"n1","view":"list"}`)
	if !strings.Contains(jsonOf(res), `"over_estimate":true`) {
		t.Errorf("list: %s", jsonOf(res))
	}
}

// describe lists each check attached to the change set, with what it is (name, type and note from its definition) and its result on the base and on the newest prediction, so a use case's own checks are visible one by one and not only as counts.
func TestDescribeListsEachChangeSetCheckWithBaseAndPredictionResult(t *testing.T) {
	base := []any{map[string]any{"id": "c1", "name": "default route absent on edge", "status": "PASS", "numViolations": 0, "definition": map[string]any{"checkType": "NQE", "note": "no 0.0.0.0/0 on edge routers"}},
		map[string]any{"id": "c2", "name": "lab reaches 8.8.8.8", "status": "PASS", "definition": map[string]any{"type": "REACHABILITY"}}}
	after := []any{map[string]any{"id": "c1", "name": "default route absent on edge", "status": "FAIL", "numViolations": 3, "definition": map[string]any{"checkType": "NQE", "note": "no 0.0.0.0/0 on edge routers"}},
		map[string]any{"id": "c2", "name": "lab reaches 8.8.8.8", "status": "PASS", "definition": map[string]any{"type": "REACHABILITY"}}}
	r := csRoutes([]any{map[string]any{"id": "p1", "state": "PROCESSED"}}, map[string]any{"checks": base}, map[string]any{"checks": after})
	r["GET /api/networks/n1/change-sets/cs1/commits/head"] = fwdtest.Const(404, map[string]any{"message": "none"})
	r["GET /api/networks/n1/change-sets/cs1/draft"] = fwdtest.Const(200, map[string]any{"draft": nil})
	res, _ := mustRun(t, "verify-change", r, `{"network_id":"n1","view":"describe","change_set_id":"cs1"}`)
	b := jsonOf(res)
	for _, want := range []string{`"checks":[`, `"name":"default route absent on edge"`, `"type":"NQE"`, `"note":"no 0.0.0.0/0 on edge routers"`, `"status_on_base":"PASS"`, `"status_on_prediction":"FAIL"`, `"violations_on_prediction":3`, `"type":"REACHABILITY"`} {
		if !strings.Contains(b, want) {
			t.Errorf("missing %s in %s", want, b)
		}
	}
}
