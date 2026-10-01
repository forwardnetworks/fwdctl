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
