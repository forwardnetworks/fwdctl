package skills_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

func reprocessRoutes(state string) map[string]fwdtest.Handler {
	sn := fwdtest.Snap("s1", state, "COLLECTION", "2026-09-01T00:00:00.000Z")
	return map[string]fwdtest.Handler{
		snapsPath: fwdtest.Snapshots(sn),
		"POST /api/snapshots/s1": func(r *http.Request, _ []byte) (int, any) {
			return 200, map[string]any{"previousState": state, "state": "PROCESSING"}
		},
	}
}

func TestReprocessDryRunSendsNoWrite(t *testing.T) {
	r, srv := mustRun(t, "edit-snapshot-reprocess", reprocessRoutes("FAILED"), `{"network_id":"n1","snapshot_id":"s1"}`)
	if r.Status != result.OK || r.Mode != result.ModeDryRun || len(r.Changes) != 1 || r.Changes[0].Applied || r.Changes[0].Reversible {
		t.Fatalf("%s %s %+v", r.Status, r.Mode, r.Changes)
	}
	if writes(srv) != 0 {
		t.Fatalf("a dry run must not write: %+v", srv.Calls())
	}
}

func TestReprocessApplyStartsItOnceAndReportsTheState(t *testing.T) {
	r, srv := mustRun(t, "edit-snapshot-reprocess", reprocessRoutes("PROCESSED"), `{"network_id":"n1","snapshot_id":"s1","apply":true}`)
	if r.Status != result.OK || r.Mode != result.ModeApplied || !r.Changes[0].Applied || r.Changes[0].After != "PROCESSING" || writes(srv) != 1 {
		t.Fatalf("%s %s %+v writes=%d", r.Status, r.Mode, r.Changes, writes(srv))
	}
	var sent bool
	for _, c := range srv.Calls() {
		if c.Method == "POST" && c.Query["action"] == "invalidate" && c.Query["reprocess"] == "true" {
			sent = true
		}
	}
	if !sent {
		t.Errorf("the write must be an invalidate with reprocess=true, never a bare invalidate: %+v", srv.Calls())
	}
}

func TestReprocessRefusesASnapshotThatIsProcessingAndAMissingOne(t *testing.T) {
	r, srv := mustRun(t, "edit-snapshot-reprocess", reprocessRoutes("PROCESSING"), `{"network_id":"n1","snapshot_id":"s1","apply":true}`)
	if r.Status != result.Failed || writes(srv) != 0 || !strings.Contains(r.Finding, "not PROCESSED or FAILED") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	r, srv = mustRun(t, "edit-snapshot-reprocess", reprocessRoutes("FAILED"), `{"network_id":"n1","snapshot_id":"nope","apply":true}`)
	if r.Status != result.Unknown || writes(srv) != 0 {
		t.Fatalf("%s", r.Status)
	}
}
