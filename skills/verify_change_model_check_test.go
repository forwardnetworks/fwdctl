package skills_test

import (
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

const modelCheckPath = "POST /api/internal/snapshots/s1/verify-predict-modeling"

func modelCheckRoutes(h fwdtest.Handler) map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/snapshots": fwdtest.Snapshots(fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")),
		modelCheckPath:                   h,
	}
}

// The view returns Forward's score as sent, overall and per protocol, and quotes precision, recall and f1 in the finding.
func TestModelCheckReturnsTheScoresForABaseSnapshot(t *testing.T) {
	r, srv := mustRun(t, "verify-change", modelCheckRoutes(fwdtest.Const(200, map[string]any{
		"score":            map[string]any{"precision": 97.0, "recall": 91.0, "f1": 94.0},
		"scoresByProtocol": map[string]any{"BGP": map[string]any{"precision": 100.0, "recall": 80.0, "f1": 89.0}},
	})), `{"network_id":"n1","view":"model_check"}`)
	b := jsonOf(r)
	if r.Status != result.OK || !strings.Contains(r.Finding, "precision 97%") || !strings.Contains(r.Finding, "recall 91%") || !strings.Contains(b, `"BGP"`) || len(calls(srv, "POST")) != 1 || len(calls(srv, "PUT"))+len(calls(srv, "PATCH"))+len(calls(srv, "DELETE")) != 0 {
		t.Fatalf("%s %s\n%s", r.Status, r.Finding, b)
	}
	if !strings.Contains(strings.Join(r.Limits, "|"), "experimental") || !strings.Contains(strings.Join(r.Limits, "|"), "MANAGE_PROTOCOL_MODELING") {
		t.Errorf("limits: %v", r.Limits)
	}
}

// A zero score in every field is no basis (what a blank network also scores): unknown, never ok. An empty score and a refused login are unknown too.
func TestModelCheckZeroEmptyAndRefusedScoresAreUnknown(t *testing.T) {
	zero := fwdtest.Const(200, map[string]any{"score": map[string]any{"precision": 0, "recall": 0, "f1": 0}})
	empty := fwdtest.Const(200, map[string]any{})
	denied := fwdtest.Const(403, map[string]any{"message": "No permission for network operation MANAGE_PROTOCOL_MODELING"})
	for name, h := range map[string]fwdtest.Handler{"zero": zero, "empty": empty, "denied": denied} {
		r, _ := mustRun(t, "verify-change", modelCheckRoutes(h), `{"network_id":"n1","view":"model_check"}`)
		if r.Status != result.Unknown {
			t.Errorf("%s: status %s (%s), want unknown", name, r.Status, r.Finding)
		}
	}
}

// An input of another view is rejected (a change set belongs to other views, not to model_check).
func TestModelCheckRefusesForeignInputs(t *testing.T) {
	for _, in := range []string{`{"network_id":"n1","view":"model_check","change_set_id":"cs1"}`, `{"network_id":"n1","view":"model_check","device":"r1"}`} {
		if _, _, err := runSkill(t, "verify-change", modelCheckRoutes(fwdtest.Const(200, map[string]any{})), in); err == nil {
			t.Errorf("must refuse %s", in)
		}
	}
}

// Unknown is never a pass: a score with a null or non-numeric field (Forward's JSON writer sends "NaN" for a 0/0 double) or one that is not an object
// has no measurement, and an unworded refusal is unknown too.
func TestModelCheckUnusableScoresAndRefusalsAreUnknown(t *testing.T) {
	for name, h := range map[string]fwdtest.Handler{
		"null fields":   fwdtest.Const(200, map[string]any{"score": map[string]any{"precision": nil, "recall": nil, "f1": nil}}),
		"NaN strings":   fwdtest.Const(200, map[string]any{"score": map[string]any{"precision": "NaN", "recall": 50.0, "f1": "NaN"}}),
		"not an object": fwdtest.Const(200, map[string]any{"score": 12.5}),
		"missing f1":    fwdtest.Const(200, map[string]any{"score": map[string]any{"precision": 90.0, "recall": 80.0}}),
		"bare 403":      fwdtest.Const(403, map[string]any{"message": "forbidden"}),
	} {
		r, _ := mustRun(t, "verify-change", modelCheckRoutes(h), `{"network_id":"n1","view":"model_check"}`)
		if r.Status != result.Unknown {
			t.Errorf("%s: status %s (%s), want unknown", name, r.Status, r.Finding)
		}
	}
}

// A score that is zero in only some fields is a measurement (precision 100 with a recall near zero is a real reading), not the no-basis case.
func TestModelCheckPartialZeroIsAResult(t *testing.T) {
	r, _ := mustRun(t, "verify-change", modelCheckRoutes(fwdtest.Const(200, map[string]any{"score": map[string]any{"precision": 100.0, "recall": 0.0, "f1": 0.0}})), `{"network_id":"n1","view":"model_check"}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "precision 100%") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
}

// model_check scores a processed COLLECTED snapshot: a prediction and an unprocessed snapshot are unknown (nothing is posted), and an explicit
// after_snapshot_id is the snapshot that is scored.
func TestModelCheckScoresTheNamedCollectedSnapshotOnly(t *testing.T) {
	p1 := fwdtest.Snap("p1", "PROCESSED", "PREDICT", "2026-09-02T00:00:00.000Z")
	p1["parentSnapshotId"] = "s1"
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/snapshots": fwdtest.Snapshots(fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z"),
			fwdtest.Snap("s2", "PROCESSED", "COLLECTION", "2026-09-03T00:00:00.000Z"), fwdtest.Snap("u1", "UNPROCESSED", "COLLECTION", "2026-09-04T00:00:00.000Z"), p1),
		"POST /api/internal/snapshots/s1/verify-predict-modeling": fwdtest.Const(200, map[string]any{"score": map[string]any{"precision": 11.0, "recall": 22.0, "f1": 33.0}}),
		"POST /api/internal/snapshots/s2/verify-predict-modeling": fwdtest.Const(200, map[string]any{"score": map[string]any{"precision": 44.0, "recall": 55.0, "f1": 66.0}}),
	}
	r, _ := mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"model_check","after_snapshot_id":"s1"}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "snapshot s1") || !strings.Contains(r.Finding, "precision 11%") {
		t.Fatalf("the named snapshot is the one scored: %s %s", r.Status, r.Finding)
	}
	for _, id := range []string{"p1", "u1"} {
		r, srv := mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"model_check","after_snapshot_id":"`+id+`"}`)
		if r.Status != result.Unknown || len(calls(srv, "POST")) != 0 {
			t.Errorf("%s: status %s, posts %d, want unknown and nothing posted", id, r.Status, len(calls(srv, "POST")))
		}
	}
}
