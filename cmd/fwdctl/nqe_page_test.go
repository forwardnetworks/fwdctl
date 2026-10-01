package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
)

// pagedRoutes serves an ordered result of five rows, sync (the request's options) and async (the result's limit and offset).
func pagedRoutes() map[string]fwdtest.Handler {
	all := []any{}
	for i := 0; i < 5; i++ {
		all = append(all, map[string]any{"n": i})
	}
	slice := func(off, lim int) any {
		end := min(off+lim, len(all))
		if off > len(all) {
			off = len(all)
		}
		return all[off:end]
	}
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/snapshots": fwdtest.Snapshots(fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")),
		"POST /api/nqe": func(_ *http.Request, body []byte) (int, any) {
			var q struct {
				Options struct{ Limit, Offset int } `json:"queryOptions"`
			}
			_ = json.Unmarshal(body, &q)
			return 200, map[string]any{"items": slice(q.Options.Offset, q.Options.Limit), "totalNumItems": len(all)}
		},
		"POST /api/networks/n1/nqe-executions":    fwdtest.Const(200, map[string]any{"executionKey": "X_1", "status": "RUNNING"}),
		"GET /api/networks/n1/nqe-executions/X_1": fwdtest.Const(200, map[string]any{"status": "COMPLETED", "outcome": "OK"}),
		"GET /api/networks/n1/nqe-executions/X_1/result": func(r *http.Request, _ []byte) (int, any) {
			off, _ := strconv.Atoi(r.URL.Query().Get("offset"))
			lim, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			return 200, map[string]any{"items": slice(off, lim), "totalNumItems": len(all)}
		},
	}
}

func TestNQERunPagesJoinIntoTheFullResultSyncAndAsync(t *testing.T) {
	for _, async := range []bool{false, true} {
		var joined []string
		for off := 0; off < 5; off += 2 {
			meta := filepath.Join(t.TempDir(), "m.json")
			args := []string{"nqe", "run", "--network", "n1", "--snapshot", "s1", "--format", "jsonl", "--offset", strconv.Itoa(off), "--limit", "2", "--meta", meta}
			if async {
				args = append(args, "--async")
			}
			code, out, errb := call(t, args, "foreach d in network.devices select {n: d.name}", pagedRoutes())
			if code != 0 {
				t.Fatalf("async=%v offset %d: exit %d %s", async, off, code, errb)
			}
			for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
				joined = append(joined, l)
			}
			b, _ := os.ReadFile(meta)
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			if m["offset"] != float64(off) || m["limit"] != float64(2) || m["total"] != float64(5) {
				t.Errorf("async=%v meta %v", async, m)
			}
		}
		if strings.Join(joined, "") != `{"n":0}{"n":1}{"n":2}{"n":3}{"n":4}` {
			t.Errorf("async=%v the pages do not join into the full ordered result: %v", async, joined)
		}
	}
	if code, _, _ := call(t, []string{"nqe", "run", "--network", "n1", "--offset", "2"}, "x", pagedRoutes()); code != usage {
		t.Errorf("--offset without --limit is a usage error, got %d", code)
	}
}
