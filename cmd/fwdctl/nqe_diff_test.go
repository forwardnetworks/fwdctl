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

// `fwdctl nqe diff` runs one ad-hoc query on two snapshots and reports the rows added, removed and changed, keyed by the columns named. It needs no saved query and returns every difference, not a capped
// handful; the counts are exact and the listing and the output file say what changed per row.
func diffRoutes() map[string]fwdtest.Handler {
	rows := map[string][]any{
		"s1": {map[string]any{"dev": "a", "rule": "1", "action": "permit"}, map[string]any{"dev": "a", "rule": "2", "action": "deny"}, map[string]any{"dev": "b", "rule": "1", "action": "permit"}},
		"s2": {map[string]any{"dev": "a", "rule": "1", "action": "deny"}, map[string]any{"dev": "a", "rule": "2", "action": "deny"}, map[string]any{"dev": "c", "rule": "9", "action": "permit"}},
	}
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/snapshots": fwdtest.Snapshots(fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z"), fwdtest.Snap("s2", "PROCESSED", "COLLECTION", "2026-09-02T00:00:00.000Z")),
		"POST /api/nqe": func(r *http.Request, _ []byte) (int, any) {
			it := rows[r.URL.Query().Get("snapshotId")]
			return 200, map[string]any{"items": it, "totalNumItems": len(it)}
		},
	}
}

func TestNqeDiffReportsAddedRemovedAndChangedByKey(t *testing.T) {
	out := filepath.Join(t.TempDir(), "diff.jsonl")
	code, so, errs := call(t, []string{"nqe", "diff", "--network", "n1", "--before-snapshot", "s1", "--after-snapshot", "s2", "--key", "dev,rule", "--out", out}, "select {dev: 1}", diffRoutes())
	if code != 1 {
		t.Fatalf("differences exit 1, got %d: %s %s", code, so, errs)
	}
	var sum struct {
		Counts  map[string]int `json:"counts"`
		Changed []struct {
			Key     map[string]any `json:"key"`
			Columns []string       `json:"changed_columns"`
		} `json:"changed"`
	}
	if err := json.Unmarshal([]byte(so), &sum); err != nil {
		t.Fatalf("%v: %s", err, so)
	}
	if sum.Counts["added"] != 1 || sum.Counts["removed"] != 1 || sum.Counts["changed"] != 1 || sum.Counts["unchanged"] != 1 {
		t.Errorf("counts = %v", sum.Counts)
	}
	if len(sum.Changed) != 1 || sum.Changed[0].Key["dev"] != "a" || len(sum.Changed[0].Columns) != 1 || sum.Changed[0].Columns[0] != "action" {
		t.Errorf("changed = %+v", sum.Changed)
	}
	b, _ := os.ReadFile(out)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 3 || !strings.Contains(string(b), `"change":"added"`) || !strings.Contains(string(b), `"change":"removed"`) || !strings.Contains(string(b), `"change":"changed"`) {
		t.Errorf("the output file lists every difference: %s", b)
	}
}

func TestNqeDiffWithNothingDifferentExitsZeroAndRefusesAmbiguousKeys(t *testing.T) {
	code, so, _ := call(t, []string{"nqe", "diff", "--network", "n1", "--before-snapshot", "s1", "--after-snapshot", "s1", "--key", "dev,rule"}, "select {dev: 1}", diffRoutes())
	if code != 0 || !strings.Contains(so, `"changed": 0`) && !strings.Contains(so, `"changed":0`) {
		t.Fatalf("the same snapshot twice: %d %s", code, so)
	}
	// dev alone is not unique (a has two rules)
	code, _, errs := call(t, []string{"nqe", "diff", "--network", "n1", "--before-snapshot", "s1", "--after-snapshot", "s2", "--key", "dev"}, "select {dev: 1}", diffRoutes())
	if code == 0 || !strings.Contains(errs, "not unique") {
		t.Errorf("a key that repeats must be refused: %d %s", code, errs)
	}
	code, _, errs = call(t, []string{"nqe", "diff", "--network", "n1", "--before-snapshot", "s1", "--after-snapshot", "s2", "--key", "nope"}, "select {dev: 1}", diffRoutes())
	if code == 0 || !strings.Contains(errs, "nope") {
		t.Errorf("a key column the rows lack must be named: %d %s", code, errs)
	}
}
