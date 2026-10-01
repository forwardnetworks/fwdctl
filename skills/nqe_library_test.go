package skills_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

const (
	diffPath = "POST /api/nqe-diffs/s1/s2"
	libPath  = "GET /api/nqe/queries"
)

func twoSnaps() fwdtest.Handler {
	return fwdtest.Snapshots(
		fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z"),
		fwdtest.Snap("s2", "PROCESSED", "COLLECTION", "2026-09-02T00:00:00.000Z"))
}

const cmpIn = `{"network_id":"n1","before_snapshot_id":"s1","after_snapshot_id":"s2","query_id":"Q1"}`

func TestCompareNQEReportsRowsAndCountsByType(t *testing.T) {
	r, _ := mustRun(t, "compare-nqe-results", map[string]fwdtest.Handler{snapsPath: twoSnaps(),
		diffPath: fwdtest.Const(200, map[string]any{"totalNumRows": 2, "rows": []any{
			map[string]any{"type": "ADDED", "after": map[string]any{"Device": "r1"}},
			map[string]any{"type": "MODIFIED", "before": map[string]any{"Device": "r2", "V": 1}, "after": map[string]any{"Device": "r2", "V": 2}}}})}, cmpIn)
	d := fmt.Sprint(r.Evidence[0].Detail)
	if r.Status != result.OK || !strings.Contains(r.Finding, "2 difference") || !strings.Contains(d, "ADDED") || !strings.Contains(d, "MODIFIED") {
		t.Fatalf("%s %+v", r.Finding, d)
	}
}

func TestCompareNQEZeroDifferencesOverAnEmptyQueryIsUnknown(t *testing.T) {
	routes := map[string]fwdtest.Handler{snapsPath: twoSnaps(), diffPath: fwdtest.Const(200, map[string]any{"totalNumRows": 0, "rows": []any{}}),
		nqePath: fwdtest.Const(200, map[string]any{"items": []any{}, "totalNumItems": 0})}
	r, _ := mustRun(t, "compare-nqe-results", routes, cmpIn)
	if r.Status != result.Unknown {
		t.Fatalf("empty on both sides must be unknown, got %s", r.Status)
	}
	routes[nqePath] = fwdtest.Const(200, map[string]any{"items": []any{map[string]any{"Device": "r1"}}, "totalNumItems": 1})
	r, _ = mustRun(t, "compare-nqe-results", routes, cmpIn)
	if r.Status != result.OK || !strings.Contains(r.Finding, "No differences") {
		t.Fatalf("zero differences over a non-empty query is a real answer: %s %s", r.Status, r.Finding)
	}
}

func TestCompareNQEUnprocessedSnapshotOrUnknownQueryIsUnknown(t *testing.T) {
	r, _ := mustRun(t, "compare-nqe-results", map[string]fwdtest.Handler{snapsPath: fwdtest.Snapshots(
		fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z"),
		fwdtest.Snap("s2", "PROCESSING", "COLLECTION", "2026-09-02T00:00:00.000Z"))}, cmpIn)
	if r.Status != result.Unknown {
		t.Fatalf("status %s", r.Status)
	}
	r, _ = mustRun(t, "compare-nqe-results", map[string]fwdtest.Handler{snapsPath: twoSnaps(), diffPath: fwdtest.Const(404, map[string]any{"message": "no"})}, cmpIn)
	if r.Status != result.Unknown {
		t.Fatalf("status %s", r.Status)
	}
}

func TestCompareNQETruncationIsSaid(t *testing.T) {
	r, _ := mustRun(t, "compare-nqe-results", map[string]fwdtest.Handler{snapsPath: twoSnaps(),
		diffPath: fwdtest.Const(200, map[string]any{"totalNumRows": 500, "rows": []any{map[string]any{"type": "ADDED", "after": map[string]any{"a": 1}}}})}, cmpIn)
	if !strings.Contains(strings.Join(r.Limits, "|"), "500 differences in total") {
		t.Fatalf("%v", r.Limits)
	}
}

func lib() fwdtest.Handler {
	return fwdtest.Const(200, []any{
		map[string]any{"queryId": "Q_BGP", "path": "/Protocols/BGP neighbor state", "intent": "List BGP neighbors that are not established", "repository": "fwd"},
		map[string]any{"queryId": "Q_NTP", "path": "/Config/NTP servers", "intent": "Devices and their NTP servers", "repository": "fwd"},
		map[string]any{"queryId": "Q_X", "path": "/Misc/Other", "intent": "Something else", "repository": "org"}})
}

func TestFindNQERanksByWordsAndSaysItIsAWordMatch(t *testing.T) {
	r, _ := mustRun(t, "find-nqe-query", map[string]fwdtest.Handler{libPath: lib()}, `{"network_id":"n1","question":"which BGP neighbors are down"}`)
	d := fmt.Sprint(r.Evidence[0].Detail)
	if r.Status != result.OK || r.Confidence != result.Inferred || !strings.Contains(d, "Q_BGP") || strings.Contains(d, "Q_X") {
		t.Fatalf("%s %s %s", r.Status, r.Confidence, d)
	}
	if strings.Index(d, "Q_BGP") > strings.Index(d, "Q_NTP") && strings.Contains(d, "Q_NTP") {
		t.Errorf("best match not first: %s", d)
	}
}

func TestFindNQENoMatchIsUnknownNotNoSuchQuery(t *testing.T) {
	r, _ := mustRun(t, "find-nqe-query", map[string]fwdtest.Handler{libPath: lib()}, `{"network_id":"n1","question":"expiring certificates"}`)
	if r.Status != result.Unknown {
		t.Fatalf("status %s", r.Status)
	}
	r, _ = mustRun(t, "find-nqe-query", map[string]fwdtest.Handler{libPath: lib()}, `{"network_id":"n1","question":"the of a"}`)
	if r.Status != result.Error {
		t.Fatalf("a question of stop words is an error, got %s", r.Status)
	}
}
