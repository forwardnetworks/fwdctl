package skills_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

func scRoutes(tasks []any, attach map[string]any) map[string]fwdtest.Handler {
	r := map[string]fwdtest.Handler{
		tasksPath:                              func(req *http.Request, _ []byte) (int, any) { return 200, tasks },
		"GET /api/networks/n1/classic-devices": fwdtest.Const(200, []any{map[string]any{"name": "r1", "host": "10.0.0.1"}}),
		"GET /api/networks/n1/endpoints":       fwdtest.Const(200, []any{}),
		"POST /api/collector-tasks":            fwdtest.Const(200, map[string]any{"taskId": "t9"}),
		"GET /api/collector-tasks/t1":          fwdtest.Const(200, map[string]any{"id": "t1", "status": "RUNNING", "networkId": "n1"}),
		"GET /api/collector-tasks/t0":          fwdtest.Const(200, map[string]any{"id": "t0", "status": "SUCCEEDED", "networkId": "n1"}),
		"POST /api/collector-tasks/t1":         fwdtest.Const(200, map[string]any{}),
	}
	if attach != nil {
		r[attachPath] = fwdtest.Const(200, attach)
	}
	return r
}

func idle() []any { return []any{task("8", "SUCCEEDED", "2026-09-29T10:05:00Z")} }

func TestStartCollectionDryRunStartsNothing(t *testing.T) {
	r, srv := mustRun(t, "edit-collection", scRoutes(idle(), map[string]any{"name": "c", "connectionStatus": "CONNECTED"}), `{"network_id":"n1"}`)
	if r.Status != result.OK || r.Mode != result.ModeDryRun || len(r.Changes) != 1 || writes(srv) != 0 {
		t.Fatalf("%s %s %+v writes=%d", r.Status, r.Mode, r.Changes, writes(srv))
	}
}

func TestStartCollectionAppliesOnceAndReturnsTheTaskAndTheUndo(t *testing.T) {
	r, srv := mustRun(t, "edit-collection", scRoutes(idle(), nil), `{"network_id":"n1","apply":true}`)
	if r.Status != result.OK || r.Mode != result.ModeApplied || writes(srv) != 1 || !strings.Contains(r.Changes[0].Undo, "task_id=t9") {
		t.Fatalf("%s %+v writes=%d", r.Status, r.Changes, writes(srv))
	}
}

func TestStartCollectionRefusesWhileOneRunsOrTheCollectorIsDown(t *testing.T) {
	running := []any{map[string]any{"id": "t1", "status": "RUNNING", "type": "NETWORK_COLLECTION", "networkId": "n1"}}
	r, srv := mustRun(t, "edit-collection", scRoutes(running, nil), `{"network_id":"n1","apply":true}`)
	if r.Status != result.Unknown || writes(srv) != 0 || !strings.Contains(r.Finding, "already running") {
		t.Fatalf("%s %s writes=%d", r.Status, r.Finding, writes(srv))
	}
	r, srv = mustRun(t, "edit-collection", scRoutes(idle(), map[string]any{"name": "dc", "connectionStatus": "DISCONNECTED"}), `{"network_id":"n1","apply":true}`)
	if r.Status != result.Unknown || writes(srv) != 0 || !strings.Contains(r.Finding, "DISCONNECTED") {
		t.Fatalf("%s %s writes=%d", r.Status, r.Finding, writes(srv))
	}
}

func TestStopCollectionOnlyEndsARunningTaskOfThisNetwork(t *testing.T) {
	r, srv := mustRun(t, "edit-collection", scRoutes(idle(), nil), `{"network_id":"n1","action":"stop","task_id":"t1"}`)
	if r.Status != result.OK || r.Mode != result.ModeDryRun || writes(srv) != 0 {
		t.Fatalf("dry run: %s %s", r.Status, r.Mode)
	}
	r, srv = mustRun(t, "edit-collection", scRoutes(idle(), nil), `{"network_id":"n1","action":"stop","task_id":"t1","apply":true,"stop_mode":"SKIP"}`)
	var q string
	for _, c := range srv.Calls() {
		if c.Method == "POST" {
			q = c.Query["action"]
		}
	}
	if r.Status != result.OK || q != "SKIP" || r.Changes[0].Reversible {
		t.Fatalf("%s action=%q %+v", r.Status, q, r.Changes)
	}
	r, srv = mustRun(t, "edit-collection", scRoutes(idle(), nil), `{"network_id":"n1","action":"stop","task_id":"t0","apply":true}`)
	if r.Status != result.Unknown || writes(srv) != 0 {
		t.Fatalf("a finished task is not stopped: %s writes=%d", r.Status, writes(srv))
	}
	r, srv = mustRun(t, "edit-collection", scRoutes(idle(), nil), `{"network_id":"other","action":"stop","task_id":"t1","apply":true}`)
	if r.Status != result.Unknown || writes(srv) != 0 {
		t.Fatalf("another network's task is not touched: %s writes=%d", r.Status, writes(srv))
	}
	if _, _, err := runSkill(t, "edit-collection", scRoutes(idle(), nil), `{"network_id":"n1","action":"stop"}`); err == nil {
		t.Error("stop without task_id must be refused")
	}
}

func TestStartCollectionRefusesWhenNothingIsConfiguredToCollect(t *testing.T) {
	routes := scRoutes(idle(), nil)
	routes["GET /api/networks/n1/classic-devices"] = fwdtest.Const(200, []any{})
	r, srv := mustRun(t, "edit-collection", routes, `{"network_id":"n1","apply":true}`)
	if r.Status != result.Unknown || writes(srv) != 0 || !strings.Contains(r.Finding, "nothing is configured") {
		t.Fatalf("%s %s writes=%d", r.Status, r.Finding, writes(srv))
	}
}
