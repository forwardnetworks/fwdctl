package skills

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const tailFraction = 0.75

// collectorMaxConcurrency is the largest concurrency Forward accepts for one collector (a range of 1 to 1024, read from Forward's source by a peer session).
const collectorMaxConcurrency = 1024

// addSlotAnalysis adds to the slow view what answers "would more concurrency or collectors help": the slots the collector held beside the devices actually collecting (idle slots, and where work waited
// while slots were idle), what holds them at the worst point, the lower bound on the run against the actual, the tail (devices that ran late, by type and connection) and the devices by the collector they
// are pinned to. It returns a sentence for the finding, or "" when there is nothing to add.
func addSlotAnalysis(ctx context.Context, s *fwd.Session, in collectionInput, cx result.Context, sm *slowSummary, stats map[string]any, configuredConcurrency int, limits *[]string) string {
	origin, ok := runOrigin(sm.m)
	if !ok {
		return ""
	}
	var sentence []string
	concurrency := configuredConcurrency
	var qbuckets []map[string]any
	if q, ok := stats["queue"].(map[string]any); ok {
		if l, ok := q["concurrency_limit"].(int); ok && l > 0 {
			concurrency = l
		}
		qbuckets, _ = q["queue_buckets"].([]map[string]any)
	}
	// idle slots: slots held minus devices in flight, per bucket
	if prof, ok := stats["in_flight"].(map[string]any); ok && len(qbuckets) > 0 {
		flight := map[int64]float64{}
		fb, _ := prof["in_flight_buckets"].([]map[string]any)
		for _, b := range fb {
			flight[b["offset_seconds"].(int64)], _ = b["devices_in_flight"].(float64)
		}
		bucketSeconds, _ := prof["bucket_seconds"].(int64)
		type worst struct {
			off            int64
			held, queued   int
			inFlight, idle float64
		}
		var w worst
		withWork, counted := 0, 0
		for _, qb := range qbuckets {
			off, _ := qb["offset_seconds"].(int64)
			held, _ := qb["max_slots_held"].(int)
			queued, _ := qb["max_queued"].(int)
			f, seen := flight[off]
			if held == 0 || !seen {
				continue
			}
			counted++
			idle := float64(held) - f
			if queued > 0 && idle > float64(held)/2 {
				withWork++
				if idle > w.idle {
					w = worst{off, held, queued, f, idle}
				}
			}
		}
		if counted > 0 {
			idleStats := map[string]any{"buckets_with_queued_work_and_idle_slots": withWork, "of_buckets": counted}
			if withWork > 0 {
				idleStats["worst"] = map[string]any{"offset_seconds": w.off, "slots_held": w.held, "devices_in_flight": w.inFlight, "queued": w.queued, "idle_slots": int(w.idle)}
				if held := whatHoldsSlots(ctx, s, in, cx, origin, w.off+bucketSeconds/2, limits); held != nil {
					idleStats["held_by_at_worst_point"] = held
				}
				sentence = append(sentence, fmt.Sprintf("in %d of %d buckets work waited in the queue while more than half the held slots had no device collecting (worst at %s: %d slots held, about %.0f devices in flight, %d queued)", withWork, counted, dur(float64(w.off)), w.held, w.inFlight, w.queued))
			}
			stats["idle_slots"] = idleStats
		}
	}
	// the lower bound
	if concurrency > 0 && sm.sumMs > 0 && sm.wallMs > 0 {
		longest := float64(sm.maxMs) / 1000
		spread := float64(sm.sumMs) / 1000 / float64(concurrency)
		lower := max(longest, spread)
		actual := float64(sm.wallMs) / 1000
		verdict := ""
		idle, _ := stats["idle_slots"].(map[string]any)
		idleWork, _ := idle["buckets_with_queued_work_and_idle_slots"].(int)
		switch {
		case actual <= lower*1.2 && longest >= spread:
			verdict = fmt.Sprintf("the run is within 20%% of its lower bound and that bound is the longest device alone (%s, against %s of device time spread over %d slots): more concurrency or another collector would not shorten it; only a faster slowest device would", dur(longest), dur(spread), concurrency)
		case actual <= lower*1.2:
			verdict = fmt.Sprintf("the run is within 20%% of its lower bound at concurrency %d, and that bound is device time over slots (%s, the longest device is %s): more concurrency or another collector would shorten it, down to about the longest device", concurrency, dur(spread), dur(longest))
		case idleWork > 0:
			verdict = fmt.Sprintf("the run took %.1fx its lower bound and slots sat idle while work waited, so concurrency is not the limit: more collectors or more slots would not help until what holds the slots is found", actual/lower)
		default:
			verdict = fmt.Sprintf("the run took %.1fx its lower bound without idle slots shown: the shortfall is in how devices were started (ramp-up or a long tail), not in the slot count", actual/lower)
		}
		atMax := concurrency >= collectorMaxConcurrency
		if atMax {
			verdict += fmt.Sprintf(". Concurrency is already at the collector maximum of %d, so more slots per collector are impossible: only more collectors could add capacity", collectorMaxConcurrency)
			if idleWork > 0 {
				verdict += "; with slots idle while work waited, the limit is how slots are handed out, not the concurrency"
			}
		}
		stats["what_if"] = map[string]any{"at_max_concurrency": atMax, "concurrency": concurrency, "device_seconds_total": float64(sm.sumMs) / 1000, "longest_device_seconds": longest, "lower_bound_seconds": lower, "actual_seconds": actual,
			"device_time_over_concurrency_seconds": spread, "verdict": verdict}
		sentence = append(sentence, fmt.Sprintf("lower bound %s (the longer of the longest device and total device time over %d slots) against %s actual", dur(lower), concurrency, dur(actual)))
	}
	addTailAndCollectors(ctx, s, in, sm, stats, origin, limits)
	if len(sentence) == 0 {
		return ""
	}
	return "; " + strings.Join(sentence, "; ")
}

// whatHoldsSlots reads the collector subtasks in progress at an instant and counts them by status and operation: what the held slots are doing when few of them are collecting a device.
func whatHoldsSlots(ctx context.Context, s *fwd.Session, in collectionInput, cx result.Context, originMs int64, offsetSeconds int64, limits *[]string) map[string]any {
	snaps, err := s.Snapshots(ctx, in.NetworkID)
	if err != nil {
		return nil
	}
	snapID := fwd.SnapshotID(cx)
	for _, sn := range snaps {
		if string(sn.ID) != snapID || sn.CollectionTaskID == "" {
			continue
		}
		taskID := string(sn.CollectionTaskID)
		if !strings.HasPrefix(taskID, "P") {
			taskID = "P" + taskID
		}
		at := time.UnixMilli(originMs).Add(time.Duration(offsetSeconds) * time.Second)
		subs, _, err := s.Client.CollectorTasks.SubTasksAt(ctx, taskID, at)
		if err != nil {
			*limits = append(*limits, "the subtasks in progress at the worst point could not be read, so what holds the slots is not shown: "+err.Error())
			return nil
		}
		type key struct{ status, operation, description string }
		counts := map[key]int{}
		for _, st := range subs {
			counts[key{st.Status, st.Operation, groupDescription(st.Description)}]++
		}
		rows := make([]map[string]any, 0, len(counts))
		for k, n := range counts {
			rows = append(rows, map[string]any{"status": k.status, "operation": nilIfEmpty(k.operation), "kind": k.description, "subtasks": n})
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i]["subtasks"].(int) > rows[j]["subtasks"].(int) })
		rows, _ = result.CapRow(rows, 8)
		*limits = append(*limits, "held_by_at_worst_point groups the collector subtasks in progress at that moment by status and operation; Forward's description of a subtask is usually a device name, so the kind column keeps only a generic word of it. Whether a subtask is a post-collection step, a retry wait or a resource wait is Forward's own operation text where it gives one, and blank where it does not")
		return map[string]any{"at_offset_seconds": offsetSeconds, "subtasks_in_progress": len(subs), "groups": rows}
	}
	return nil
}

// groupDescription keeps a generic word of a subtask description so the groups are not one row per device: the first word that is not a device-like token.
func groupDescription(d string) string {
	d = strings.TrimSpace(d)
	if d == "" {
		return "(none)"
	}
	if strings.ContainsAny(d, "0123456789-_.") && !strings.Contains(d, " ") {
		return "(a device)"
	}
	return d
}

type spanMs struct {
	start, end int64
	d          forward.DeviceCollectionMetrics
}

// addTailAndCollectors groups the devices that ran in the last quarter of the run by type and connection, and every device by the collector it is pinned to.
func addTailAndCollectors(ctx context.Context, s *fwd.Session, in collectionInput, sm *slowSummary, stats map[string]any, originMs int64, limits *[]string) {
	var spans []spanMs
	var lastEnd int64
	for _, d := range sm.m.Devices {
		if in.Device != "" && !strings.Contains(strings.ToLower(d.DeviceName), strings.ToLower(in.Device)) {
			continue
		}
		if d.CollectionStartTimeMillis == nil || d.CollectionDurationMillis == nil {
			continue
		}
		st := *d.CollectionStartTimeMillis - originMs
		spans = append(spans, spanMs{st, st + *d.CollectionDurationMillis, d})
		lastEnd = max(lastEnd, st+*d.CollectionDurationMillis)
	}
	if len(spans) == 0 || lastEnd <= 0 {
		return
	}
	cut := int64(float64(lastEnd) * tailFraction)
	group := func(key func(forward.DeviceCollectionMetrics) string, pick func(spanMs) bool) []map[string]any {
		by := map[string][]int64{}
		for _, sp := range spans {
			if pick(sp) {
				by[firstNonEmpty(key(sp.d), "(none)")] = append(by[firstNonEmpty(key(sp.d), "(none)")], *sp.d.CollectionDurationMillis)
			}
		}
		names := make([]string, 0, len(by))
		for k := range by {
			names = append(names, k)
		}
		sort.Slice(names, func(i, j int) bool {
			return len(by[names[i]]) > len(by[names[j]]) || (len(by[names[i]]) == len(by[names[j]]) && names[i] < names[j])
		})
		names, _ = result.CapRow(names, 8)
		out := make([]map[string]any, 0, len(names))
		for _, k := range names {
			d := append([]int64(nil), by[k]...)
			sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
			out = append(out, map[string]any{"name": k, "devices": len(d), "median_ms": d[len(d)/2], "max_ms": d[len(d)-1]})
		}
		return out
	}
	late := func(sp spanMs) bool { return sp.end > cut }
	n := 0
	for _, sp := range spans {
		if late(sp) {
			n++
		}
	}
	tail := map[string]any{"after_seconds": cut / 1000, "devices": n, "of_devices": len(spans), "by_device_type": group(func(d forward.DeviceCollectionMetrics) string { return d.DeviceType }, late),
		"by_connection": group(func(d forward.DeviceCollectionMetrics) string { return d.ConnTypeDisplayName }, late)}
	stats["tail"] = tail
	*limits = append(*limits, fmt.Sprintf("tail is the devices still collecting after %s, the last quarter of the run (%.0f%% of its length), by device type and connection, with the median and longest collection of each group", dur(float64(cut)/1000), tailFraction*100))
	addCollectorBreakdown(ctx, s, in, sm, spans, late, stats, limits)
}

// peakOverlap is the most intervals open at once.
func peakOverlap(iv [][2]int64) int {
	type ev struct {
		t int64
		d int
	}
	evs := make([]ev, 0, 2*len(iv))
	for _, x := range iv {
		evs = append(evs, ev{x[0], 1}, ev{x[1], -1})
	}
	sort.Slice(evs, func(i, j int) bool {
		if evs[i].t != evs[j].t {
			return evs[i].t < evs[j].t
		}
		return evs[i].d < evs[j].d
	})
	cur, peak := 0, 0
	for _, e := range evs {
		cur += e.d
		peak = max(peak, cur)
	}
	return peak
}
