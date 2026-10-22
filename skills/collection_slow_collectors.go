package skills

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
)

// collectorKey makes the collector ids Forward writes in different forms comparable: "C101290", "c101290" and "101290" are one collector.
func collectorKey(id string) string {
	return strings.TrimLeft(strings.TrimSpace(id), "Cc")
}

type collectorLoad struct {
	id, name             string
	isDefault            bool
	concurrency          int
	pinned, unpinned     int
	pinnedMs, unpinnedMs int64
	longestMs, lastEnd   int64
	late                 int
	ev                   [][2]int64
}

// addCollectorBreakdown groups the run's devices by the collector that ran them: the one they are pinned to, or the network's default collector when they are not. It names each collector, separates
// pinned from unpinned work, says when each finished (the last one is the straggler), puts the collector on each device row, and estimates what spreading the unpinned devices over the collectors that
// already work on this network would do to the run.
func addCollectorBreakdown(ctx context.Context, s *fwd.Session, in collectionInput, sm *slowSummary, spans []spanMs, late func(spanMs) bool, stats map[string]any, limits *[]string) {
	if in.Device != "" {
		return
	}
	list, _, err := s.Client.ClassicDevices.List(ctx, in.NetworkID)
	if err != nil {
		*limits = append(*limits, "the devices' collector pins could not be read, so no per-collector breakdown is shown: "+err.Error())
		return
	}
	pin := map[string]string{}
	for _, c := range list {
		pin[c.Name] = collectorKey(string(c.CollectorID))
	}
	names := map[string]string{}
	if cs, _, cerr := s.Client.Collectors.List(ctx); cerr != nil {
		*limits = append(*limits, "the organization's collectors could not be listed, so collector ids are shown without names: "+cerr.Error())
	} else {
		for _, c := range cs {
			names[collectorKey(string(c.ID))] = c.Name
		}
	}
	defKey, defName, defID := "", "", ""
	if a, aerr := s.CollectorAttachment(ctx, in.NetworkID); aerr == nil && a != nil {
		defID, defName, defKey = string(a.CollectorID), a.CollectorName, collectorKey(string(a.CollectorID))
		if n, ok := names[defKey]; ok && defName == "" {
			defName = n
		}
		if defKey != "" && defName != "" {
			names[defKey] = defName
		}
	}
	by := map[string]*collectorLoad{}
	get := func(key string) *collectorLoad {
		if l := by[key]; l != nil {
			return l
		}
		l := &collectorLoad{id: key, name: names[key], isDefault: key == defKey && key != ""}
		by[key] = l
		return l
	}
	deviceCollector := map[string]string{}
	for _, sp := range spans {
		key, pinned := pin[sp.d.DeviceName], true
		known := false
		if _, ok := pin[sp.d.DeviceName]; ok {
			known = true
		}
		if !known || key == "" {
			key, pinned = defKey, false
		}
		if key == "" {
			key = "(default collector)"
		}
		l := get(key)
		ms := *sp.d.CollectionDurationMillis
		if pinned {
			l.pinned++
			l.pinnedMs += ms
		} else {
			l.unpinned++
			l.unpinnedMs += ms
		}
		l.longestMs, l.lastEnd = max(l.longestMs, ms), max(l.lastEnd, sp.end)
		if late(sp) {
			l.late++
		}
		l.ev = append(l.ev, [2]int64{sp.start, sp.end})
		deviceCollector[sp.d.DeviceName] = key
	}
	if len(by) == 0 {
		return
	}
	// concurrency of each collector that did work, read once each
	keys := make([]string, 0, len(by))
	for k := range by {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return by[keys[i]].pinnedMs+by[keys[i]].unpinnedMs > by[keys[j]].pinnedMs+by[keys[j]].unpinnedMs
	})
	for n, k := range keys {
		if n >= 12 || strings.HasPrefix(k, "(") {
			continue
		}
		id := k
		if k == defKey && defID != "" {
			id = defID
		} else if !strings.HasPrefix(id, "C") {
			id = "C" + id
		}
		if st, _, gerr := s.Client.Collectors.GetSettings(ctx, id); gerr == nil && st != nil {
			by[k].concurrency = st.EffectiveConcurrency()
		}
	}
	rows := make([]map[string]any, 0, len(keys))
	var straggler, second *collectorLoad
	for _, k := range keys {
		l := by[k]
		row := map[string]any{"collector": l.id, "name": nilIfEmpty(l.name), "is_default": l.isDefault, "pinned_devices": l.pinned, "pinned_device_hours": float64(l.pinnedMs) / 3_600_000,
			"unpinned_devices": l.unpinned, "unpinned_device_hours": float64(l.unpinnedMs) / 3_600_000, "device_hours": float64(l.pinnedMs+l.unpinnedMs) / 3_600_000,
			"finished_at_seconds": l.lastEnd / 1000, "devices_in_the_tail": l.late, "peak_in_flight": peakOverlap(l.ev), "longest_device_seconds": float64(l.longestMs) / 1000}
		if l.concurrency > 0 {
			row["concurrency"] = l.concurrency
			row["lower_bound_seconds"] = max(float64(l.longestMs), float64(l.pinnedMs+l.unpinnedMs)/float64(l.concurrency)) / 1000
		}
		rows = append(rows, row)
		if straggler == nil || l.lastEnd > straggler.lastEnd {
			straggler, second = l, straggler
		} else if second == nil || l.lastEnd > second.lastEnd {
			second = l
		}
	}
	// each collector's own resource series over the collection window (a debug permission most logins lack)
	if origin, ok := runOrigin(sm.m); ok {
		denied := false
		for n, k := range keys {
			l := by[k]
			if n >= 6 || l.name == "" || denied {
				continue
			}
			start := time.UnixMilli(origin)
			end := start.Add(time.Duration(l.lastEnd) * time.Millisecond)
			samples, _, merr := s.Client.Collectors.Metrics(ctx, l.name, start, end, 200)
			if merr != nil {
				if op, ok := fwd.MissingPermission(merr); ok {
					*limits = append(*limits, fmt.Sprintf("collector resource metrics (CPU, memory, queue) were not read: this login lacks %s, a Forward debug permission", op))
					denied = true
				} else {
					*limits = append(*limits, fmt.Sprintf("the resource metrics of collector %q could not be read: %v", l.name, merr))
				}
				continue
			}
			if res := collectorResources(samples); res != nil {
				rows[n]["resources"] = res
			} else {
				rows[n]["resources"] = map[string]any{"samples": 0, "verdict": "the collector uploaded no metrics in the collection window"}
			}
		}
	}
	stats["by_collector"] = rows
	if out := collectorOutliers(rows); len(out) > 0 {
		stats["collector_outliers"] = out
		for _, o := range out {
			*limits = append(*limits, fmt.Sprint(o["note"]))
		}
	}
	if straggler != nil && second != nil {
		stats["by_collector_straggler"] = map[string]any{"collector": straggler.id, "name": nilIfEmpty(straggler.name), "finished_at_seconds": straggler.lastEnd / 1000, "next_collector_finished_at_seconds": second.lastEnd / 1000}
	}
	*limits = append(*limits, "by_collector groups the devices by the collector that ran them: the collector id they are pinned to in the collection sources, or, for a device with no pin, the network's default collector (the one attached to the network, is_default). Names come from the organization's collector list; finished_at_seconds is when the collector's last device ended; the series of queued and running subtasks is Forward's and is not split by collector")
	// the collector on each device row, so a paged slow view is a per-device join of collector and duration
	for _, r := range sm.rows {
		if c, ok := deviceCollector[fmt.Sprint(r["device"])]; ok {
			r["collector"] = c
		} else {
			r["collector"] = nil
		}
	}
	addRebalance(sm, spans, by, deviceCollector, pin, defKey, in, stats, limits)
}

// addRebalance estimates the run if the devices with no pin were spread over the collectors that already work on this network. Longest-first greedy: each unpinned device goes to the collector whose
// estimated finish (the longer of its longest device and its device time over its concurrency) it grows least. It is an estimate of the bound, not a schedule.
func addRebalance(sm *slowSummary, spans []spanMs, by map[string]*collectorLoad, deviceCollector map[string]string, pin map[string]string, defKey string, in collectionInput, stats map[string]any, limits *[]string) {
	type cand struct {
		l        *collectorLoad
		load     int64
		longest  int64
		assigned []spanMs
	}
	var cands []*cand
	unpinned := []spanMs{}
	for _, sp := range spans {
		if k, known := pin[sp.d.DeviceName]; !known || k == "" {
			unpinned = append(unpinned, sp)
		}
	}
	if len(unpinned) == 0 {
		return
	}
	for _, l := range by {
		if l.concurrency > 0 && !strings.HasPrefix(l.id, "(") {
			cands = append(cands, &cand{l: l, load: l.pinnedMs, longest: l.longestMs})
		}
	}
	if len(cands) < 2 {
		return
	}
	// what the run would be with the unpinned devices where they are now (on the default collector)
	est := func(c *cand, load, longest int64) float64 {
		return max(float64(longest), float64(load)/float64(c.l.concurrency)) / 1000
	}
	curMax := 0.0
	for _, c := range cands {
		load, longest := c.load, c.longest
		if c.l.id == defKey {
			load, longest = c.l.pinnedMs+c.l.unpinnedMs, c.l.longestMs
		}
		curMax = max(curMax, est(c, load, longest))
	}
	sort.SliceStable(unpinned, func(i, j int) bool {
		return *unpinned[i].d.CollectionDurationMillis > *unpinned[j].d.CollectionDurationMillis
	})
	for _, sp := range unpinned {
		ms := *sp.d.CollectionDurationMillis
		var best *cand
		bestEst := 0.0
		for _, c := range cands {
			e := est(c, c.load+ms, max(c.longest, ms))
			if best == nil || e < bestEst {
				best, bestEst = c, e
			}
		}
		best.load += ms
		best.longest = max(best.longest, ms)
		best.assigned = append(best.assigned, sp)
	}
	newMax := 0.0
	rows := []map[string]any{}
	for _, c := range cands {
		e := est(c, c.load, c.longest)
		newMax = max(newMax, e)
		var hrs int64
		var ex []string
		for _, sp := range c.assigned {
			hrs += *sp.d.CollectionDurationMillis
			if len(ex) < 10 {
				ex = append(ex, sp.d.DeviceName)
			}
		}
		rows = append(rows, map[string]any{"collector": c.l.id, "name": nilIfEmpty(c.l.name), "concurrency": c.l.concurrency, "unpinned_devices_to_run": len(c.assigned), "unpinned_device_hours": float64(hrs) / 3_600_000,
			"estimated_seconds": e, "largest_examples": ex})
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i]["unpinned_devices_to_run"].(int) > rows[j]["unpinned_devices_to_run"].(int)
	})
	var assigns []moveAssign
	for _, c := range cands {
		for _, sp := range c.assigned {
			assigns = append(assigns, moveAssign{sp: sp, to: c.l})
		}
	}
	moves, movesTotal, patch := rebalanceMoves(assigns, by, deviceCollector, pin, in)
	stats["rebalance"] = map[string]any{"unpinned_devices": len(unpinned), "estimated_seconds_now": curMax, "estimated_seconds_if_spread": newMax, "collectors": rows,
		"moves": moves, "moves_total": movesTotal, "moves_shown": len(moves), "edit_source_definition": patch,
		"assumptions": "an estimate of the bound only: each collector's finish is the longer of its longest device and its device time over its concurrency; it ignores start ramp-up, jump servers and per-vendor concurrency limits, assumes durations stay the same on another collector and that every collector can reach every unpinned device"}
	*limits = append(*limits, "rebalance spreads the devices that have no pin over the collectors that already work on this network, longest first, and reports the estimated bound before and after; it changes nothing, and pinning a device to a collector is a separate edit (edit-source object classic_device, collectorId)")
}

// collectorResources summarises a collector's own metric samples over the collection window: the peaks that say whether the collector itself, not the devices, held the run back.
func collectorResources(samples []forward.CollectorMetricSample) map[string]any {
	if len(samples) == 0 {
		return nil
	}
	var cpuPeak, cpuSum, gcLoadPeak float64
	cpuN := 0
	queuePeak, inFlightPeak, conc, filesPeak, filesMax := 0, 0, 0, 0, 0
	jvmFreeMin, vmFreeMin, diskMin := -1, -1, -1
	gcDelta, lastGC := 0, -1
	for _, m := range samples {
		if m.CPU.Load >= 0 {
			cpuPeak, cpuSum, cpuN = max(cpuPeak, float64(m.CPU.Load)), cpuSum+float64(m.CPU.Load), cpuN+1
		}
		queuePeak = max(queuePeak, m.Tasks.QueueSize)
		inFlightPeak = max(inFlightPeak, m.Tasks.TasksStarted-m.Tasks.TasksFinished)
		conc = max(conc, m.Tasks.Concurrency)
		gcLoadPeak = max(gcLoadPeak, float64(m.GC.Load))
		if lastGC >= 0 && m.GC.TimeSeconds >= lastGC { // the counter resets when the collector restarts
			gcDelta += m.GC.TimeSeconds - lastGC
		}
		lastGC = m.GC.TimeSeconds
		filesPeak, filesMax = max(filesPeak, m.Files.Open), max(filesMax, m.Files.Max)
		if jvmFreeMin < 0 || m.Memory.JVMFreeMB < jvmFreeMin {
			jvmFreeMin = m.Memory.JVMFreeMB
		}
		if vmFreeMin < 0 || m.Memory.VMFreeMB < vmFreeMin {
			vmFreeMin = m.Memory.VMFreeMB
		}
		if m.Disk.Collector.SizeMB > 0 && (diskMin < 0 || m.Disk.Collector.AvailableMB < diskMin) {
			diskMin = m.Disk.Collector.AvailableMB
		}
	}
	out := map[string]any{"samples": len(samples), "queue_size_peak": queuePeak, "tasks_in_flight_peak": inFlightPeak, "executor_concurrency": conc, "gc_load_peak": gcLoadPeak, "gc_seconds_in_window": gcDelta,
		"file_descriptors_open_peak": filesPeak, "file_descriptors_max": filesMax, "jvm_free_mb_min": jvmFreeMin, "vm_free_mb_min": vmFreeMin, "collector_disk_available_mb_min": diskMin}
	if cpuN > 0 {
		out["cpu_load_peak"], out["cpu_load_average"] = cpuPeak, cpuSum/float64(cpuN)
	}
	full := conc > 0 && inFlightPeak >= conc*95/100
	hot := cpuPeak >= 0.85
	switch {
	case queuePeak > 0 && (hot || full):
		out["verdict"] = fmt.Sprintf("the collector was a bottleneck: up to %d tasks queued while %s", queuePeak, strings.Join(nonEmpty(map[bool]string{true: fmt.Sprintf("CPU reached %.0f%%", cpuPeak*100)}[hot], map[bool]string{true: fmt.Sprintf("its executor was full (%d of %d running)", inFlightPeak, conc)}[full]), " and "))
	case queuePeak > 0:
		out["verdict"] = fmt.Sprintf("tasks queued (up to %d) but neither CPU (peak %.0f%%) nor the executor (%d of %d running) was saturated: the wait was not the collector's capacity", queuePeak, cpuPeak*100, inFlightPeak, conc)
	default:
		out["verdict"] = fmt.Sprintf("nothing queued on the collector (CPU peak %.0f%%): it was not the bottleneck", cpuPeak*100)
	}
	return out
}

func nonEmpty(l ...string) []string {
	var out []string
	for _, x := range l {
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}

// collectorOutliers names a collector whose peak open file descriptors or queued tasks are at least twice the median of the other collectors (with three or more collectors read, and above a floor that keeps small numbers quiet).
func collectorOutliers(rows []map[string]any) []map[string]any {
	type metric struct {
		key   string
		floor int
	}
	var out []map[string]any
	for _, m := range []metric{{"file_descriptors_open_peak", 1000}, {"queue_size_peak", 100}} {
		type val struct {
			name string
			v    int
		}
		var vals []val
		for _, r := range rows {
			res, _ := r["resources"].(map[string]any)
			if res == nil {
				continue
			}
			if v, ok := res[m.key].(int); ok {
				vals = append(vals, val{fmt.Sprint(firstNonEmptyAny(r["name"], r["collector"])), v})
			}
		}
		if len(vals) < 3 {
			continue
		}
		for i, x := range vals {
			var others []int
			for j, y := range vals {
				if j != i {
					others = append(others, y.v)
				}
			}
			sort.Ints(others)
			med := others[len(others)/2]
			if x.v >= m.floor && x.v >= 2*max(med, 1) {
				out = append(out, map[string]any{"collector": x.name, "metric": m.key, "value": x.v, "others_median": med,
					"note": fmt.Sprintf("collector %s peaked at %d for %s, %.1fx the median (%d) of the other collectors %v", x.name, x.v, strings.ReplaceAll(m.key, "_", " "), float64(x.v)/float64(max(med, 1)), med, others)})
			}
		}
	}
	return out
}

func firstNonEmptyAny(vs ...any) any {
	for _, v := range vs {
		if v != nil && fmt.Sprint(v) != "" && fmt.Sprint(v) != "<nil>" {
			return v
		}
	}
	return ""
}

type moveAssign struct {
	sp spanMs
	to *collectorLoad
}

// rebalanceMoves turns the rebalance assignment into the concrete moves: each device that would run on a different collector than it ran on, longest first, with its connection type and device-hours. A device
// is movable through edit-source classic_device only when it is a classic device with no pin; a device the classic list does not know has its collector set by its source setup, and the row says so. The patch
// is the ready edit-source definition for the shown movable moves. PreferConnection keeps only that connection type; MaxMoves caps the list (default 50, at most 5000).
func rebalanceMoves(assigns []moveAssign, by map[string]*collectorLoad, deviceCollector, pin map[string]string, in collectionInput) ([]map[string]any, int, map[string]any) {
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 5000)
	var all []moveAssign
	for _, a := range assigns {
		if from := deviceCollector[a.sp.d.DeviceName]; from == a.to.id {
			continue
		}
		if in.Failure != "" && !strings.EqualFold(a.sp.d.ConnTypeDisplayName, in.Failure) {
			continue
		}
		all = append(all, a)
	}
	sort.SliceStable(all, func(i, j int) bool {
		return *all[i].sp.d.CollectionDurationMillis > *all[j].sp.d.CollectionDurationMillis
	})
	moves := []map[string]any{}
	var devices []map[string]any
	for _, a := range all {
		if len(moves) >= limit {
			break
		}
		name := a.sp.d.DeviceName
		from := deviceCollector[name]
		fromName := ""
		if l, ok := by[from]; ok {
			fromName = l.name
		}
		_, classic := pin[name]
		row := map[string]any{"device": name, "connection": a.sp.d.ConnTypeDisplayName, "device_type": a.sp.d.DeviceType, "from_collector": nilIfEmpty(from), "from_name": nilIfEmpty(fromName),
			"to_collector": a.to.id, "to_name": nilIfEmpty(a.to.name), "device_hours": float64(*a.sp.d.CollectionDurationMillis) / 3_600_000, "pinned": false, "movable_with": "edit-source classic_device"}
		if !classic {
			row["movable_with"] = "its source setup (not a classic device)"
		} else {
			devices = append(devices, map[string]any{"name": name, "collectorId": a.to.id})
		}
		moves = append(moves, row)
	}
	var patch map[string]any
	if len(devices) > 0 {
		patch = map[string]any{"object": "classic_device", "action": "update", "definition": map[string]any{"devices": devices}}
	}
	return moves, len(all), patch
}
