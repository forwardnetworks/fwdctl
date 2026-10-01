package skills_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

const notePath = "PATCH /api/snapshots/s1"

func noteRoutes(prior string, echo func(sent string) string) map[string]fwdtest.Handler {
	sn := fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")
	sn["note"] = prior
	return map[string]fwdtest.Handler{
		snapsPath: fwdtest.Snapshots(sn),
		notePath: func(r *http.Request, body []byte) (int, any) {
			s := strings.TrimSuffix(strings.TrimPrefix(string(body), `{"note":"`), `"}`)
			out := fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")
			out["note"] = echo(s)
			return 200, out
		},
	}
}

func writes(srv *fwdtest.Server) int {
	n := 0
	for _, c := range srv.Calls() {
		// an NQE run is a POST that reads
		if c.Method != "GET" && c.Path != "/api/nqe" {
			n++
		}
	}
	return n
}

func TestAnnotateDryRunShowsBeforeAndAfterAndSendsNoWrite(t *testing.T) {
	r, srv := mustRun(t, "edit-snapshot-note", noteRoutes("old", func(s string) string { return s }), `{"network_id":"n1","snapshot_id":"s1","note":"before change X"}`)
	if r.Status != result.OK || r.Mode != result.ModeDryRun || len(r.Changes) != 1 || r.Changes[0].Applied {
		t.Fatalf("%s %s %+v", r.Status, r.Mode, r.Changes)
	}
	if r.Changes[0].Before != "old" || r.Changes[0].After != "before change X" || !strings.Contains(r.Changes[0].Undo, `"old"`) {
		t.Errorf("%+v", r.Changes[0])
	}
	if writes(srv) != 0 {
		t.Fatalf("a dry run must not write: %+v", srv.Calls())
	}
}

func TestAnnotateApplyWritesOnceAndVerifiesWhatForwardHolds(t *testing.T) {
	r, srv := mustRun(t, "edit-snapshot-note", noteRoutes("old", func(s string) string { return s }), `{"network_id":"n1","snapshot_id":"s1","note":"new","apply":true}`)
	if r.Status != result.OK || r.Mode != result.ModeApplied || !r.Changes[0].Applied || writes(srv) != 1 {
		t.Fatalf("%s %s %+v writes=%d", r.Status, r.Mode, r.Changes, writes(srv))
	}
	r, _ = mustRun(t, "edit-snapshot-note", noteRoutes("old", func(string) string { return "something else" }), `{"network_id":"n1","snapshot_id":"s1","note":"new","apply":true}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "not the requested note") {
		t.Fatalf("a note Forward did not keep must be failed: %s %s", r.Status, r.Finding)
	}
}

func TestAnnotateChangesNothingWhenTheNoteMatchesOrTheSnapshotIsMissing(t *testing.T) {
	r, srv := mustRun(t, "edit-snapshot-note", noteRoutes("same", func(s string) string { return s }), `{"network_id":"n1","snapshot_id":"s1","note":"same","apply":true}`)
	if r.Status != result.OK || len(r.Changes) != 0 || writes(srv) != 0 {
		t.Fatalf("%s %+v", r.Status, r.Changes)
	}
	r, srv = mustRun(t, "edit-snapshot-note", noteRoutes("x", func(s string) string { return s }), `{"network_id":"n1","snapshot_id":"nope","note":"n","apply":true}`)
	if r.Status != result.Unknown || writes(srv) != 0 {
		t.Fatalf("%s", r.Status)
	}
}

func TestAnnotateRequiresANoteAndBoundsItsLength(t *testing.T) {
	for _, in := range []string{`{"network_id":"n1","snapshot_id":"s1"}`, `{"network_id":"n1","snapshot_id":"s1","note":"` + strings.Repeat("x", 1001) + `"}`} {
		_, _, err := runSkill(t, "edit-snapshot-note", noteRoutes("", func(s string) string { return s }), in)
		if err == nil {
			t.Errorf("must refuse: %.60s", in)
		}
	}
}
