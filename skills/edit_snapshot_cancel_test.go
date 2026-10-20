package skills_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

// cancelWorld serves snapshot 15429 and its active job rows. Cancelling a row's link removes the rows of that link from the next read, unless stubborn is set (a cooperative stop that has not landed yet).
func cancelWorld(state string, stubborn bool, cancelled *[]string) map[string]fwdtest.Handler {
	rows := []any{
		map[string]any{"jobType": "Protocol Modeling", "orgName": "org-a", "networkId": 7, "snapshotId": 15429, "queued": 0, "running": 1, "durationInSeconds": 900, "totalCount": 1, "cancelLink": "LINK1"},
		map[string]any{"jobType": "NQE Query Computation", "orgName": "org-b", "networkId": 7, "snapshotId": 15429, "queued": 1, "running": 0, "durationInSeconds": 60, "totalCount": 1, "cancelLink": "LINK2"},
		map[string]any{"jobType": "Protocol Modeling", "orgName": "org-a", "networkId": 7, "snapshotId": 99, "queued": 0, "running": 1, "durationInSeconds": 10, "totalCount": 1, "cancelLink": "OTHER"},
	}
	done := map[string]bool{}
	del := func(link string) fwdtest.Handler {
		return func(*http.Request, []byte) (int, any) {
			*cancelled = append(*cancelled, link)
			if !stubborn {
				done[link] = true
			}
			return 200, nil
		}
	}
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/snapshots": fwdtest.Snapshots(fwdtest.Snap("15429", state, "PREDICT", "2026-09-01T00:00:00.000Z")),
		"GET /api/jobs/active": func(*http.Request, []byte) (int, any) {
			var out []any
			for _, r := range rows {
				if !done[r.(map[string]any)["cancelLink"].(string)] {
					out = append(out, r)
				}
			}
			return 200, out
		},
		"DELETE /api/jobs/LINK1": del("LINK1"),
		"DELETE /api/jobs/LINK2": del("LINK2"),
		"DELETE /api/jobs/OTHER": del("OTHER"),
	}
}

// cancel is an explicit admin-gated action on the jobs of ONE snapshot: a dry run lists the rows it would cancel (including other users' jobs), apply needs confirm = the snapshot id, only that snapshot's rows are touched, and the result reads the jobs back and says what the action does not cover.
func TestEditSnapshotCancelListsThenCancelsOnlyThatSnapshotsJobsAndReadsBack(t *testing.T) {
	var cancelled []string
	routes := cancelWorld("PROCESSING", false, &cancelled)
	in := `{"network_id":"n1","snapshot_id":"15429","action":"cancel"`
	r, srv := mustRun(t, "edit-snapshot", routes, in+`}`)
	b := jsonOf(r)
	if r.Mode != result.ModeDryRun || writes(srv) != 0 || len(cancelled) != 0 || !strings.Contains(b, "Protocol Modeling") || !strings.Contains(b, "NQE Query Computation") || strings.Contains(b, "OTHER") || !strings.Contains(r.Finding, `confirm="15429"`) {
		t.Fatalf("dry run: %s %v %s", r.Mode, cancelled, b)
	}
	l := strings.Join(r.Limits, " ")
	for _, want := range []string{"ADMINISTER_SYSTEM", "other users", "per-device", "cooperative"} {
		if !strings.Contains(l, want) {
			t.Errorf("limit lacks %q: %v", want, r.Limits)
		}
	}
	if _, _, err := runSkill(t, "edit-snapshot", routes, in+`,"apply":true}`); err == nil || len(cancelled) != 0 {
		t.Fatalf("apply needs confirm = the snapshot id: %v %v", err, cancelled)
	}
	r, _ = mustRun(t, "edit-snapshot", routes, in+`,"apply":true,"confirm":"15429"}`)
	if r.Status != result.OK || len(cancelled) != 2 || strings.Contains(strings.Join(cancelled, ","), "OTHER") || !strings.Contains(r.Finding, "no active job") {
		t.Fatalf("apply: %s %v %s", r.Status, cancelled, r.Finding)
	}
}

func TestEditSnapshotCancelSaysWhenRowsAreStillListedAndWhenThereIsNothingToCancel(t *testing.T) {
	var cancelled []string
	r, _ := mustRun(t, "edit-snapshot", cancelWorld("PROCESSING", true, &cancelled), `{"network_id":"n1","snapshot_id":"15429","action":"cancel","apply":true,"confirm":"15429"}`)
	if len(cancelled) != 2 || !strings.Contains(r.Finding, "still listed") || r.Status == result.Failed {
		t.Errorf("a cooperative stop that has not landed is reported, not claimed: %s %s", r.Status, r.Finding)
	}
	// no active row: unknown, not success (an import or reprocess is per-device work this cannot stop; a snapshot in the throttle queue is removed by invalidate)
	var none []string
	w := cancelWorld("PROCESSING", false, &none)
	w["GET /api/jobs/active"] = fwdtest.Const(200, []any{})
	r, _ = mustRun(t, "edit-snapshot", w, `{"network_id":"n1","snapshot_id":"15429","action":"cancel"}`)
	if r.Status != result.Unknown || !strings.Contains(strings.Join(r.Limits, " "), "invalidate") {
		t.Errorf("nothing to cancel: %s %v", r.Status, r.Limits)
	}
	// without ADMINISTER_SYSTEM the jobs list is refused: unknown, naming the credential
	w["GET /api/jobs/active"] = fwdtest.Const(403, map[string]any{"message": "Missing permission: ADMINISTER_SYSTEM"})
	r, _ = mustRun(t, "edit-snapshot", w, `{"network_id":"n1","snapshot_id":"15429","action":"cancel"}`)
	if r.Status != result.Unknown || !strings.Contains(r.Finding+strings.Join(r.Limits, " "), "ADMINISTER_SYSTEM") {
		t.Errorf("403: %s %s %v", r.Status, r.Finding, r.Limits)
	}
	// a snapshot id that is not numeric cannot be matched to a job row
	if _, _, err := runSkill(t, "edit-snapshot", cancelWorld("PROCESSING", false, &none), `{"network_id":"n1","snapshot_id":"abc","action":"cancel"}`); err == nil {
		t.Errorf("a non-numeric snapshot id is refused")
	}
}
