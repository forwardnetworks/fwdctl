package skills_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

func snapList() fwdtest.Handler {
	return fwdtest.Snapshots(
		fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z"),
		fwdtest.Snap("s2", "PROCESSED", "COLLECTION", "2026-09-03T00:00:00.000Z"),
		map[string]any{"id": "s3", "state": "PROCESSED", "processingTrigger": "PREDICT", "processedAt": "2026-09-04T00:00:00.000Z", "parentSnapshotId": "s2", "changeSetId": "cs1"},
		fwdtest.Snap("s4", "PROCESSING", "COLLECTION", "2026-09-05T00:00:00.000Z"),
	)
}

func TestInspectSnapshotsMarksTheLatestReadableAndLabelsPredictions(t *testing.T) {
	r, _ := mustRun(t, "inspect-snapshots", map[string]fwdtest.Handler{snapsPath: snapList()}, `{"network_id":"n1"}`)
	d := fmt.Sprint(r.Evidence[0].Detail)
	if r.Status != result.OK || !strings.Contains(d, "latest_readable:s2") {
		t.Fatalf("the newest processed COLLECTED snapshot (not the newer prediction, not the one still processing) is s2: %s", d)
	}
	if !strings.Contains(r.Finding, "1 predicted") || !strings.Contains(d, "kind:predicted") {
		t.Errorf("%s / %s", r.Finding, d)
	}
}

func TestInspectSnapshotsPagesAndSaysWhenThereAreNone(t *testing.T) {
	r, _ := mustRun(t, "inspect-snapshots", map[string]fwdtest.Handler{snapsPath: snapList()}, `{"network_id":"n1","limit":2}`)
	if !strings.Contains(strings.Join(r.Limits, "|"), "Page with offset=2") {
		t.Errorf("%v", r.Limits)
	}
	for _, in := range []string{`{"network_id":"n1","offset":50}`, `{"network_id":"n1","snapshot_id":"nope"}`} {
		r, _ = mustRun(t, "inspect-snapshots", map[string]fwdtest.Handler{snapsPath: snapList()}, in)
		if r.Status != result.Unknown {
			t.Errorf("%s: %s", in, r.Status)
		}
	}
	r, _ = mustRun(t, "inspect-snapshots", map[string]fwdtest.Handler{snapsPath: fwdtest.Snapshots()}, `{"network_id":"n1"}`)
	if r.Status != result.Unknown {
		t.Errorf("no snapshots must be unknown, got %s", r.Status)
	}
}

func TestInspectSnapshotDetailSaysWhenExceptionsCouldNotBeRead(t *testing.T) {
	routes := map[string]fwdtest.Handler{snapsPath: snapList(),
		"GET /api/snapshots/s2/metrics":    fwdtest.Const(200, map[string]any{"numSuccessfulDevices": 12}),
		"GET /api/snapshots/s2/exceptions": fwdtest.Const(403, map[string]any{"message": "no"})}
	r, _ := mustRun(t, "inspect-snapshots", routes, `{"network_id":"n1","snapshot_id":"s2"}`)
	l := strings.Join(r.Limits, "|")
	if r.Status != result.OK || !strings.Contains(l, "exceptions were not read") {
		t.Fatalf("a permission gap must be said, not read as no exceptions: %s %v", r.Status, r.Limits)
	}
}

func progressRoutes(extra map[string]fwdtest.Handler) map[string]fwdtest.Handler {
	start := time.Now().Add(-120 * time.Second).UnixMilli()
	r := map[string]fwdtest.Handler{snapsPath: snapList(),
		"GET /api/snapshots/s4/metrics": fwdtest.Const(200, map[string]any{}), "GET /api/snapshots/s4/exceptions": fwdtest.Const(200, []any{}),
		"GET /api/snapshots/s4/progress": fwdtest.Const(200, map[string]any{"networkId": "n1", "snapshotId": "s4", "done": false, "stages": []any{
			map[string]any{"stage": "CREATION", "operationState": "SUCCEEDED", "startedAt": start, "updatedAt": start + 5000, "numObjects": 12},
			map[string]any{"stage": "REACHABILITY", "operationState": "COMPUTING", "startedAt": start + 60000, "updatedAt": start + 90000},
			map[string]any{"stage": "ADVANCED_REACHABILITY", "operationState": "NOT_TRIGGERED"}}}),
		"GET /api/snapshots/s4/processEstimate": fwdtest.Const(200, map[string]any{"stageToDuration": map[string]any{"REACHABILITY": 300000, "CREATION": 60000}}),
	}
	for k, v := range extra {
		r[k] = v
	}
	return r
}

func TestInspectSnapshotsReportsAgeAndTheProgressOfOneThatIsProcessing(t *testing.T) {
	r, _ := mustRun(t, "inspect-snapshots", progressRoutes(nil), `{"network_id":"n1"}`)
	d := fmt.Sprint(r.Evidence[0].Detail)
	if !strings.Contains(d, "age_seconds:") {
		t.Errorf("each snapshot has an age: %s", d)
	}
	rows := r.Evidence[0].Detail["snapshots"].([]map[string]any)
	var p map[string]any
	for _, row := range rows {
		if row["id"] == "s4" {
			p, _ = row["processing"].(map[string]any)
		}
		if row["id"] != "s4" && row["processing"] != nil {
			t.Errorf("a processed snapshot has no processing block: %v", row)
		}
	}
	if p == nil {
		t.Fatalf("s4 is PROCESSING and must carry processing: %s", d)
	}
	el, _ := p["elapsed_seconds"].(int64)
	if el < 119 || el > 300 || p["stage"] != "REACHABILITY" || p["stage_state"] != "COMPUTING" || p["done"] != false || p["estimate_total_seconds"] != int64(360) || p["started_at"] == nil {
		t.Errorf("processing = %v", p)
	}
	stages := p["stages"].([]map[string]any)
	if len(stages) != 3 || stages[0]["objects"] != int64(12) || stages[2]["started_at"] != nil || stages[2]["state"] != "NOT_TRIGGERED" {
		t.Errorf("a stage that has not started has no times: %v", stages)
	}
	l := strings.Join(r.Limits, "|")
	if !strings.Contains(l, "estimate from earlier runs of this network") || !strings.Contains(l, "not since createdAt") || strings.Contains(l, "SDK has no snapshot progress call") {
		t.Errorf("limits: %v", r.Limits)
	}
	r, _ = mustRun(t, "inspect-snapshots", progressRoutes(nil), `{"network_id":"n1","snapshot_id":"s4"}`)
	if m, _ := r.Evidence[0].Detail["processing"].(map[string]any); m == nil || m["stage"] != "REACHABILITY" {
		t.Errorf("detail view: %v", r.Evidence[0].Detail["processing"])
	}
}

func TestInspectSnapshotsSaysWhyProgressIsMissing(t *testing.T) {
	routes := progressRoutes(map[string]fwdtest.Handler{"GET /api/snapshots/s4/progress": fwdtest.Const(404, map[string]any{"message": "not served"})})
	r, _ := mustRun(t, "inspect-snapshots", routes, `{"network_id":"n1"}`)
	l := strings.Join(r.Limits, "|")
	if !strings.Contains(l, "s4: processing progress could not be read") || !strings.Contains(l, "createdAt") {
		t.Errorf("a failed progress call is said, not omitted: %v", r.Limits)
	}
	routes = progressRoutes(map[string]fwdtest.Handler{"GET /api/snapshots/s4/processEstimate": fwdtest.Const(404, map[string]any{"message": "no"}),
		"GET /api/snapshots/s4/progress": fwdtest.Const(200, map[string]any{"done": false, "stages": []any{map[string]any{"stage": "CREATION", "operationState": "QUEUED"}}})})
	r, _ = mustRun(t, "inspect-snapshots", routes, `{"network_id":"n1","snapshot_id":"s4"}`)
	l = strings.Join(r.Limits, "|")
	p, _ := r.Evidence[0].Detail["processing"].(map[string]any)
	if p == nil || p["started_at"] != nil || p["elapsed_seconds"] != nil || p["stage"] != nil || !strings.Contains(l, "no processing stage has started") || !strings.Contains(l, "processing-time estimate could not be read") {
		t.Errorf("nothing started, no estimate: %v / %v", p, r.Limits)
	}
}

// The metrics go into the evidence as they are, so they must be written under readable names (an untagged field is written under its
// Go name, which a garble release rewrites to an opaque string).
func TestInspectSnapshotDetailWritesMetricsUnderReadableKeys(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		snapsPath:                          fwdtest.Snapshots(fwdtest.Snap("s2", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")),
		"GET /api/snapshots/s2/metrics":    fwdtest.Const(200, map[string]any{"numSuccessfulDevices": 12, "deviceCollectionFailures": map[string]any{"TIMEOUT": 2}}),
		"GET /api/snapshots/s2/exceptions": fwdtest.Const(200, []any{}),
	}
	r, _ := mustRun(t, "inspect-snapshots", routes, `{"network_id":"n1","snapshot_id":"s2"}`)
	b, err := json.Marshal(r.Evidence[0].Detail["metrics"])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"successful_devices":12`, `"collection_failures":{"TIMEOUT":2}`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("metrics %s lacks %s", b, want)
		}
	}
}

func TestInspectSnapshotsWithoutANetworkSaysWhatIsProcessingAnywhere(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		"GET /api/networks":              fwdtest.Const(200, []any{map[string]any{"id": "n1", "name": "one"}, map[string]any{"id": "n2", "name": "two"}}),
		"GET /api/networks/n1/snapshots": fwdtest.Snapshots(fwdtest.Snap("a", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")),
		"GET /api/networks/n2/snapshots": fwdtest.Snapshots(fwdtest.Snap("b", "PROCESSING", "COLLECTION", "2026-09-02T00:00:00.000Z"), fwdtest.Snap("c", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")),
	}
	r, _ := mustRun(t, "inspect-snapshots", routes, `{}`)
	rows := r.Evidence[0].Detail["in_progress"].([]map[string]any)
	if r.Status != result.OK || len(rows) != 1 || rows[0]["network_id"] != "n2" || rows[0]["snapshot_id"] != "b" {
		t.Fatalf("%s %s %v", r.Status, r.Finding, r.Evidence[0].Detail)
	}
	routes["GET /api/networks/n2/snapshots"] = fwdtest.Const(200, []any{})
	r, _ = mustRun(t, "inspect-snapshots", routes, `{}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "No snapshot is in progress in any of the 2") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	// a network that cannot be read: not "nothing is processing"
	routes["GET /api/networks/n2/snapshots"] = fwdtest.Const(403, map[string]any{"message": "no"})
	r, _ = mustRun(t, "inspect-snapshots", routes, `{}`)
	if r.Status != result.Unknown || !strings.Contains(r.Finding, "could not be read") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	if _, _, err := runSkill(t, "inspect-snapshots", routes, `{"kind":"x"}`); err == nil {
		t.Errorf("kind needs a network_id")
	}
}

// A reprocess keeps createdAt and moves processedAt. A week-old snapshot reprocessed today is still the OLDEST data, so it must neither become
// "the newest processed collected" snapshot nor move a backdate's starting point to today.
func reprocessedList() fwdtest.Handler {
	return fwdtest.Snapshots(
		map[string]any{"id": "old", "state": "PROCESSED", "processingTrigger": "REPROCESS", "createdAt": "2026-09-22T07:00:00.000Z", "processedAt": "2026-10-02T14:49:00.000Z"},
		map[string]any{"id": "mid", "state": "PROCESSED", "processingTrigger": "COLLECTION", "createdAt": "2026-09-28T07:00:00.000Z", "processedAt": "2026-09-28T07:30:00.000Z"},
		map[string]any{"id": "new", "state": "PROCESSED", "processingTrigger": "COLLECTION", "createdAt": "2026-10-01T07:00:00.000Z", "processedAt": "2026-10-01T07:30:00.000Z"},
	)
}

func TestInspectSnapshotsOrdersByWhenTheDataWasCollectedNotWhenItWasLastProcessed(t *testing.T) {
	r, _ := mustRun(t, "inspect-snapshots", map[string]fwdtest.Handler{snapsPath: reprocessedList()}, `{"network_id":"n1"}`)
	d := r.Evidence[0].Detail
	if d["latest_readable"] != "new" {
		t.Fatalf("the newest collected snapshot is the one created last, not the one reprocessed last: %v", d["latest_readable"])
	}
	rows := d["snapshots"].([]map[string]any)
	if rows[0]["id"] != "new" || rows[2]["id"] != "old" {
		t.Errorf("rows must be newest data first: %v", rows)
	}
	if rows[2]["at"] != "2026-09-22T07:00:00.000Z" || rows[2]["processed_at"] != "2026-10-02T14:49:00.000Z" {
		t.Errorf("at is the collection time and a reprocess shows up as processed_at: %v", rows[2])
	}
	if _, has := rows[0]["processed_at"]; has && rows[0]["processed_at"] != "2026-10-01T07:30:00.000Z" {
		t.Errorf("processed_at must be the real processing time: %v", rows[0])
	}
}

func TestABackdateFromAReprocessedSnapshotStillReachesEverythingCreatedSince(t *testing.T) {
	routes := wanRoutes(true)
	routes[snapsPath] = reprocessedList()
	in := `{"network_id":"n1","name":"wan-01","connection1":{"device":"r1","port":"Gi0/1","vlan":150},"connection2":{"device":"r2","port":"Gi0/1","vlan":200},"backdate_snapshot_id":"old"}`
	r, _ := mustRun(t, "edit-wan-circuit", routes, in)
	// Forward backdates from the snapshot's creation instant: old (Sep 22), mid and new were all created at or after it. Ordering by processedAt
	// would start at today (the reprocess) and have reached only "old" itself.
	if !strings.Contains(r.Finding, "invalidating 3 snapshot") {
		t.Errorf("want all 3 snapshots created since old: %s", r.Finding)
	}
}
