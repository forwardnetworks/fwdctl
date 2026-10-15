package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
)

// `fwdctl nqe explain` prints Forward's own optimized plan for a saved query (the Query Debug Panel's text: parallel_foreach, totality and cardinality marks); `nqe metrics` lists the slow query runs Forward
// recorded. Both are Forward's debug features, so they need its permissions and say so when missing.
func TestNqeExplainPrintsTheOptimizedPlanOfASavedQuery(t *testing.T) {
	var sawParam bool
	r := map[string]fwdtest.Handler{
		"POST /api/internal/nqe/queries/Q_abc/explain": func(_ *http.Request, b []byte) (int, any) {
			sawParam = strings.Contains(string(b), `"threshold"`)
			return 200, []byte("parallel_foreach device in network.devices\n select {}")
		},
	}
	code, out, _ := call(t, []string{"nqe", "explain", "--query-id", "Q_abc", "--param", "threshold=3"}, "", r)
	if code != 0 || !strings.Contains(out, "parallel_foreach") || !sawParam {
		t.Fatalf("code %d out %q param %v", code, out, sawParam)
	}
	if code, _, errs := call(t, []string{"nqe", "explain"}, "", r); code != usage || !strings.Contains(errs, "--query-id") {
		t.Errorf("no query: %d %s", code, errs)
	}
}

func TestNqeMetricsListsSlowRunsAndExplainsAMissingPermission(t *testing.T) {
	r := map[string]fwdtest.Handler{
		"GET /api/nqe-run-metrics": fwdtest.Const(200, []any{map[string]any{"taskId": "1", "networkId": "n1", "networkName": "lab", "snapshotId": "s1", "executedAt": 1790000000000, "runDuration": 42000, "queryId": "Q_x", "sourceSetId": "NqeSourceSetId_1", "trackingId": "t"}}),
	}
	code, out, _ := call(t, []string{"nqe", "metrics", "--min-seconds", "5"}, "", r)
	if code != 0 || !strings.Contains(out, `"run_seconds": 42`) || !strings.Contains(out, "Q_x") {
		t.Fatalf("code %d out %s", code, out)
	}
	denied := map[string]fwdtest.Handler{"GET /api/nqe-run-metrics": fwdtest.Const(403, map[string]any{"message": "Missing permission: OrgOperation.DEBUG_NQE"})}
	if code, _, errs := call(t, []string{"nqe", "metrics"}, "", denied); code == 0 || !strings.Contains(errs, "DEBUG_NQE") {
		t.Errorf("a missing permission should be named: %d %s", code, errs)
	}
}
