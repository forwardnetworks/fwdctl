package skills_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

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

// describe says how many devices the committed version edits AND how many the caller's uncommitted draft edits: a change set that holds only BGP advertisements, or was staged and not committed, can show 0 committed devices. The limit says what 0 does and does not prove.
func TestDescribeShowsDraftDevicesAndSaysWhatZeroCommittedDevicesMeans(t *testing.T) {
	r := csRoutes([]any{}, []any{}, []any{})
	r[csListPath] = fwdtest.Const(200, map[string]any{"changeSets": []any{map[string]any{"id": "cs1", "name": "withdraw", "networkId": "n1", "snapshotId": "s1"}}})
	r["GET /api/networks/n1/change-sets/cs1/draft"] = fwdtest.Const(200, map[string]any{"draft": map[string]any{"id": "cs1", "deviceToChanges": map[string]any{"r1": map[string]any{}}}})
	res, _ := mustRun(t, "verify-change", r, `{"network_id":"n1","view":"describe","change_set_id":"cs1"}`)
	b := jsonOf(res)
	l := strings.Join(res.Limits, " ")
	if !strings.Contains(b, `"draft_devices":1`) || !strings.Contains(l, "committed") || !strings.Contains(l, "advertisements") {
		t.Errorf("draft devices and the zero-devices limit: %s %v", b, res.Limits)
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
