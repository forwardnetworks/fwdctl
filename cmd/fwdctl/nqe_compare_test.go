package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
)

// `fwdctl nqe compare` runs two versions of a query on ONE snapshot (nqe diff runs one query on two) and says whether their rows are the same: counts of added, removed and changed rows by key, the column and
// type shape of each, whether they differ only in array order, and the timing of each run (cold, interleaved), so it can gate a release. Exit 0 identical, 1 differs.
func compareRoutes(rowsA, rowsB []any) map[string]fwdtest.Handler {
	keyOf := map[string]string{}
	n := 0
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/snapshots": fwdtest.Snapshots(fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")),
		"POST /api/networks/n1/nqe-executions": func(_ *http.Request, b []byte) (int, any) {
			n++
			k := "X_" + string(rune('0'+n))
			if strings.Contains(string(b), "version b") {
				keyOf[k] = "b"
			} else {
				keyOf[k] = "a"
			}
			return 200, map[string]any{"executionKey": k, "status": "RUNNING"}
		},
	}
	for i := 1; i <= 8; i++ {
		k := "X_" + string(rune('0'+i))
		routes["GET /api/networks/n1/nqe-executions/"+k] = fwdtest.Const(200, map[string]any{"status": "COMPLETED", "outcome": "OK", "millisExecuting": 120})
		routes["GET /api/networks/n1/nqe-executions/"+k+"/result"] = func(*http.Request, []byte) (int, any) {
			rows := rowsA
			if keyOf[k] == "b" {
				rows = rowsB
			}
			return 200, map[string]any{"items": rows, "totalNumItems": len(rows)}
		}
	}
	return routes
}

func writeQ(t *testing.T, name, body string) string {
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const qa = "// version a\nforeach d in network.devices select {dev: d.name}"
const qb = "// version b\nforeach d in network.devices select {dev: d.name}"

func TestNqeCompareReportsRowAndShapeDifferencesAndTheTimingOfEachRun(t *testing.T) {
	a := []any{map[string]any{"dev": "r1", "ips": []any{"1", "2"}}, map[string]any{"dev": "r2", "ips": []any{"3"}}}
	b := []any{map[string]any{"dev": "r1", "ips": []any{"1", "2"}, "extra": 1}, map[string]any{"dev": "r3", "ips": []any{"3"}}}
	args := []string{"nqe", "compare", "--network", "n1", "--snapshot", "s1", "--a", writeQ(t, "a.nqe", qa), "--b", writeQ(t, "b.nqe", qb), "--key", "dev"}
	code, out, errs := call(t, args, "", compareRoutes(a, b))
	if code != 1 {
		t.Fatalf("differing versions exit 1, got %d: %s %s", code, out, errs)
	}
	var sum struct {
		Identical bool           `json:"identical"`
		Counts    map[string]int `json:"counts"`
		Shape     struct {
			OnlyInB []string `json:"columns_only_in_b"`
		} `json:"shape"`
		Runs []struct {
			Side            string `json:"side"`
			ExecutionKey    string `json:"execution_key"`
			MillisExecuting *int64 `json:"millis_executing"`
		} `json:"runs"`
	}
	if err := json.Unmarshal([]byte(out), &sum); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if sum.Identical || sum.Counts["added"] != 1 || sum.Counts["removed"] != 1 || sum.Counts["changed"] != 1 {
		t.Errorf("counts %v identical %v", sum.Counts, sum.Identical)
	}
	if len(sum.Shape.OnlyInB) != 1 || sum.Shape.OnlyInB[0] != "extra" {
		t.Errorf("shape: %+v", sum.Shape)
	}
	if len(sum.Runs) != 2 || sum.Runs[0].Side != "a" || sum.Runs[1].Side != "b" || sum.Runs[0].ExecutionKey == "" || sum.Runs[1].MillisExecuting == nil {
		t.Errorf("runs: %+v", sum.Runs)
	}
}

func TestNqeCompareCanIgnoreArrayOrderAndGatesOnTheExitCode(t *testing.T) {
	a := []any{map[string]any{"dev": "r1", "ips": []any{"1", "2"}}}
	b := []any{map[string]any{"dev": "r1", "ips": []any{"2", "1"}}}
	args := []string{"nqe", "compare", "--network", "n1", "--snapshot", "s1", "--a", writeQ(t, "a.nqe", qa), "--b", writeQ(t, "b.nqe", qb), "--key", "dev"}
	code, out, _ := call(t, args, "", compareRoutes(a, b))
	if code != 1 || !strings.Contains(out, `"value_differences_ignoring_array_order": 0`) {
		t.Fatalf("an array in another order is a difference unless asked: %d %s", code, out)
	}
	if code, out, _ := call(t, append(args, "--ignore-array-order"), "", compareRoutes(a, b)); code != 0 || !strings.Contains(out, `"identical": false`) {
		t.Errorf("--ignore-array-order: only the order differs, exit 0 (byte-identical stays false): %d %s", code, out)
	}
	same := []any{map[string]any{"dev": "r1"}}
	if code, out, _ := call(t, args, "", compareRoutes(same, same)); code != 0 || !strings.Contains(out, `"identical": true`) {
		t.Errorf("identical rows exit 0: %d %s", code, out)
	}
}

func TestNqeCompareRefusesTwoVersionsThatAreTheSameText(t *testing.T) {
	f := writeQ(t, "a.nqe", qa)
	code, _, errs := call(t, []string{"nqe", "compare", "--network", "n1", "--snapshot", "s1", "--a", f, "--b", f}, "", compareRoutes(nil, nil))
	if code != usage || !strings.Contains(errs, "same") {
		t.Errorf("nothing to compare: %d %s", code, errs)
	}
}

func TestNqeCompareWithoutAKeyComparesTheRowsAsASet(t *testing.T) {
	a := []any{map[string]any{"dev": "r1"}, map[string]any{"dev": "r2"}}
	b := []any{map[string]any{"dev": "r2"}, map[string]any{"dev": "r1"}, map[string]any{"dev": "r3"}}
	args := []string{"nqe", "compare", "--network", "n1", "--snapshot", "s1", "--a", writeQ(t, "a.nqe", qa), "--b", writeQ(t, "b.nqe", qb)}
	code, out, _ := call(t, args, "", compareRoutes(a, b))
	if code != 1 || !strings.Contains(out, `"added": 1`) || !strings.Contains(out, `"removed": 0`) {
		t.Errorf("one row added, row order ignored: %d %s", code, out)
	}
	if code, _, _ := call(t, args, "", compareRoutes(a, []any{map[string]any{"dev": "r2"}, map[string]any{"dev": "r1"}})); code != 0 {
		t.Errorf("the same rows in another order are identical: %d", code)
	}
}

// --meta records how many bytes of rows the run wrote, so a job can size a result without measuring the output itself.
func TestNqeRunMetaRecordsTheResponseBytes(t *testing.T) {
	meta := filepath.Join(t.TempDir(), "m.json")
	code, out, errb := call(t, []string{"nqe", "run", "--network", "n1", "--snapshot", "s1", "--format", "jsonl", "--limit", "2", "--meta", meta}, "foreach d in network.devices select {n: d.name}", pagedRoutes())
	if code != 0 {
		t.Fatalf("%d %s", code, errb)
	}
	b, _ := os.ReadFile(meta)
	var m struct {
		ResponseBytes int64 `json:"response_bytes"`
	}
	if err := json.Unmarshal(b, &m); err != nil || m.ResponseBytes != int64(len(out)) || m.ResponseBytes == 0 {
		t.Errorf("response_bytes %d, stdout %d bytes: %s", m.ResponseBytes, len(out), b)
	}
}
