package skills_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

const (
	csList   = "GET /api/networks/n1/change-sets"
	csPred   = "GET /api/networks/n1/change-sets/cs1/predicted-snapshots"
	connPath = "GET /api/diffs/b1/a1/subnet-connectivity"
)

var diffAreas = []string{"devices", "interfaces", "topology", "l2", "acl", "cloud-objects", "cloud-acl", "routing-loop/count"}

func conn(partial bool, pairs, isolated, connected int) map[string]any {
	return map[string]any{"isPartialResult": partial, "totalSubnetPairs": pairs, "evaluatedSubnetPairs": pairs,
		"newlyIsolatedSubnetPairs": isolated, "newlyConnectedSubnetPairs": connected, "modifiedSubnetPairs": 0}
}

func chk(id, name, status string, viol int) map[string]any {
	c := map[string]any{"id": id, "name": name, "status": status, "enabled": true}
	if viol > 0 {
		c["numViolations"] = viol
	}
	return c
}

type changeFix struct {
	predicted  []map[string]any
	before     []map[string]any
	after      []map[string]any
	conn       map[string]any
	paths      map[string]any
	afterSt    string
	counts     map[string]int
	incomplete map[string]bool
}

func (f changeFix) routes() map[string]fwdtest.Handler {
	afterState := f.afterSt
	if afterState == "" {
		afterState = "PROCESSED"
	}
	pred := f.predicted
	if pred == nil {
		pred = []map[string]any{{"id": "a1", "state": afterState, "processedAt": "2026-09-02T00:00:00.000Z", "processingTrigger": "PREDICT"}}
	}
	r := map[string]fwdtest.Handler{
		snapsPath: fwdtest.Snapshots(
			fwdtest.Snap("b1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z"),
			fwdtest.Snap("a1", afterState, "PREDICT", "2026-09-02T00:00:00.000Z")),
		csList:                         fwdtest.Const(200, map[string]any{"changeSets": []map[string]any{{"id": "cs1", "name": "cs", "networkId": "n1", "snapshotId": "b1"}}}),
		csPred:                         fwdtest.Const(200, map[string]any{"predictedSnapshots": pred}),
		connPath:                       fwdtest.Const(200, orConn(f.conn)),
		"GET /api/snapshots/b1/checks": fwdtest.Const(200, map[string]any{"checks": orEmpty(f.before)}),
		"GET /api/snapshots/a1/checks": fwdtest.Const(200, map[string]any{"checks": orEmpty(f.after)}),
		pathsPath:                      fwdtest.Const(200, orPaths(f.paths)),
	}
	for _, a := range diffAreas {
		r["GET /api/diffs/b1/a1/"+a] = fwdtest.Const(200, map[string]any{"count": f.counts[a], "complete": !f.incomplete[a]})
	}
	return r
}

func orConn(m map[string]any) map[string]any {
	if m == nil {
		return conn(false, 10, 0, 0)
	}
	return m
}
func orEmpty(m []map[string]any) []map[string]any {
	if m == nil {
		return []map[string]any{}
	}
	return m
}
func orPaths(m map[string]any) map[string]any {
	if m == nil {
		return pathsBody(false, "EXACT", path("DELIVERED", "PERMITTED"))
	}
	return m
}

const csInput = `{"network_id":"n1","change_set_id":"cs1","connectivity_timeout_seconds":1`

func flow(expect string) string {
	return `,"expectations":[{"src_ip":"10.0.0.1","dst_ip":"10.0.1.1","protocol":"tcp","dst_port":"443","expect":"` + expect + `"}]`
}

func verify(t *testing.T, f changeFix, extra string) (result.Result, *fwdtest.Server) {
	return mustRun(t, "verify-change", f.routes(), csInput+extra+"}")
}

func TestVerifyChangeExpectationMetAndNoRegressionIsOK(t *testing.T) {
	r, _ := verify(t, changeFix{}, flow("delivered"))
	if r.Status != result.OK || r.Confidence != result.Deterministic || r.Context.State != "predicted" {
		t.Fatalf("got %s/%s state %s: %s", r.Status, r.Confidence, r.Context.State, r.Finding)
	}
}

func TestVerifyChangeUnmetExpectationFails(t *testing.T) {
	r, _ := verify(t, changeFix{paths: pathsBody(false, "EXACT", path("DROPPED", "PERMITTED"))}, flow("delivered"))
	if r.Status != result.Failed || !strings.Contains(r.Finding, "expected delivered, observed blocked") || r.NextActions[0] != "investigate-reachability" {
		t.Fatalf("got %s: %s %v", r.Status, r.Finding, r.NextActions)
	}
}

func TestVerifyChangeBlockedExpectationHoldsWhenBlocked(t *testing.T) {
	r, _ := verify(t, changeFix{paths: pathsBody(false, "EXACT", path("DROPPED", "PERMITTED"))}, flow("blocked"))
	if r.Status != result.OK {
		t.Fatalf("status %s", r.Status)
	}
}

func TestVerifyChangeARegressedCheckFailsEvenWithoutExpectations(t *testing.T) {
	r, _ := verify(t, changeFix{before: []map[string]any{chk("c1", "no-telnet", "PASS", 0)}, after: []map[string]any{chk("c1", "no-telnet", "FAIL", 3)}}, "")
	if r.Status != result.Failed || r.Evidence[len(r.Evidence)-1].Type != result.EvPolicy {
		t.Fatalf("got %s: %s", r.Status, r.Finding)
	}
}

func TestVerifyChangeAnAlreadyFailingCheckIsNotARegression(t *testing.T) {
	r, _ := verify(t, changeFix{before: []map[string]any{chk("c1", "x", "FAIL", 3)}, after: []map[string]any{chk("c1", "x", "FAIL", 3)}}, flow("delivered"))
	if r.Status != result.OK {
		t.Fatalf("status %s: %s", r.Status, r.Finding)
	}
}

func TestVerifyChangeMoreViolationsOnAFailingCheckIsARegression(t *testing.T) {
	r, _ := verify(t, changeFix{before: []map[string]any{chk("c1", "x", "FAIL", 1)}, after: []map[string]any{chk("c1", "x", "FAIL", 4)}}, "")
	if r.Status != result.Failed {
		t.Fatalf("status %s", r.Status)
	}
}

func TestVerifyChangeNoExpectationsReportsImpactButDoesNotJudge(t *testing.T) {
	r, _ := verify(t, changeFix{conn: conn(false, 10, 2, 0)}, "")
	if r.Status != result.Unknown || !strings.Contains(strings.Join(r.Limits, " "), "not judged") ||
		!strings.Contains(r.Evidence[0].Summary, "2 newly isolated") {
		t.Fatalf("got %s %v %v", r.Status, r.Limits, r.NextActions)
	}
}

func TestVerifyChangeAnUndeterminedExpectationIsUnknownNotOK(t *testing.T) {
	r, _ := verify(t, changeFix{paths: pathsBody(false, "EXACT")}, flow("blocked"))
	if r.Status != result.Unknown {
		t.Fatalf("status %s", r.Status)
	}
}

func TestVerifyChangePartialConnectivityIsNotEvidence(t *testing.T) {
	r, _ := verify(t, changeFix{conn: conn(true, 0, 0, 0)}, "")
	if !strings.Contains(strings.Join(r.Limits, " "), "did not settle") {
		t.Errorf("limits %v", r.Limits)
	}
	for _, e := range r.Evidence {
		if e.Type == result.EvPredict {
			t.Error("unsettled connectivity was cited as evidence")
		}
	}
}

func TestVerifyChangeZeroPairsComparedIsUnmeasuredNotNoChange(t *testing.T) {
	r, _ := verify(t, changeFix{conn: conn(false, 0, 0, 0)}, "")
	if !strings.Contains(strings.Join(r.Limits, " "), "no subnet pairs") {
		t.Errorf("limits %v", r.Limits)
	}
	for _, e := range r.Evidence {
		if e.Type == result.EvPredict {
			t.Error("zero pairs was cited as evidence")
		}
	}
}

func TestVerifyChangeNoPredictionIsUnknownAndPredictIsNotRun(t *testing.T) {
	r, srv := verify(t, changeFix{predicted: []map[string]any{}}, "")
	if r.Status != result.Unknown || !strings.Contains(strings.Join(r.Limits, " "), "run_predict") {
		t.Fatalf("got %s %v", r.Status, r.Limits)
	}
	for _, c := range srv.Calls() {
		if c.Method == "POST" {
			t.Errorf("Predict must be opt-in, but %s %s was sent", c.Method, c.Path)
		}
	}
}

func TestVerifyChangeTwoSnapshotMode(t *testing.T) {
	r, _ := mustRun(t, "verify-change", changeFix{}.routes(),
		`{"network_id":"n1","before_snapshot_id":"b1","after_snapshot_id":"a1","connectivity_timeout_seconds":1`+flow("delivered")+`}`)
	if r.Status != result.OK {
		t.Fatalf("status %s", r.Status)
	}
}

func TestVerifyChangeUnprocessedAfterSnapshotIsUnknownAndNothingIsSearched(t *testing.T) {
	r, srv := mustRun(t, "verify-change", changeFix{afterSt: "UNPROCESSED"}.routes(),
		`{"network_id":"n1","before_snapshot_id":"b1","after_snapshot_id":"a1"}`)
	if r.Status != result.Unknown || srv.Called("GET", "/api/networks/n1/paths") {
		t.Fatalf("status %s", r.Status)
	}
}

func TestVerifyChangeMissingSelectorIsAnErrorResult(t *testing.T) {
	r, _ := mustRun(t, "verify-change", map[string]fwdtest.Handler{}, `{"network_id":"n1"}`)
	if r.Status != result.Error {
		t.Fatalf("status %s", r.Status)
	}
}

func TestVerifyChangeAConnectivityFeatureSwitchedOffIsALimitNotAnError(t *testing.T) {
	f := changeFix{}
	routes := f.routes()
	routes[connPath] = fwdtest.Const(403, map[string]any{"message": "LOCATION_CONNECTIVITY_DIFFS is off for your organization"})
	r, _ := mustRun(t, "verify-change", routes, csInput+flow("delivered")+"}")
	if r.Status != result.OK || !strings.Contains(strings.Join(r.Limits, " "), "LOCATION_CONNECTIVITY_DIFFS is off") {
		t.Fatalf("got %s %v", r.Status, r.Limits)
	}
	for _, e := range r.Evidence {
		if e.Type == result.EvPredict {
			t.Error("a disabled comparison was cited as evidence")
		}
	}
}

func TestVerifyChangeAnUnrelated403OnConnectivityIsNotSwallowedAsAFeatureGate(t *testing.T) {
	routes := changeFix{}.routes()
	routes[connPath] = fwdtest.Const(403, map[string]any{"message": "you may not read this snapshot"})
	_, _, err := runSkill(t, "verify-change", routes, csInput+flow("delivered")+"}")
	if err == nil {
		t.Fatal("a real permission error was reported as an unmeasured limit")
	}
}

func TestVerifyChangeManyRegressionsAreAllCountedButOnlyTheWorstAreCited(t *testing.T) {
	var before, after []map[string]any
	for i := 1; i <= 40; i++ {
		id, name := fmt.Sprint(i), fmt.Sprintf("check-%02d", i)
		before = append(before, chk(id, name, "PASS", 0))
		after = append(after, chk(id, name, "FAIL", i))
	}
	r, _ := verify(t, changeFix{before: before, after: after}, "")
	if r.Status != result.Failed || !strings.HasSuffix(r.Finding, "and 35 more") {
		t.Fatalf("got %s: %s", r.Status, r.Finding)
	}
	cited := 0
	for _, e := range r.Evidence {
		if e.Type == result.EvPolicy {
			cited++
		}
	}
	if cited != 10 || !strings.Contains(strings.Join(r.Limits, " "), "40 checks regressed") {
		t.Errorf("cited %d, limits %v", cited, r.Limits)
	}
}
