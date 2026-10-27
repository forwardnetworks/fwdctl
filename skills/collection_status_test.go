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
			tasksPath:                            fwdtest.Const(200, []any{}),
			"GET /api/snapshots/s9/metrics":      fwdtest.Const(200, map[string]any{"numSuccessfulDevices": 12}),
			"GET /api/networks/n1/cloudAccounts": fwdtest.Const(200, []any{map[string]any{"type": "AZURE", "name": "azure-prod", "collect": true}}),
			nqePath:                              fwdtest.Const(200, map[string]any{"items": []any{map[string]any{"collected": true, "setup": "azure-prod"}, map[string]any{"collected": collected, "setup": "azure-prod"}}, "totalNumItems": 2}),
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

// A network with cloud setups is not healthy just because its collector tasks ended well: accounts a snapshot did not collect are part of the verdict whether or not a recent task is in
// Forward's window. The finding says how many, and the evidence rolls the coverage up by setup. A network with no cloud setup pays nothing for it.
func TestCollectionStatusCountsUncollectedCloudAccountsEvenWhenRecentTasksSucceeded(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		tasksPath:                            fwdtest.Const(200, []any{task("8", "COMPLETED", "2026-09-29T10:05:00Z")}),
		"GET /api/networks/n1/cloudAccounts": fwdtest.Const(200, []any{map[string]any{"type": "AZURE", "name": "azure-prod", "collect": true}, map[string]any{"type": "AZURE", "name": "azure-test", "collect": true}}),
		nqePath: fwdtest.Const(200, map[string]any{"totalNumItems": 4, "items": []any{
			map[string]any{"collected": true, "setup": "azure-prod"}, map[string]any{"collected": false, "setup": "azure-prod"},
			map[string]any{"collected": false, "setup": "azure-prod"}, map[string]any{"collected": true, "setup": "azure-test"}}}),
	}
	r := colStatusWithSnapshot(t, "COLLECTION", routes)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "2 of 4 cloud account(s) were not collected") || strings.Contains(r.Finding, "healthy") {
		t.Fatalf("recent tasks succeeded but half the cloud accounts were not collected: %s %q", r.Status, r.Finding)
	}
	cov, _ := r.Evidence[0].Detail["cloud_coverage"].(map[string]any)
	setups, _ := cov["by_setup"].([]map[string]any)
	if cov == nil || cov["accounts"] != 4 || cov["collected"] != 2 || len(setups) != 2 || setups[0]["setup"] != "azure-prod" || setups[0]["not_collected"] != 2 {
		t.Errorf("cloud_coverage = %v", cov)
	}
}

func TestCollectionStatusAskedNothingOfNQEForANetworkWithNoCloudSetup(t *testing.T) {
	r, srv := mustRun(t, "inspect-collection-status", map[string]fwdtest.Handler{
		snapsPath:                            fwdtest.Snapshots(fwdtest.Snap("s9", "PROCESSED", "COLLECTION", "2026-09-30T11:00:00.000Z")),
		tasksPath:                            fwdtest.Const(200, []any{task("8", "COMPLETED", "2026-09-29T10:05:00Z")}),
		"GET /api/networks/n1/cloudAccounts": fwdtest.Const(200, []any{}),
	}, `{"network_id":"n1"}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "healthy") {
		t.Errorf("no cloud setup: %s %s", r.Status, r.Finding)
	}
	for _, c := range srv.Calls() {
		if c.Method == "POST" && strings.HasPrefix(c.Path, "/api/nqe") {
			t.Errorf("an NQE query ran for a network with no cloud setup: %s", c.Path)
		}
	}
}

// The first thing to know when asking why no collection ran: no collector is attached. It is said in the finding only when the attachment was read and is not set.
func TestCollectionStatusSaysWhenNoCollectorIsAttached(t *testing.T) {
	none := map[string]fwdtest.Handler{attachPath: fwdtest.Const(200, map[string]any{})}
	if r := colStatusWithSnapshot(t, "REPROCESS", none); r.Status != result.Unknown || !strings.Contains(r.Finding, "No collector is attached to this network") {
		t.Errorf("%s: %s", r.Status, r.Finding)
	}
	set := map[string]fwdtest.Handler{attachPath: fwdtest.Const(200, map[string]any{"id": "c1", "name": "dc-collector", "connectionStatus": "CONNECTED"})}
	if r := colStatusWithSnapshot(t, "REPROCESS", set); strings.Contains(r.Finding, "No collector is attached") {
		t.Errorf("a collector is attached: %s", r.Finding)
	}
}

// While a collection runs, the status view says which device command each running device is on (what Forward's Job progress dialog shows).
func TestCollectionStatusShowsTheCommandEachRunningDeviceIsOn(t *testing.T) {
	running := map[string]any{"id": "9", "status": "RUNNING", "type": "NETWORK_COLLECTION", "networkId": "n1", "startedAt": "2026-10-27T10:00:00Z",
		"progress": map[string]any{"total": 2, "running": 1, "succeeded": 1}}
	r := colStatus(t, map[string]fwdtest.Handler{
		tasksPath: fwdtest.Const(200, []any{running}),
		"GET /api/collector-tasks/9": fwdtest.Const(200, map[string]any{"id": "9", "status": "RUNNING", "type": "NETWORK_COLLECTION", "subTasks": []any{
			map[string]any{"id": "a", "description": "edge1", "status": "RUNNING", "operation": "show sdwan omp routes", "startedAt": "2026-10-27T10:01:00Z"},
			map[string]any{"id": "b", "description": "edge2", "status": "SUCCEEDED", "startedAt": "2026-10-27T10:00:10Z", "finishedAt": "2026-10-27T10:00:50Z"}}}),
	})
	d := fmt.Sprint(r.Evidence[0].Detail["running_now"])
	if !strings.Contains(d, "edge1") || !strings.Contains(d, "show sdwan omp routes") || strings.Contains(d, "edge2") && !strings.Contains(d, "SUCCEEDED:1") {
		t.Fatalf("running_now: %s", d)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "one instant") {
		t.Errorf("limits: %v", r.Limits)
	}
}
