package skills

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

type slowAgg struct {
	durs    []int64
	sum     int64
	withErr int
}

// slowSummary is the numbers of one collection, computed the same way for the snapshot asked about and for a baseline it is compared with.
type slowSummary struct {
	rows                   []map[string]any // one per device (after the name filter), slowest first
	stats                  map[string]any
	devices, timed, errs   int
	sumMs, wallMs          int64
	medianMs, p95Ms, maxMs int64
	parallelism            float64
	byType, byConn         map[string]*slowAgg
	errorsByType           map[string]int
	m                      *forward.SnapshotCollectionMetrics
}

func summariseSlow(m *forward.SnapshotCollectionMetrics, filter string) *slowSummary {
	sm := &slowSummary{m: m, stats: map[string]any{}, errorsByType: map[string]int{}}
	var durs []int64
	for _, d := range m.Devices {
		if filter != "" && !strings.Contains(strings.ToLower(d.DeviceName), strings.ToLower(filter)) {
			continue
		}
		row := map[string]any{"device": d.DeviceName, "source_type": d.SourceType, "device_type": d.DeviceType, "connection": d.ConnTypeDisplayName,
			"slowest_command": d.SlowestCommand, "jump_server": nilIfEmpty(d.JumpServer), "error": noneIsNil(d.Error)}
		row["collection_ms"], row["slowest_command_ms"] = nil, nil
		if d.CollectionDurationMillis != nil {
			row["collection_ms"] = *d.CollectionDurationMillis
			durs = append(durs, *d.CollectionDurationMillis)
			sm.timed++
			sm.sumMs += *d.CollectionDurationMillis
		}
		if d.SlowestCommandDurationMillis != nil {
			row["slowest_command_ms"] = *d.SlowestCommandDurationMillis
		}
		if e, ok := row["error"].(string); ok {
			sm.errs++
			sm.errorsByType[errorLabel(e)]++
		}
		sm.rows = append(sm.rows, row)
	}
	sm.devices = len(sm.rows)
	ms := func(r map[string]any) int64 {
		if v, ok := r["collection_ms"].(int64); ok {
			return v
		}
		return -1
	}
	sort.SliceStable(sm.rows, func(i, j int) bool { return ms(sm.rows[i]) > ms(sm.rows[j]) })
	sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
	sm.stats["devices"], sm.stats["with_duration"], sm.stats["with_error"], sm.stats["sum_ms"] = sm.devices, sm.timed, sm.errs, sm.sumMs
	if len(durs) > 0 {
		sm.medianMs, sm.p95Ms, sm.maxMs = durs[len(durs)/2], durs[(len(durs)*95)/100], durs[len(durs)-1]
		sm.stats["median_ms"], sm.stats["p95_ms"], sm.stats["max_ms"] = sm.medianMs, sm.p95Ms, sm.maxMs
	}
	if filter == "" && m.CollectionStartTimeMillis != nil && m.CollectionEndTimeMillis != nil && *m.CollectionEndTimeMillis > *m.CollectionStartTimeMillis {
		sm.wallMs = *m.CollectionEndTimeMillis - *m.CollectionStartTimeMillis
		sm.parallelism = float64(sm.sumMs) / float64(sm.wallMs)
		sm.stats["collection_wall_ms"], sm.stats["implied_parallelism"] = sm.wallMs, sm.parallelism
	}
	sm.byType = slowGroupMap(m.Devices, func(d forward.DeviceCollectionMetrics) string { return d.DeviceType }, filter)
	sm.byConn = slowGroupMap(m.Devices, func(d forward.DeviceCollectionMetrics) string { return d.ConnTypeDisplayName }, filter)
	sm.stats["by_device_type"], sm.stats["by_connection"] = topSlowGroups(sm.byType, 8), topSlowGroups(sm.byConn, 8)
	if len(sm.errorsByType) > 0 {
		sm.stats["errors_by_type"] = sm.errorsByType
	}
	return sm
}

// slowCollection is view slow: each device's collection duration and its slowest command, slowest first, with the totals that say whether the collector or the devices set the pace:
// total device time, the run's wall time, the average number of devices collected at once beside the collector's configured concurrency, how many were in flight over time, and groups
// by device type and connection. compare_to_snapshot_id sets a second collection beside it. Forward keeps one slowest command per device, not every command, and nothing for an
// imported or partially collected snapshot.
func slowCollection(ctx context.Context, s *fwd.Session, in collectionInput, cx result.Context) (result.Result, error) {
	m, err := s.CollectionMetrics(ctx, in.NetworkID, fwd.SnapshotID(cx))
	if fwd.NotFound(err) {
		return result.NewUnknown(collectionFailureName, "Forward holds no collection metrics for this snapshot", cx,
			[]string{"collection metrics are not saved for an imported or partially collected snapshot, so nothing was measured"}, result.Options{})
	}
	if err != nil {
		return result.Result{}, err
	}
	if m == nil || len(m.Devices) == 0 {
		return result.NewUnknown(collectionFailureName, "Forward returned no per-device collection metrics", cx,
			[]string{"no device rows came back; an empty answer says nothing about speed or errors"}, result.Options{})
	}
	sm := summariseSlow(m, in.Device)
	if sm.devices == 0 {
		return result.NewUnknown(collectionFailureName, "No device matches the filter", cx, []string{"names are matched as a substring; the snapshot has " + fmt.Sprint(len(m.Devices)) + " device rows"}, result.Options{})
	}
	stats := sm.stats
	var limits []string
	if in.Device == "" && sm.wallMs == 0 {
		limits = append(limits, "Forward recorded no collection start and end for this snapshot, so the implied parallelism (total device time over wall time) is not computed")
	}
	concurrency, concDefault := 0, false
	if in.Device == "" {
		if name, conc, isDefault, cerr := s.CollectorConcurrency(ctx, in.NetworkID); cerr != nil {
			limits = append(limits, "the collector's configured concurrency could not be read ("+cerr.Error()+"), so implied_parallelism has nothing to be compared with")
		} else {
			concurrency, concDefault = conc, isDefault
			stats["collector"] = map[string]any{"name": name, "concurrency": conc, "concurrency_is_default": isDefault}
			if sm.parallelism > 0 && conc > 0 {
				stats["concurrency_in_use_percent"] = sm.parallelism / float64(conc) * 100
			}
		}
		if prof := inFlightProfile(m); prof != nil {
			stats["in_flight"] = prof
		} else {
			limits = append(limits, "the devices carry no collection start time, so how many were in flight over the run is not computed")
		}
	}
	// a second collection to set beside this one
	var cmp *slowComparison
	if in.CompareToSnapshotID != "" {
		bm, berr := s.CollectionMetrics(ctx, in.NetworkID, in.CompareToSnapshotID)
		switch {
		case fwd.NotFound(berr) || (berr == nil && (bm == nil || len(bm.Devices) == 0)):
			limits = append(limits, fmt.Sprintf("Forward holds no collection metrics for compare_to_snapshot_id %s (an imported, reprocessed or partially collected snapshot keeps none), so there is nothing to compare with", in.CompareToSnapshotID))
		case berr != nil:
			return result.Result{}, berr
		default:
			cmp = compareSlow(sm, summariseSlow(bm, in.Device), in.CompareToSnapshotID)
			stats["compare"] = cmp.detail
		}
	}
	win, wl, ok := window(sm.rows, in.Limit, in.Offset, defaultFailureRows, maxFailureRows, "devices")
	if !ok {
		return result.NewUnknown(collectionFailureName, fmt.Sprintf("Offset %d is beyond the %d devices", in.Offset, len(sm.rows)), cx, []string{"offset is past the end of the list"}, result.Options{})
	}
	limits = append(limits, wl...)
	limits = append(limits,
		"implied_parallelism is the total device collection time over the collection's wall time: the average number of devices being collected at once. If it sits near the collector's configured concurrency (128 unless configured), the collector's slots are the limit and a slower run means more devices or more time per device, not a stalled collector; it is an average, so a ramp-up or a long tail lowers it: in_flight shows when",
		"in_flight counts a device from its recorded start to its recorded end, and that start can be when the device was handed to the collector rather than when it got a slot, so the count can exceed the collector's configured concurrency (seen: 1,788 in the first five minutes against 1,024); read it as 'started and not finished', and use idle_gaps and finished_by_seconds for the shape of the run",
		"in_flight is built from each device's own start time and duration: devices_in_flight is the average number collecting during each bucket, and finished_by_seconds says when 50%, 90%, 99% and all of the devices had finished. It shows WHEN concurrency fell (a slow start, a mid-run stall, a long tail); it does not say why, which Forward does not record",
		"errors_by_type counts every error class on the devices, including ones Forward tags on devices it did not collect (for example LICENSE_EXHAUSTED); the snapshot's collection-failure count (investigate-collection-failure view summary) was seen to leave such devices out, so the two totals need not agree",
		"durations are milliseconds; Forward keeps only each device's slowest command (not every command), and a device with no duration has no recorded collection. error is the collection and processing error merged, so it is every error class, not only failures. For what a device did, read its log (view logs).")
	finding := fmt.Sprintf("%d device(s); slowest collection %s", sm.devices, sm.rows[0]["device"])
	if v, ok := sm.rows[0]["collection_ms"].(int64); ok {
		finding += fmt.Sprintf(" at %.1fs", float64(v)/1000)
	}
	// the headline numbers go in the finding: a table or CSV rendering prints the finding, not the stats
	if sm.timed > 0 {
		finding += fmt.Sprintf("; median %.0fs, p95 %.0fs, total device time %s", float64(sm.medianMs)/1000, float64(sm.p95Ms)/1000, dur(float64(sm.sumMs)/1000))
	}
	if sm.parallelism > 0 {
		finding += fmt.Sprintf("; on average %.0f devices at once over %s", sm.parallelism, dur(float64(sm.wallMs)/1000))
		if c, ok := stats["collector"].(map[string]any); ok {
			if concDefault {
				finding += fmt.Sprintf(" (collector %s runs %d at once, the default)", c["name"], concurrency)
			} else {
				finding += fmt.Sprintf(" (collector %s is configured for %d at once, not the default %d)", c["name"], concurrency, forward.DefaultCollectorConcurrency)
			}
		}
	}
	if prof, ok := stats["in_flight"].(map[string]any); ok {
		if fin, ok := prof["finished_by_seconds"].(map[string]float64); ok && fin["p99"] > 0 {
			finding += fmt.Sprintf("; 99%% of devices had finished by %s, the last at %s", dur(fin["p99"]), dur(fin["max"]))
		}
	}
	if prof, ok := stats["in_flight"].(map[string]any); ok {
		if gaps, ok := prof["idle_gaps"].([]map[string]any); ok {
			g := gaps[0]
			finding += fmt.Sprintf("; NOTHING was being collected for %s (%s to %s)", dur(float64(g["idle_seconds"].(int64))), dur(float64(g["from_seconds"].(int64))), dur(float64(g["to_seconds"].(int64))))
			if a, ok := prof["started_after_the_longest_gap"].(map[string]any); ok && a["devices"].(int) > 0 {
				finding += fmt.Sprintf(", then %d more devices started", a["devices"])
				if top, ok := a["most_common"].([]map[string]any); ok && len(top) > 0 {
					finding += fmt.Sprintf(", mostly %v %v (%v)", top[0]["connection"], top[0]["device_type"], top[0]["devices"])
				}
			}
		}
	}
	if cmp != nil {
		finding += "; " + cmp.summary
	}
	d := map[string]any{"stats": stats, "offset": in.Offset, "devices": win}
	return result.Build(collectionFailureName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits,
		NextActions: []string{"inspect-collection", "inspect-device-files"},
		Evidence:    []result.Evidence{result.NewEvidence(result.EvCollection, "getCollectionMetrics", cx.SnapshotID, d, finding)}})
}

const maxInFlightBuckets = 48

// inFlightProfile is how many devices were being collected over the run: the average per bucket (5 minutes, longer when the run is), the peak and when the profile first reached
// 90% of it, and when 50%, 90%, 99% and all devices had finished. It needs each device's own start time; nil when none has one.
func inFlightProfile(m *forward.SnapshotCollectionMetrics) map[string]any {
	type span struct{ s, e int64 }
	var spans []span
	var first, last int64 = math.MaxInt64, 0
	for _, d := range m.Devices {
		if d.CollectionStartTimeMillis == nil || d.CollectionDurationMillis == nil {
			continue
		}
		st, en := *d.CollectionStartTimeMillis, *d.CollectionStartTimeMillis+*d.CollectionDurationMillis
		spans = append(spans, span{st, en})
		first, last = min(first, st), max(last, en)
	}
	if len(spans) == 0 {
		return nil
	}
	origin := first
	if m.CollectionStartTimeMillis != nil && *m.CollectionStartTimeMillis <= first {
		origin = *m.CollectionStartTimeMillis
	}
	wall := last - origin
	if wall <= 0 {
		return nil
	}
	bucket := int64(300_000)
	if wall/bucket >= maxInFlightBuckets {
		bucket = (wall/maxInFlightBuckets/60_000 + 1) * 60_000
	}
	nb := int((wall + bucket - 1) / bucket)
	area := make([]float64, nb)
	ends := make([]int64, 0, len(spans))
	for _, sp := range spans {
		s, e := sp.s-origin, sp.e-origin
		ends = append(ends, e)
		for b := int(s / bucket); b < nb && int64(b)*bucket < e; b++ {
			lo, hi := max(s, int64(b)*bucket), min(e, int64(b+1)*bucket)
			if hi > lo {
				area[b] += float64(hi - lo)
			}
		}
	}
	buckets := make([]map[string]any, nb)
	avgs := make([]float64, nb)
	peak, peakAt := 0.0, 0
	for b := range area {
		width := min(bucket, wall-int64(b)*bucket)
		if width <= 0 {
			width = bucket
		}
		avg := area[b] / float64(width)
		avgs[b] = avg
		buckets[b] = map[string]any{"offset_seconds": int64(b) * bucket / 1000, "devices_in_flight": math.Round(avg*10) / 10}
		if avg > peak {
			peak, peakAt = avg, b
		}
	}
	ramp := 0
	for b := range area {
		if width := min(bucket, wall-int64(b)*bucket); width > 0 && area[b]/float64(width) >= 0.9*peak {
			ramp = b
			break
		}
	}
	sort.Slice(ends, func(i, j int) bool { return ends[i] < ends[j] })
	// the time by which a fraction p of the devices had finished: the ceil(p*n)-th smallest end
	at := func(p float64) float64 {
		i := int(math.Ceil(p*float64(len(ends)))) - 1
		return float64(ends[max(0, min(len(ends)-1, i))]) / 1000
	}
	gaps := idleGaps(avgs, bucket)
	out := map[string]any{"bucket_seconds": bucket / 1000, "devices_with_start_time": len(spans), "peak_devices_in_flight": math.Round(peak*10) / 10,
		"peak_at_seconds": int64(peakAt) * bucket / 1000, "seconds_to_reach_90_percent_of_peak": int64(ramp) * bucket / 1000, "buckets": buckets,
		"finished_by_seconds": map[string]float64{"p50": at(0.5), "p90": at(0.9), "p99": at(0.99), "max": float64(ends[len(ends)-1]) / 1000}}
	if len(gaps) > 0 {
		out["idle_gaps"] = gaps
		// what started after the longest gap: a second batch often has one thing in common
		end := gaps[0]["to_seconds"].(int64) * 1000
		type key struct{ typ, conn string }
		after, counts := 0, map[key]int{}
		for _, d := range m.Devices {
			if d.CollectionStartTimeMillis != nil && d.CollectionDurationMillis != nil && *d.CollectionStartTimeMillis-origin >= end {
				after++
				counts[key{d.DeviceType, d.ConnTypeDisplayName}]++
			}
		}
		var ks []key
		for k := range counts {
			ks = append(ks, k)
		}
		sort.Slice(ks, func(i, j int) bool {
			if counts[ks[i]] != counts[ks[j]] {
				return counts[ks[i]] > counts[ks[j]]
			}
			return ks[i].typ+ks[i].conn < ks[j].typ+ks[j].conn
		})
		top := []map[string]any{}
		for i, k := range ks {
			if i == 3 {
				break
			}
			top = append(top, map[string]any{"device_type": k.typ, "connection": k.conn, "devices": counts[k]})
		}
		out["started_after_the_longest_gap"] = map[string]any{"devices": after, "most_common": top}
	}
	return out
}

// idleGaps finds stretches inside the run, at least three buckets long, where fewer than one device was being collected on average, longest first (at most three). A run that
// goes quiet in the middle and then starts again is two phases, which the average concurrency hides.
func idleGaps(avgs []float64, bucket int64) []map[string]any {
	first, last := -1, -1
	for b, a := range avgs {
		if a >= 1 {
			if first < 0 {
				first = b
			}
			last = b
		}
	}
	if first < 0 {
		return nil
	}
	type gap struct{ from, to int }
	var gaps []gap
	for b := first; b <= last; b++ {
		if avgs[b] >= 1 {
			continue
		}
		e := b
		for e+1 <= last && avgs[e+1] < 1 {
			e++
		}
		if e-b+1 >= 3 {
			gaps = append(gaps, gap{b, e + 1})
		}
		b = e
	}
	sort.SliceStable(gaps, func(i, j int) bool { return gaps[i].to-gaps[i].from > gaps[j].to-gaps[j].from })
	var out []map[string]any
	for i, g := range gaps {
		if i == 3 {
			break
		}
		out = append(out, map[string]any{"from_seconds": int64(g.from) * bucket / 1000, "to_seconds": int64(g.to) * bucket / 1000, "idle_seconds": int64(g.to-g.from) * bucket / 1000})
	}
	return out
}

type slowComparison struct {
	detail  map[string]any
	summary string
}

// compareSlow sets the collection asked about (cur) beside a baseline: the change in total device time, parallelism and the per-device figures, and per device type and connection.
func compareSlow(cur, base *slowSummary, baseID string) *slowComparison {
	pct := func(now, then float64) any {
		if then == 0 {
			return nil
		}
		return math.Round((now-then)/then*1000) / 10
	}
	side := func(sm *slowSummary) map[string]any {
		o := map[string]any{"devices": sm.devices, "sum_ms": sm.sumMs, "median_ms": sm.medianMs, "p95_ms": sm.p95Ms, "max_ms": sm.maxMs, "with_error": sm.errs}
		if sm.parallelism > 0 {
			o["collection_wall_ms"], o["implied_parallelism"] = sm.wallMs, sm.parallelism
		}
		return o
	}
	group := func(a, b map[string]*slowAgg) []map[string]any {
		names := map[string]bool{}
		for n := range a {
			names[n] = true
		}
		for n := range b {
			names[n] = true
		}
		type row struct {
			name string
			m    map[string]any
			abs  int64
		}
		var rows []row
		for n := range names {
			ca, cb := a[n], b[n]
			if ca == nil {
				ca = &slowAgg{}
			}
			if cb == nil {
				cb = &slowAgg{}
			}
			med := func(g *slowAgg) int64 {
				if len(g.durs) == 0 {
					return 0
				}
				d := append([]int64(nil), g.durs...)
				sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
				return d[len(d)/2]
			}
			ch := ca.sum - cb.sum
			rows = append(rows, row{n, map[string]any{"name": n, "devices": len(ca.durs), "baseline_devices": len(cb.durs), "sum_ms": ca.sum, "baseline_sum_ms": cb.sum,
				"change_ms": ch, "median_ms": med(ca), "baseline_median_ms": med(cb)}, max(ch, -ch)})
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].abs != rows[j].abs {
				return rows[i].abs > rows[j].abs
			}
			return rows[i].name < rows[j].name
		})
		out := make([]map[string]any, 0, min(len(rows), 10))
		for i, r := range rows {
			if i == 10 {
				break
			}
			out = append(out, r.m)
		}
		return out
	}
	change := map[string]any{"devices": cur.devices - base.devices, "sum_ms": cur.sumMs - base.sumMs, "sum_percent": pct(float64(cur.sumMs), float64(base.sumMs)),
		"median_ms": cur.medianMs - base.medianMs, "p95_ms": cur.p95Ms - base.p95Ms, "max_ms": cur.maxMs - base.maxMs}
	summary := fmt.Sprintf("against %s: total device time %s to %s", baseID, dur(float64(base.sumMs)/1000), dur(float64(cur.sumMs)/1000))
	if base.medianMs > 0 {
		summary += fmt.Sprintf(", median %.0fs to %.0fs", float64(base.medianMs)/1000, float64(cur.medianMs)/1000)
	}
	if cur.parallelism > 0 && base.parallelism > 0 {
		change["implied_parallelism"], change["implied_parallelism_percent"] = cur.parallelism-base.parallelism, pct(cur.parallelism, base.parallelism)
		summary += fmt.Sprintf(", devices at once %.0f to %.0f (%+.0f%%)", base.parallelism, cur.parallelism, 100*(cur.parallelism-base.parallelism)/base.parallelism)
	}
	return &slowComparison{summary: summary, detail: map[string]any{"baseline_snapshot_id": baseID, "baseline": side(base), "current": side(cur), "change": change,
		"by_device_type": group(cur.byType, base.byType), "by_connection": group(cur.byConn, base.byConn)}}
}

func slowGroupMap(devs []forward.DeviceCollectionMetrics, label func(forward.DeviceCollectionMetrics) string, filter string) map[string]*slowAgg {
	groups := map[string]*slowAgg{}
	for _, d := range devs {
		if filter != "" && !strings.Contains(strings.ToLower(d.DeviceName), strings.ToLower(filter)) {
			continue
		}
		name := label(d)
		if name == "" {
			name = "(none)"
		}
		g := groups[name]
		if g == nil {
			g = &slowAgg{}
			groups[name] = g
		}
		if d.CollectionDurationMillis != nil {
			g.durs = append(g.durs, *d.CollectionDurationMillis)
			g.sum += *d.CollectionDurationMillis
		}
		if noneIsNil(d.Error) != nil {
			g.withErr++
		}
	}
	return groups
}

// topSlowGroups is the groups that took the most total time, at most n, as rows.
func topSlowGroups(groups map[string]*slowAgg, n int) []map[string]any {
	names := make([]string, 0, len(groups))
	for k := range groups {
		names = append(names, k)
	}
	sort.Slice(names, func(i, j int) bool {
		if groups[names[i]].sum != groups[names[j]].sum {
			return groups[names[i]].sum > groups[names[j]].sum
		}
		return names[i] < names[j]
	})
	if len(names) > n {
		names = names[:n]
	}
	out := make([]map[string]any, 0, len(names))
	for _, k := range names {
		g := groups[k]
		d := append([]int64(nil), g.durs...)
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		row := map[string]any{"name": k, "devices_timed": len(d), "sum_ms": g.sum, "with_error": g.withErr}
		if len(d) > 0 {
			row["median_ms"], row["max_ms"] = d[len(d)/2], d[len(d)-1]
		}
		out = append(out, row)
	}
	return out
}

// errorLabel reduces an error to its class: the text before the first colon or newline, at most 60 characters, so thousands of devices with the same error count as one group.
func errorLabel(e string) string {
	e = strings.TrimSpace(e)
	if i := strings.IndexAny(e, ":\n"); i > 0 {
		e = e[:i]
	}
	if len(e) > 60 {
		e = e[:60]
	}
	return e
}
