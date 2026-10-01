package skills_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

const (
	tasksPath    = "GET /api/collector-tasks"
	attachPath   = "GET /api/networks/n1/collector"
	statusesPath = "GET /api/networks/n1/device-statuses"
)

func task(id, status, finished string) map[string]any {
	return map[string]any{"id": id, "status": status, "type": "COLLECTION", "networkId": "n1", "startedAt": "2026-09-30T10:00:00Z", "finishedAt": finished}
}

func colStatus(t *testing.T, routes map[string]fwdtest.Handler) result.Result {
	r, _ := mustRun(t, "inspect-collection-status", routes, `{"network_id":"n1"}`)
	return r
}

func TestCollectionStatusFailedWhenTheLastTaskFailedOrADeviceDidOrTheCollectorIsDown(t *testing.T) {
	r := colStatus(t, map[string]fwdtest.Handler{tasksPath: fwdtest.Const(200, []any{task("9", "FAILED", "2026-09-30T10:05:00Z"), task("8", "COMPLETED", "2026-09-29T10:05:00Z")})})
	if r.Status != result.Failed || !strings.Contains(r.Finding, "task 9) ended FAILED") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	r = colStatus(t, map[string]fwdtest.Handler{
		tasksPath: fwdtest.Const(200, []any{task("8", "COMPLETED", "2026-09-29T10:05:00Z")}),
		statusesPath: fwdtest.Const(200, map[string]any{"deviceStatuses": []any{
			map[string]any{"deviceName": "r1", "collectionStatus": "COLLECTED"},
			map[string]any{"deviceName": "r2", "collectionStatus": "FAILED", "collectionFailed": true, "collectionError": "login failed"}}}),
	})
	d := fmt.Sprint(r.Evidence[0].Detail)
	if r.Status != result.Failed || !strings.Contains(d, "r2: LOGIN_FAILED") {
		t.Fatalf("%s %s", r.Status, d)
	}
	r = colStatus(t, map[string]fwdtest.Handler{
		tasksPath:  fwdtest.Const(200, []any{task("8", "COMPLETED", "2026-09-29T10:05:00Z")}),
		attachPath: fwdtest.Const(200, map[string]any{"id": "c1", "name": "dc-collector", "connectionStatus": "DISCONNECTED"}),
	})
	if r.Status != result.Failed || !strings.Contains(r.Finding, `collector "dc-collector" is DISCONNECTED`) {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
}

func TestCollectionStatusHealthyNeedsSomethingThatShowsItWorked(t *testing.T) {
	r := colStatus(t, map[string]fwdtest.Handler{tasksPath: fwdtest.Const(200, []any{task("8", "COMPLETED", "2026-09-29T10:05:00Z")})})
	if r.Status != result.OK || !strings.Contains(r.Finding, "healthy") {
		t.Fatalf("a finished good task is evidence: %s %s", r.Status, r.Finding)
	}
	// silence: nothing running, no tasks, no statuses, no collector
	r = colStatus(t, map[string]fwdtest.Handler{tasksPath: fwdtest.Const(200, []any{})})
	if r.Status != result.Unknown {
		t.Fatalf("silence is not health, got %s %s", r.Status, r.Finding)
	}
}

func colStatusWithSnapshot(t *testing.T, trigger string, extra map[string]fwdtest.Handler) result.Result {
	routes := map[string]fwdtest.Handler{snapsPath: fwdtest.Snapshots(fwdtest.Snap("s9", "PROCESSED", trigger, "2026-09-30T11:00:00.000Z"))}
	for k, v := range extra {
		routes[k] = v
	}
	return colStatus(t, routes)
}

func TestCollectionStatusSaysFirstWhenTheSnapshotWasImportedOrReprocessed(t *testing.T) {
	for trigger, word := range map[string]string{"IMPORT": "imported", "REPROCESS": "reprocessed"} {
		r := colStatusWithSnapshot(t, trigger, nil)
		if r.Status != result.Unknown || !strings.HasPrefix(r.Finding, "The newest snapshot (s9) was "+word+"; no collection ran in Forward") {
			t.Fatalf("%s: unknown is never a pass, and the kind comes first: %s %s", trigger, r.Status, r.Finding)
		}
		all := strings.Join(r.Limits, " | ")
		if strings.Contains(all, "cannot say collection is healthy") || strings.Contains(r.Finding, "No collection is running and nothing shows") {
			t.Errorf("%s: the ambiguous silence wording is for collected snapshots only: %s", trigger, all)
		}
		if !strings.Contains(all, "original source") || len(r.NextActions) == 0 || r.NextActions[0] != "inspect-snapshots" {
			t.Errorf("%s: next actions and limits must name the alternative: %v %s", trigger, r.NextActions, all)
		}
	}
	// an old good collector task does not make an imported snapshot healthy
	r := colStatusWithSnapshot(t, "IMPORT", map[string]fwdtest.Handler{tasksPath: fwdtest.Const(200, []any{task("8", "COMPLETED", "2026-09-29T10:05:00Z")})})
	if r.Status != result.Unknown {
		t.Errorf("%s %s", r.Status, r.Finding)
	}
	// a real failure or a running collection is still reported
	r = colStatusWithSnapshot(t, "IMPORT", map[string]fwdtest.Handler{tasksPath: fwdtest.Const(200, []any{task("9", "FAILED", "2026-09-30T10:05:00Z")})})
	if r.Status != result.Failed {
		t.Errorf("a failed collector task stays failed: %s %s", r.Status, r.Finding)
	}
}

func TestCollectionStatusKeepsTheSilenceWordingForCollectedAndUnknownKinds(t *testing.T) {
	for _, trigger := range []string{"COLLECTION", ""} {
		r := colStatusWithSnapshot(t, trigger, map[string]fwdtest.Handler{tasksPath: fwdtest.Const(200, []any{})})
		if r.Status != result.Unknown || !strings.Contains(r.Finding, "No collection is running and nothing shows how the last one went") || !strings.Contains(strings.Join(r.Limits, " "), "cannot say collection is healthy") {
			t.Errorf("trigger %q: %s %s %v", trigger, r.Status, r.Finding, r.Limits)
		}
	}
	r := colStatusWithSnapshot(t, "", map[string]fwdtest.Handler{tasksPath: fwdtest.Const(200, []any{})})
	if !strings.Contains(strings.Join(r.Limits, " "), "reports no processing trigger") {
		t.Errorf("an unknown kind says so: %v", r.Limits)
	}
	r = colStatusWithSnapshot(t, "COLLECTION", map[string]fwdtest.Handler{tasksPath: fwdtest.Const(200, []any{task("8", "COMPLETED", "2026-09-29T10:05:00Z")})})
	if r.Status != result.OK {
		t.Errorf("a collected snapshot with a good task is still healthy: %s %s", r.Status, r.Finding)
	}
}

func TestCollectionStatusRunningSaysTaskElapsedProgressAndARoughEstimateAndCanWait(t *testing.T) {
	started := time.Now().Add(-90 * time.Second).UTC().Format(time.RFC3339)
	run := map[string]any{"id": "77", "status": "RUNNING", "type": "COLLECTION", "networkId": "n1", "startedAt": started,
		"progress": map[string]any{"total": 10, "succeeded": 4, "running": 2, "queued": 4}}
	routes := map[string]fwdtest.Handler{tasksPath: fwdtest.Const(200, []any{run})}
	r := colStatus(t, routes)
	for _, want := range []string{"task 77", "running for 1m", "4 of 10 sources finished", "rough estimate"} {
		if !strings.Contains(r.Finding, want) {
			t.Errorf("finding %q lacks %q", r.Finding, want)
		}
	}
	r, _ = mustRun(t, "inspect-collection-status", routes, `{"network_id":"n1","wait_seconds":1}`)
	if !strings.Contains(strings.Join(r.Limits, " "), "still running") {
		t.Errorf("a wait that ends with the collection still running says so: %v", r.Limits)
	}
}

func TestCollectionStatusFallsBackToTheNewestSnapshotsResultWhenNoTaskIsInTheWindow(t *testing.T) {
	routes := func(collected bool) map[string]fwdtest.Handler {
		return map[string]fwdtest.Handler{
			tasksPath:                       fwdtest.Const(200, []any{}),
			"GET /api/snapshots/s9/metrics": fwdtest.Const(200, map[string]any{"numSuccessfulDevices": 12}),
			nqePath:                         fwdtest.Const(200, map[string]any{"items": []any{map[string]any{"collected": true}, map[string]any{"collected": collected}}, "totalNumItems": 2}),
		}
	}
	r := colStatusWithSnapshot(t, "COLLECTION", routes(true))
	if r.Status != result.OK || !strings.Contains(strings.Join(r.Limits, " "), "newest collected snapshot's own result") {
		t.Fatalf("a healthy newest snapshot is judged from its own result: %s %s %v", r.Status, r.Finding, r.Limits)
	}
	r = colStatusWithSnapshot(t, "COLLECTION", routes(false))
	if r.Status != result.Failed || !strings.Contains(r.Finding, "1 of 2 cloud account(s) were not collected") {
		t.Fatalf("an uncollected cloud account is a failure: %s %s", r.Status, r.Finding)
	}
}
