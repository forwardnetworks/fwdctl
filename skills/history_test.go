package skills_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

func histRoutes(perSnap map[string][]any) map[string]fwdtest.Handler {
	r := map[string]fwdtest.Handler{snapsPath: fwdtest.Snapshots(
		fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z"),
		fwdtest.Snap("s2", "PROCESSED", "COLLECTION", "2026-09-02T00:00:00.000Z"),
		fwdtest.Snap("s3", "PROCESSED", "COLLECTION", "2026-09-03T00:00:00.000Z"),
		map[string]any{"id": "p1", "state": "PROCESSED", "processingTrigger": "PREDICT", "processedAt": "2026-09-09T00:00:00.000Z"})}
	for id, rows := range perSnap {
		rows := rows
		r["GET /api/snapshots/"+id+"/checks"] = func(*http.Request, []byte) (int, any) { return 200, rows }
	}
	return r
}

func hc(status string, v int) map[string]any {
	m := map[string]any{"id": "c1", "name": "no telnet", "status": status}
	if status == "FAIL" {
		m["numViolations"] = v
	}
	return m
}

func TestHistoryFindsWhereTheStatusLastChangedAndSkipsPredictions(t *testing.T) {
	r, srv := mustRun(t, "inspect-history", histRoutes(map[string][]any{"s3": {hc("FAIL", 3)}, "s2": {hc("FAIL", 1)}, "s1": {hc("PASS", 0)}}), `{"network_id":"n1","check_id":"c1"}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "different on snapshot s1") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	for _, c := range srv.Calls() {
		if strings.Contains(c.Path, "/p1/") {
			t.Errorf("a prediction is not history: %s", c.Path)
		}
	}
}

func TestHistoryAbsentIsNotAPassAndAMissingCheckIsUnknown(t *testing.T) {
	r, _ := mustRun(t, "inspect-history", histRoutes(map[string][]any{"s3": {hc("PASS", 0)}, "s2": {hc("PASS", 0)}, "s1": {}}), `{"network_id":"n1","check_id":"c1"}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "predates it") || !strings.Contains(jsonOf(r), "ABSENT") {
		t.Fatalf("%s %s", r.Status, jsonOf(r))
	}
	r, _ = mustRun(t, "inspect-history", histRoutes(map[string][]any{"s3": {}}), `{"network_id":"n1","check_id":"c1"}`)
	if r.Status != result.Unknown {
		t.Fatalf("%s", r.Status)
	}
}

func TestHistoryBoundsItsReadsAndSaysSoWhenNothingChanged(t *testing.T) {
	r, srv := mustRun(t, "inspect-history", histRoutes(map[string][]any{"s3": {hc("PASS", 0)}, "s2": {hc("PASS", 0)}, "s1": {hc("PASS", 0)}}), `{"network_id":"n1","check_id":"c1","snapshots":2}`)
	if len(srv.Calls()) != 3 || !strings.Contains(strings.Join(r.Limits, "|"), "newest 2") {
		t.Fatalf("calls %d limits %v", len(srv.Calls()), r.Limits)
	}
}

func TestHistoryOfADevicesConfigFindsTheNewestChangedPair(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		snapsPath: fwdtest.Snapshots(
			fwdtest.Snap("s3", "PROCESSED", "COLLECTION", "2026-09-03T00:00:00.000Z"),
			fwdtest.Snap("s2", "PROCESSED", "COLLECTION", "2026-09-02T00:00:00.000Z"),
			fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")),
		"GET /api/networks/n1/devices/r1/files": fwdtest.Const(200, map[string]any{"files": []any{map[string]any{"name": "configuration.txt"}}}),
		"GET /api/diffs/s2/s3/files":            fwdtest.Const(200, []any{}),
		"GET /api/diffs/s1/s2/files":            fwdtest.Const(200, []any{map[string]any{"name": "r1", "hasConfigChange": true, "files": []any{map[string]any{"name": "configuration.txt"}}}}),
	}
	r, srv := mustRun(t, "inspect-history", routes, `{"network_id":"n1","device":"r1"}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "snapshot s1") || !strings.Contains(r.Finding, "snapshot s2") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	if writes(srv) != 0 {
		t.Fatal("history only reads")
	}
	r, _ = mustRun(t, "inspect-history", map[string]fwdtest.Handler{
		snapsPath:                               routes[snapsPath],
		"GET /api/networks/n1/devices/r1/files": routes["GET /api/networks/n1/devices/r1/files"],
		"GET /api/diffs/s2/s3/files":            fwdtest.Const(200, []any{}),
		"GET /api/diffs/s1/s2/files":            fwdtest.Const(200, []any{}),
	}, `{"network_id":"n1","device":"r1"}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "No configuration change") {
		t.Fatalf("no change in any pair: %s %s", r.Status, r.Finding)
	}
	r, _ = mustRun(t, "inspect-history", map[string]fwdtest.Handler{snapsPath: routes[snapsPath], "GET /api/networks/n1/devices/ghost/files": fwdtest.Const(404, map[string]any{})}, `{"network_id":"n1","device":"ghost"}`)
	if r.Status != result.Unknown {
		t.Fatalf("a device the newest snapshot does not hold is unknown, not unchanged: %s %s", r.Status, r.Finding)
	}
	if _, _, err := runSkill(t, "inspect-history", routes, `{"network_id":"n1","device":"r1","check_id":"c1"}`); err == nil {
		t.Error("device with check_id must be refused")
	}
}
