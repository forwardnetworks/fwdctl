package skills_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
)

// The slow view says whether more concurrency or more collectors would help. It sets the slots the collector held beside the devices actually collecting (idle slots, especially while work waited), gives the
// lower bound on the run (the longest device, or total device time over concurrency) against the actual, and names the tail: the devices that ran late, by type and by the collector they are pinned to.
func slowWhatIfRoutes() map[string]fwdtest.Handler {
	routes := cfRoutes("PROCESSED", nil, nil)
	snap := fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")
	snap["collectionTaskId"] = "500"
	routes[snapsPath] = fwdtest.Snapshots(snap)
	var devs []any
	for i := 0; i < 4; i++ {
		devs = append(devs, map[string]any{"deviceName": "sw" + string(rune('0'+i)), "deviceType": "SWITCH", "connTypeDisplayName": "SSH", "collectionStartTime": 0, "collectionDuration": 300_000})
	}
	devs = append(devs, map[string]any{"deviceName": "late1", "deviceType": "PAN_OS", "connTypeDisplayName": "SSH", "collectionStartTime": 1_000_000, "collectionDuration": 200_000})
	routes["GET /api/networks/n1/collection-metrics"] = fwdtest.Const(200, map[string]any{"snapshotId": "s1", "collectionStartTime": 0, "collectionEndTime": 1_200_000, "metrics": devs})
	routes["GET /api/networks/n1/classic-devices"] = fwdtest.Const(200, []any{map[string]any{"name": "sw0", "host": "h", "collectorId": "C1"}, map[string]any{"name": "late1", "host": "h", "collectorId": "C2"},
		map[string]any{"name": "sw1", "host": "h"}, map[string]any{"name": "sw2", "host": "h"}, map[string]any{"name": "sw3", "host": "h"}})
	routes["GET /api/collectors"] = fwdtest.Const(200, []any{map[string]any{"id": "C1", "name": "Alpha"}, map[string]any{"id": "C2", "name": "Bravo"}, map[string]any{"id": "C7", "name": "Garland"}})
	routes["GET /api/networks/n1/collector"] = fwdtest.Const(200, map[string]any{"id": "C7", "name": "Garland", "connectionStatus": "CONNECTED"})
	routes["GET /api/collectors/C1/collection-settings"] = fwdtest.Const(200, map[string]any{"concurrency": 4})
	routes["GET /api/collectors/C2/collection-settings"] = fwdtest.Const(200, map[string]any{"concurrency": 4})
	routes["GET /api/collectors/C7/collection-settings"] = fwdtest.Const(200, map[string]any{"concurrency": 4})
	routes["GET /api/collector-tasks"] = func(r *http.Request, _ []byte) (int, any) {
		if r.URL.Query().Get("snapshotId") == "" {
			return 200, []any{}
		}
		return 200, map[string]any{"timestamps": []int64{0, 300_000, 600_000, 900_000, 1_100_000},
			"queued": []int{0, 3, 3, 3, 2}, "running": []int{8, 8, 8, 8, 8}, "concurrency": []int{8, 8, 8, 8, 8}, "succeeded": []int{0, 4, 4, 4, 4},
			"concurrencyLimits": map[string]any{"global": 8}}
	}
	routes["GET /api/collector-tasks/P500"] = func(r *http.Request, _ []byte) (int, any) {
		if r.URL.Query().Get("view") == "subtasks" {
			return 200, []any{map[string]any{"id": "h1", "description": "post-collection step", "status": "RUNNING", "startedAt": 0, "operation": "waiting for retry"},
				map[string]any{"id": "h2", "description": "post-collection step", "status": "RUNNING", "startedAt": 0, "operation": "waiting for retry"}}
		}
		return 200, map[string]any{"id": "P500", "status": "FINISHED", "subTasks": []any{}}
	}
	return routes
}

func TestSlowViewShowsIdleSlotsWhileWorkWaitedAndAWhatIfLowerBound(t *testing.T) {
	r, _ := collect(t, slowWhatIfRoutes(), `{"network_id":"n1","view":"slow"}`)
	st := r.Evidence[0].Detail["stats"].(map[string]any)
	idle, _ := st["idle_slots"].(map[string]any)
	if idle == nil || idle["buckets_with_queued_work_and_idle_slots"].(int) < 2 {
		t.Fatalf("slots held with work queued but few devices collecting should be counted: %v", st["idle_slots"])
	}
	if h, _ := idle["held_by_at_worst_point"].(map[string]any); h == nil || h["subtasks_in_progress"] != 2 {
		t.Errorf("what holds the slots at the worst point is read from the running subtasks: %v", idle["held_by_at_worst_point"])
	}
	w, _ := st["what_if"].(map[string]any)
	if w == nil || w["lower_bound_seconds"] != 300.0 || w["actual_seconds"] != 1200.0 {
		t.Fatalf("lower bound is the longest device (300 s) here, actual 1200 s: %v", w)
	}
	if !strings.Contains(w["verdict"].(string), "not the limit") || !strings.Contains(r.Finding, "lower bound") {
		t.Errorf("the verdict should say concurrency is not what limits the run: %v / %s", w["verdict"], r.Finding)
	}
}

func TestSlowViewNamesTheTailAndBreaksDownByCollector(t *testing.T) {
	r, _ := collect(t, slowWhatIfRoutes(), `{"network_id":"n1","view":"slow"}`)
	st := r.Evidence[0].Detail["stats"].(map[string]any)
	tail, _ := st["tail"].(map[string]any)
	if tail == nil || tail["devices"] != 1 {
		t.Fatalf("one device ended in the last quarter of the run: %v", st["tail"])
	}
	bt := tail["by_device_type"].([]map[string]any)
	if len(bt) != 1 || bt[0]["name"] != "PAN_OS" || bt[0]["devices"] != 1 {
		t.Errorf("tail by type = %v", bt)
	}
	pc, _ := st["by_collector"].([]map[string]any)
	byName := map[string]map[string]any{}
	for _, c := range pc {
		byName[c["collector"].(string)] = c
	}
	def := byName["7"]
	if def == nil || def["name"] != "Garland" || def["is_default"] != true || def["unpinned_devices"] != 3 {
		t.Errorf("unpinned devices run on the network's default collector, named Garland: %v", pc)
	}
	if byName["1"]["pinned_devices"] != 1 || byName["1"]["name"] != "Alpha" || byName["2"]["name"] != "Bravo" || byName["1"]["finished_at_seconds"] == nil {
		t.Errorf("pinned devices are grouped under their collector with its name: %v", pc)
	}
	if sg, _ := st["by_collector_straggler"].(map[string]any); sg == nil || sg["name"] != "Bravo" {
		t.Errorf("Bravo ran the late device, so it finished last: %v", st["by_collector_straggler"])
	}
	rb, _ := st["rebalance"].(map[string]any)
	if rb == nil || rb["estimated_seconds_if_spread"].(float64) > rb["estimated_seconds_now"].(float64) {
		t.Errorf("spreading the unpinned devices never makes the bound worse: %v", rb)
	}
	rows := r.Evidence[0].Detail["devices"].([]map[string]any)
	if rows[0]["collector"] == nil {
		t.Errorf("each device row names its collector, so a paged view is a per-device join: %v", rows[0])
	}
}

// When the longest device already accounts for the run, more concurrency cannot help and the verdict says the device is the limit; when device time over slots does, concurrency helps.
func TestSlowViewVerdictSeparatesTheLongestDeviceFromTheSlotCount(t *testing.T) {
	routes := slowWhatIfRoutes()
	var devs []any
	devs = append(devs, map[string]any{"deviceName": "one-slow", "deviceType": "SWITCH", "connTypeDisplayName": "SSH", "collectionStartTime": 0, "collectionDuration": 1_150_000})
	for i := 0; i < 4; i++ {
		devs = append(devs, map[string]any{"deviceName": "q" + string(rune('0'+i)), "deviceType": "SWITCH", "connTypeDisplayName": "SSH", "collectionStartTime": 0, "collectionDuration": 20_000})
	}
	routes["GET /api/networks/n1/collection-metrics"] = fwdtest.Const(200, map[string]any{"snapshotId": "s1", "collectionStartTime": 0, "collectionEndTime": 1_200_000, "metrics": devs})
	r, _ := collect(t, routes, `{"network_id":"n1","view":"slow"}`)
	w := r.Evidence[0].Detail["stats"].(map[string]any)["what_if"].(map[string]any)
	if v := w["verdict"].(string); !strings.Contains(v, "longest device alone") || !strings.Contains(v, "would not shorten") {
		t.Errorf("verdict = %q", v)
	}
}

// With the unpinned devices all on the default collector and the other collectors nearly idle, the estimate shows what spreading them would do, without changing anything.
func TestSlowViewRebalanceEstimateSpreadsUnpinnedDevicesOverTheCollectors(t *testing.T) {
	routes := slowWhatIfRoutes()
	var devs, classic []any
	for i := 0; i < 6; i++ {
		n := "u" + string(rune('0'+i))
		devs = append(devs, map[string]any{"deviceName": n, "deviceType": "ROUTER", "connTypeDisplayName": "SSH", "collectionStartTime": 0, "collectionDuration": 600_000})
		classic = append(classic, map[string]any{"name": n, "host": "h"})
	}
	for _, p := range [][2]string{{"p1", "C1"}, {"p2", "C2"}} {
		devs = append(devs, map[string]any{"deviceName": p[0], "deviceType": "ROUTER", "connTypeDisplayName": "SSH", "collectionStartTime": 0, "collectionDuration": 10_000})
		classic = append(classic, map[string]any{"name": p[0], "host": "h", "collectorId": p[1]})
	}
	for _, c := range []string{"C1", "C2", "C7"} {
		routes["GET /api/collectors/"+c+"/collection-settings"] = fwdtest.Const(200, map[string]any{"concurrency": 2})
	}
	routes["GET /api/networks/n1/collection-metrics"] = fwdtest.Const(200, map[string]any{"snapshotId": "s1", "collectionStartTime": 0, "collectionEndTime": 1_800_000, "metrics": devs})
	routes["GET /api/networks/n1/classic-devices"] = fwdtest.Const(200, classic)
	r, srv := collect(t, routes, `{"network_id":"n1","view":"slow"}`)
	rb, _ := r.Evidence[0].Detail["stats"].(map[string]any)["rebalance"].(map[string]any)
	after, _ := rb["estimated_seconds_if_spread"].(float64)
	if rb == nil || rb["estimated_seconds_now"] != 1800.0 || after < 600 || after > 620 || rb["unpinned_devices"] != 6 {
		t.Fatalf("six 600 s devices on one 2-slot collector take 1800 s; spread over three 2-slot collectors, 600 s: %v", rb)
	}
	if writes(srv) != 0 {
		t.Errorf("an estimate changes nothing")
	}
}

// A collector's own resource series over the collection window says whether the collector itself was the bottleneck: CPU, free memory, queued tasks, tasks in flight against its concurrency, GC. It needs
// Forward's collector-debug permission, and a login without it gets a limit that names the permission, not an error.
func TestSlowViewReadsEachCollectorsResourceSeriesAndNamesTheBottleneckSignal(t *testing.T) {
	routes := slowWhatIfRoutes()
	sample := func(ts string, load float64, queue, started, finished int) map[string]any {
		return map[string]any{"timestamp": ts, "cpuMetrics": map[string]any{"cpuLoad": load, "cpuCores": 8}, "memoryMetrics": map[string]any{"vmMemTotalMb": 32000, "vmMemFreeMb": 9000, "jvmMemMaxMb": 8000, "jvmMemFreeMb": 1500},
			"gcMetrics": map[string]any{"gcTimeSeconds": 10 + queue, "gcLoad": 0.05}, "fileMetrics": map[string]any{"fileDescriptorsMax": 1000, "fileDescriptorsOpen": 400},
			"diskMetrics": map[string]any{"collector": map[string]any{"sizeMb": 1000, "availableMb": 800}}, "taskMetrics": map[string]any{"concurrency": 4, "queueSize": queue, "tasksStarted": started, "tasksFinished": finished}}
	}
	routes["GET /api/collectors/Alpha/metrics"] = fwdtest.Const(200, map[string]any{"metrics": []any{sample("2026-09-01T00:01:00Z", 0.4, 0, 2, 1), sample("2026-09-01T00:10:00Z", 0.95, 30, 40, 36), sample("2026-09-01T00:15:00Z", 0.9, 12, 60, 56)}})
	routes["GET /api/collectors/Bravo/metrics"] = fwdtest.Const(403, map[string]any{"message": "Missing permission: OrgOperation.DEBUG_COLLECTORS"})
	r, _ := collect(t, routes, `{"network_id":"n1","view":"slow"}`)
	var alpha map[string]any
	for _, c := range r.Evidence[0].Detail["stats"].(map[string]any)["by_collector"].([]map[string]any) {
		if c["name"] == "Alpha" {
			alpha = c
		}
	}
	res, _ := alpha["resources"].(map[string]any)
	if res == nil || res["cpu_load_peak"].(float64) < 0.94 || res["queue_size_peak"] != 30 || res["tasks_in_flight_peak"] != 4 {
		t.Fatalf("alpha resources = %v", alpha["resources"])
	}
	if !strings.Contains(res["verdict"].(string), "bottleneck") {
		t.Errorf("queued tasks with a full executor and high CPU is the bottleneck signal: %v", res["verdict"])
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "DEBUG_COLLECTORS") {
		t.Errorf("a missing permission is named in a limit: %v", r.Limits)
	}
}

// A collector configured at its maximum concurrency (1024) cannot be given more slots, so the verdict says so and that only more collectors could add capacity; with idle slots the limit is how slots are handed out.
func TestSlowViewSaysWhenTheCollectorIsAtItsMaximumConcurrency(t *testing.T) {
	routes := slowWhatIfRoutes()
	routes["GET /api/collector-tasks"] = func(r *http.Request, _ []byte) (int, any) {
		if r.URL.Query().Get("snapshotId") == "" {
			return 200, []any{}
		}
		return 200, map[string]any{"timestamps": []int64{0, 300_000, 600_000, 900_000, 1_100_000},
			"queued": []int{0, 3, 3, 3, 2}, "running": []int{8, 8, 8, 8, 8}, "concurrency": []int{1024, 1024, 1024, 1024, 1024}, "succeeded": []int{0, 4, 4, 4, 4},
			"concurrencyLimits": map[string]any{"global": 1024}}
	}
	r, _ := collect(t, routes, `{"network_id":"n1","view":"slow"}`)
	w := r.Evidence[0].Detail["stats"].(map[string]any)["what_if"].(map[string]any)
	v, _ := w["verdict"].(string)
	if !strings.Contains(v, "at the collector maximum of 1024") || !strings.Contains(v, "only more collectors") {
		t.Errorf("the cap must be named: %s", v)
	}
	if w["at_max_concurrency"] != true {
		t.Errorf("at_max_concurrency: %v", w)
	}
}

// Two collections with the same device time but different wall times are the same work dispatched differently.
func TestSlowViewCompareSaysSameWorkDifferentDispatch(t *testing.T) {
	routes := slowProfileRoutes(1_200_000, 300_000)
	// the baseline ran the same devices for the same times (75 minutes of device time) without the stall, so it finished sooner
	var bdevs []any
	for i := 0; i < 5; i++ {
		bdevs = append(bdevs, map[string]any{"deviceName": "a" + string(rune('0'+i)), "deviceType": "SWITCH", "connTypeDisplayName": "SSH", "collectionStartTime": 0, "collectionDuration": 300_000})
		bdevs = append(bdevs, map[string]any{"deviceName": "b" + string(rune('0'+i)), "deviceType": "FIREWALL", "connTypeDisplayName": "PAN-OS", "collectionStartTime": 300_000, "collectionDuration": 600_000})
	}
	base := fwdtest.Const(200, map[string]any{"snapshotId": "base", "collectionStartTime": 0, "collectionEndTime": 900_000, "metrics": bdevs})
	cur := routes["GET /api/networks/n1/collection-metrics"]
	routes["GET /api/networks/n1/collection-metrics"] = func(r *http.Request, b []byte) (int, any) {
		if r.URL.Query().Get("snapshotId") == "base" {
			return base(r, b)
		}
		return cur(r, b)
	}
	r, _ := collect(t, routes, `{"network_id":"n1","view":"slow","compare_to_snapshot_id":"base"}`)
	c := r.Evidence[0].Detail["stats"].(map[string]any)["compare"].(map[string]any)
	d, _ := c["dispatch"].(string)
	if !strings.Contains(d, "same work, different dispatch") || !strings.Contains(r.Finding, "same work, different dispatch") {
		t.Errorf("dispatch %q finding %s", d, r.Finding)
	}
}

// Forward records only the SLOWEST command of each device, so the closest signal to "what do the custom command groups cost" is a rollup of those commands by text (devices, median, p95, max, share of device time) with the
// custom group that holds each, and a rollup by group. It is a lower bound on a command's cost, and the limits say so.
func TestSlowViewRollsUpTheSlowestCommandsAndTheCustomGroupsThatHoldThem(t *testing.T) {
	routes := slowProfileRoutes(1_200_000, 300_000)
	var devs []any
	for i := 0; i < 4; i++ {
		devs = append(devs, map[string]any{"deviceName": fmt.Sprintf("xe%d", i), "deviceType": "ROUTER", "connTypeDisplayName": "SSH", "collectionStartTime": 0, "collectionDuration": 100_000 + i*100_000,
			"slowestCommand": "show sdwan omp routes", "slowestCommandDuration": 60_000 + i*10_000})
	}
	devs = append(devs, map[string]any{"deviceName": "sw1", "deviceType": "SWITCH", "connTypeDisplayName": "SSH", "collectionStartTime": 0, "collectionDuration": 100_000,
		"slowestCommand": "show running-config", "slowestCommandDuration": 20_000})
	routes["GET /api/networks/n1/collection-metrics"] = fwdtest.Const(200, map[string]any{"snapshotId": "s1", "collectionStartTime": 0, "collectionEndTime": 600_000, "metrics": devs})
	routes["GET /api/networks/n1/custom-command-groups"] = fwdtest.Const(200, []any{map[string]any{"name": "Change Validation SD-WAN", "deviceConnTypes": []string{"cisco_ios_xe_ssh"}, "type": "CLI", "commands": []string{"show sdwan omp routes"}}})
	r, _ := collect(t, routes, `{"network_id":"n1","view":"slow"}`)
	st := r.Evidence[0].Detail["stats"].(map[string]any)
	cmds := st["slowest_commands"].([]map[string]any)
	if len(cmds) != 2 || cmds[0]["command"] != "show sdwan omp routes" || fmt.Sprint(cmds[0]["devices"]) != "4" || fmt.Sprint(cmds[0]["max_ms"]) != "90000" || fmt.Sprint(cmds[0]["median_ms"]) != "80000" {
		t.Fatalf("rollup by command: %v", cmds)
	}
	if fmt.Sprint(cmds[0]["custom_group"]) != "[Change Validation SD-WAN]" || cmds[1]["custom_group"] != nil {
		t.Errorf("the custom group is named for the command it holds, not for a built-in: %v", cmds)
	}
	if sh, ok := cmds[0]["share_of_device_time_percent"].(float64); !ok || sh < 20 || sh > 35 {
		t.Errorf("share of device time: %v", cmds[0]["share_of_device_time_percent"])
	}
	groups := st["slowest_by_custom_group"].([]map[string]any)
	if len(groups) != 1 || groups[0]["group"] != "Change Validation SD-WAN" || fmt.Sprint(groups[0]["devices"]) != "4" {
		t.Errorf("rollup by group: %v", groups)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "slowest command of each device") {
		t.Errorf("the limit says only the slowest command per device is recorded: %v", r.Limits)
	}
}

// A collector far above the others in open file descriptors or queued tasks is called out by name, with the figures of the rest for comparison.
func TestSlowViewCallsOutACollectorThatIsFarAboveTheOthers(t *testing.T) {
	routes := slowWhatIfRoutes()
	sample := func(open, queue int) fwdtest.Handler {
		return fwdtest.Const(200, map[string]any{"metrics": []any{map[string]any{"timestamp": "2026-09-01T00:10:00Z", "cpuMetrics": map[string]any{"cpuLoad": 0.5, "cpuCores": 8},
			"fileMetrics": map[string]any{"fileDescriptorsMax": 65000, "fileDescriptorsOpen": open}, "taskMetrics": map[string]any{"concurrency": 4, "queueSize": queue, "tasksStarted": 10, "tasksFinished": 9}}}})
	}
	routes["GET /api/collectors/Alpha/metrics"] = sample(18724, 8075)
	routes["GET /api/collectors/Bravo/metrics"] = sample(3700, 700)
	routes["GET /api/collectors/Garland/metrics"] = sample(3800, 900)
	r, _ := collect(t, routes, `{"network_id":"n1","view":"slow"}`)
	st := r.Evidence[0].Detail["stats"].(map[string]any)
	out, _ := st["collector_outliers"].([]map[string]any)
	got := map[string]bool{}
	for _, o := range out {
		if o["collector"] == "Alpha" {
			got[fmt.Sprint(o["metric"])] = true
		}
	}
	if !got["file_descriptors_open_peak"] || !got["queue_size_peak"] || len(out) != 2 {
		t.Fatalf("Alpha is the outlier on both: %v", out)
	}
	if !strings.Contains(fmt.Sprint(out[0]["note"]), "other collectors") {
		t.Errorf("the others' figures are given: %v", out[0])
	}
}

// Slots held against devices actually collecting: slot-hours (up to) against device-hours, so "slots are held several times longer than the devices need" is a number.
func TestSlowViewStatesSlotTimeAgainstDeviceTime(t *testing.T) {
	r, _ := collect(t, slowWhatIfRoutes(), `{"network_id":"n1","view":"slow"}`)
	st := r.Evidence[0].Detail["stats"].(map[string]any)
	sl, _ := st["slot_time"].(map[string]any)
	if sl == nil {
		t.Fatalf("no slot_time: %v", st)
	}
	held, _ := sl["slot_hours_held_up_to"].(float64)
	dev, _ := sl["device_hours"].(float64)
	if dev <= 0 || held < 2*dev || fmt.Sprint(sl["held_over_device_time"]) == "" {
		t.Errorf("slot_time %v", sl)
	}
	if !strings.Contains(r.Finding, "slot-hours") {
		t.Errorf("the finding says slot-hours against device-hours: %s", r.Finding)
	}
}

// The rebalance block lists the concrete moves (device, from, to, device-hours), longest first, capped by max_moves and narrowed by prefer_connection, with a ready edit-source batch for the classic devices among them.
func TestSlowViewRebalanceListsTheMovesAndABatchPatch(t *testing.T) {
	routes := slowWhatIfRoutes()
	var devs, classic []any
	for i := 0; i < 6; i++ {
		n := "u" + string(rune('0'+i))
		conn := "SSH"
		if i%2 == 1 {
			conn = "TELNET"
		}
		devs = append(devs, map[string]any{"deviceName": n, "deviceType": "ROUTER", "connTypeDisplayName": conn, "collectionStartTime": 0, "collectionDuration": 600_000 - i*1000})
		classic = append(classic, map[string]any{"name": n, "host": "h"})
	}
	for _, p := range [][2]string{{"p1", "C1"}, {"p2", "C2"}} {
		devs = append(devs, map[string]any{"deviceName": p[0], "deviceType": "ROUTER", "connTypeDisplayName": "SSH", "collectionStartTime": 0, "collectionDuration": 10_000})
		classic = append(classic, map[string]any{"name": p[0], "host": "h", "collectorId": p[1]})
	}
	for _, c := range []string{"C1", "C2", "C7"} {
		routes["GET /api/collectors/"+c+"/collection-settings"] = fwdtest.Const(200, map[string]any{"concurrency": 2})
	}
	routes["GET /api/networks/n1/collection-metrics"] = fwdtest.Const(200, map[string]any{"snapshotId": "s1", "collectionStartTime": 0, "collectionEndTime": 1_800_000, "metrics": devs})
	routes["GET /api/networks/n1/classic-devices"] = fwdtest.Const(200, classic)
	run := func(in string) map[string]any {
		r, _ := collect(t, routes, in)
		rb, _ := r.Evidence[0].Detail["stats"].(map[string]any)["rebalance"].(map[string]any)
		if rb == nil {
			t.Fatalf("no rebalance block")
		}
		return rb
	}
	rb := run(`{"network_id":"n1","view":"slow","limit":2}`)
	moves, _ := rb["moves"].([]map[string]any)
	if len(moves) != 2 || rb["moves_total"].(int) < 3 || moves[0]["to_collector"] == moves[0]["from_collector"] || moves[0]["device_hours"].(float64) < moves[1]["device_hours"].(float64) {
		t.Fatalf("moves are capped by max_moves, differ in collector and run longest first: %v total %v", moves, rb["moves_total"])
	}
	patch, _ := rb["edit_source_definition"].(map[string]any)
	if patch == nil || patch["object"] != "classic_device" || len(patch["definition"].(map[string]any)["devices"].([]map[string]any)) != 2 {
		t.Errorf("a ready batch for edit-source: %v", patch)
	}
	for _, m := range run(`{"network_id":"n1","view":"slow","failure":"telnet"}`)["moves"].([]map[string]any) {
		if m["connection"] != "TELNET" {
			t.Errorf("prefer_connection keeps one connection type: %v", m)
		}
	}
}

// Cohorts compare in one call: the device filter takes several names, each group states its share of the total device time, and stats.by_setup groups devices by the controller-managed setup that collects them.
func TestSlowViewComparesCohortsInOneCall(t *testing.T) {
	routes := slowWhatIfRoutes()
	routes["GET /api/networks/n1/controller-managed-setups"] = fwdtest.Const(200, []any{map[string]any{"name": "pilot", "controllers": []any{map[string]any{"name": "sw0"}}, "managedDevices": []any{map[string]any{"name": "sw1"}}}})
	r, _ := collect(t, routes, `{"network_id":"n1","view":"slow","device":"sw0, late1"}`)
	st := r.Evidence[0].Detail["stats"].(map[string]any)
	rows := r.Evidence[0].Detail["devices"].([]map[string]any)
	if len(rows) != 2 {
		t.Fatalf("a comma list keeps sw0 and late1 only: %v", rows)
	}
	var share float64
	for _, g := range st["by_device_type"].([]map[string]any) {
		share += g["share_of_device_time"].(float64)
	}
	if share < 0.999 || share > 1.001 {
		t.Errorf("the shares of the device types add up to 1: %v", st["by_device_type"])
	}
	by := st["by_setup"].([]map[string]any)
	names := map[string]int{}
	for _, g := range by {
		names[fmt.Sprint(g["name"])] = g["devices_timed"].(int)
	}
	if names["pilot"] != 1 || names["(not in a controller-managed setup)"] != 1 {
		t.Errorf("sw0 is in the pilot setup, late1 is not: %v", by)
	}
}
