package skills_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
	"github.com/forwardnetworks/fwdctl/skills"
)

// slowNQERoutes serves a synchronous NQE that takes longer than the client's timeout and the asynchronous API that answers at once, so a skill's query only completes if
// it falls back (or is told) to run asynchronously.
func slowNQERoutes(syncCalls, started *int) map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		snapsPath: ready("s1"),
		nqePath: func(*http.Request, []byte) (int, any) {
			*syncCalls++
			time.Sleep(400 * time.Millisecond)
			return 200, map[string]any{"items": []any{}, "totalNumItems": 0}
		},
		"POST /api/networks/n1/nqe-executions": func(*http.Request, []byte) (int, any) {
			*started++
			return 200, map[string]any{"executionKey": "X_1", "status": "RUNNING"}
		},
		"GET /api/networks/n1/nqe-executions/X_1":        fwdtest.Const(200, map[string]any{"status": "COMPLETED", "outcome": "OK", "millisExecuting": 90000}),
		"GET /api/networks/n1/nqe-executions/X_1/result": fwdtest.Const(200, map[string]any{"items": []any{map[string]any{"device": "r1"}}, "totalNumItems": 1}),
	}
}

func runNQEWith(t *testing.T, routes map[string]fwdtest.Handler, mode string) ([]map[string]any, error) {
	t.Helper()
	sess, _ := fwdtest.NewWith(t, routes, func(c *fwd.Config) {
		hc := *c.HTTPClient
		hc.Timeout = 150 * time.Millisecond
		c.HTTPClient = &hc
		c.NQEMode = mode
	})
	rows, _, _, err := sess.RunNQEAll(context.Background(), "n1", "s1", "foreach d in network.devices select {device: d.name}", 10)
	return rows, err
}

func TestNQEFallsBackToTheAsynchronousAPIWhenTheSynchronousCallTimesOut(t *testing.T) {
	var sync, started int
	rows, err := runNQEWith(t, slowNQERoutes(&sync, &started), "")
	if err != nil || len(rows) != 1 || rows[0]["device"] != "r1" || sync != 1 || started != 1 {
		t.Fatalf("rows=%v err=%v sync=%d async=%d", rows, err, sync, started)
	}
}

func TestNQEModeSyncNeverFallsBackAndModeAsyncNeverTriesSync(t *testing.T) {
	var sync, started int
	if _, err := runNQEWith(t, slowNQERoutes(&sync, &started), "sync"); err == nil || started != 0 {
		t.Fatalf("sync mode must fail on the timeout and not start an execution: err=%v async=%d", err, started)
	}
	sync, started = 0, 0
	rows, err := runNQEWith(t, slowNQERoutes(&sync, &started), "async")
	if err != nil || len(rows) != 1 || sync != 0 || started != 1 {
		t.Fatalf("async mode: rows=%v err=%v sync=%d async=%d", rows, err, sync, started)
	}
}

func TestASkillsQueryCompletesThroughTheFallback(t *testing.T) {
	var sync, started int
	routes := slowNQERoutes(&sync, &started)
	routes["GET /api/networks/n1/snapshots/s1/metrics"] = fwdtest.Const(200, map[string]any{})
	sess, _ := fwdtest.NewWith(t, routes, func(c *fwd.Config) {
		hc := *c.HTTPClient
		hc.Timeout = 150 * time.Millisecond
		c.HTTPClient = &hc
	})
	// validate-nqe-query runs one query through RunNQE (a single page), the same call the other skills use
	r, err := skills.Run(context.Background(), "validate-nqe-query", sess, json.RawMessage(`{"network_id":"n1","query":"foreach d in network.devices select {device: d.name}"}`))
	if err != nil || r.Status == result.Error || started != 1 {
		t.Fatalf("status=%v err=%v async=%d sync=%d finding=%s", r.Status, err, started, sync, r.Finding)
	}
}
