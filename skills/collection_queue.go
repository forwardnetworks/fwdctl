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

const (
	maxQueueBuckets = 48
	// subtasks read to find the ones that went wrong: Forward lists running, failed, timed-out and cancelled subtasks before succeeded ones
	problemSubTasks  = 3000
	maxEndStateShown = 50
)

// queueProfile buckets Forward's collector task series over the run (the same bucket width as the in-flight profile): the most devices queued, running and holding slots in each
// bucket, so a stretch with nothing running can be told from one where work waited for a slot. gap, when given as offsets in seconds, is summarised on its own. nil when the series is empty.
func queueProfile(p *forward.SnapshotTaskProgress, origin time.Time, bucketSeconds int64, gap [2]int64) map[string]any {
	n := len(p.Timestamps)
	if n == 0 || len(p.Queued) != n || len(p.Running) != n {
		return nil
	}
	at := func(s []int, i int) int {
		if i < len(s) {
			return s[i]
		}
		return 0
	}
	type agg struct{ q, r, c int }
	buckets := map[int64]*agg{}
	var peakQ, peakR, gq, gr, gc int
	haveGap := gap[1] > gap[0]
	for i, ts := range p.Timestamps {
		off := int64(ts.Sub(origin).Seconds())
		if off < 0 {
			off = 0
		}
		b := buckets[off/bucketSeconds]
		if b == nil {
			b = &agg{}
			buckets[off/bucketSeconds] = b
		}
		q, r, c := p.Queued[i], p.Running[i], at(p.Concurrency, i)
		b.q, b.r, b.c = max(b.q, q), max(b.r, r), max(b.c, c)
		peakQ, peakR = max(peakQ, q), max(peakR, r)
		if haveGap && off >= gap[0] && off < gap[1] {
			gq, gr, gc = max(gq, q), max(gr, r), max(gc, c)
		}
	}
	keys := make([]int64, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	rows := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		b := buckets[k]
		rows = append(rows, map[string]any{"offset_seconds": k * bucketSeconds, "max_queued": b.q, "max_running": b.r, "max_slots_held": b.c})
	}
	out := map[string]any{"samples": n, "bucket_seconds": bucketSeconds, "peak_queued": peakQ, "peak_running": peakR, "concurrency_limit": p.ConcurrencyLimits.Global, "buckets": rows}
	if haveGap {
		out["during_the_longest_idle_gap"] = map[string]any{"from_seconds": gap[0], "to_seconds": gap[1], "max_queued": gq, "max_running": gr, "max_slots_held": gc}
	}
	return out
}

// taskEndStates reads the subtasks of a collector task that did not simply succeed (Forward lists running, failed, timed-out and cancelled ones first) and counts them by state,
// with the devices that timed out or were cancelled and how long each ran.
func taskEndStates(ctx context.Context, s *fwd.Session, taskID string) (map[string]any, error) {
	if !strings.HasPrefix(taskID, "P") {
		taskID = "P" + taskID
	}
	t, _, err := s.Client.CollectorTasks.GetWithSubTasks(ctx, taskID, problemSubTasks)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	var bad []map[string]any
	for _, st := range t.SubTasks {
		counts[st.Status]++
		if st.Status != forward.CollectorTaskTimedOut && st.Status != forward.CollectorTaskCanceled && st.Status != forward.CollectorTaskFailed {
			continue
		}
		row := map[string]any{"device": st.Description, "status": st.Status}
		if !st.StartedAt.IsZero() && !st.FinishedAt.IsZero() {
			row["ran_seconds"] = st.FinishedAt.Sub(st.StartedAt).Seconds()
		}
		if st.Note != "" {
			row["note"] = st.Note
		}
		bad = append(bad, row)
	}
	sort.SliceStable(bad, func(i, j int) bool {
		if bad[i]["status"] != bad[j]["status"] {
			return bad[i]["status"].(string) < bad[j]["status"].(string)
		}
		a, _ := bad[i]["ran_seconds"].(float64)
		b, _ := bad[j]["ran_seconds"].(float64)
		return a > b
	})
	out := map[string]any{"subtasks_read": len(t.SubTasks), "by_status": counts, "task_progress": t.Progress}
	if len(bad) > maxEndStateShown {
		out["more_not_shown"] = len(bad) - maxEndStateShown
		bad = bad[:maxEndStateShown]
	}
	out["failed_timed_out_or_cancelled"] = bad
	if len(t.SubTasks) >= problemSubTasks {
		out["cap_reached"] = fmt.Sprintf("the %d-subtask cap was reached, so a timed-out or cancelled device may be missing", problemSubTasks)
	}
	return out, nil
}

// addQueue adds what Forward recorded about queued and running subtasks over the run, and the end state of the devices with no recorded collection (timed out, cancelled, failed), to
// stats. A failure to read either is a limit, not an error: the per-device metrics stand on their own.
func addQueue(ctx context.Context, s *fwd.Session, in collectionInput, cx result.Context, m *forward.SnapshotCollectionMetrics, prof, stats map[string]any, limits *[]string) {
	snapID := fwd.SnapshotID(cx)
	origin, ok := runOrigin(m)
	if !ok {
		return
	}
	var gap [2]int64
	if gaps, ok := prof["idle_gaps"].([]map[string]any); ok {
		gap = [2]int64{gaps[0]["from_seconds"].(int64), gaps[0]["to_seconds"].(int64)}
	}
	if p, _, err := s.Client.CollectorTasks.SnapshotProgress(ctx, snapID); err != nil {
		*limits = append(*limits, "Forward's collector task series (queued and running devices over time) could not be read: "+err.Error())
	} else if p == nil {
		*limits = append(*limits, "Forward returned no collector task series for this snapshot")
	} else if qp := queueProfile(p, time.UnixMilli(origin), prof["bucket_seconds"].(int64), gap); qp == nil {
		*limits = append(*limits, "Forward holds no collector task series for this snapshot (an empty series, not proof nothing was queued)")
	} else {
		stats["queue"] = qp
		*limits = append(*limits, "queue is Forward's own series of devices queued, running and holding concurrency slots, sampled at Forward's intervals and bucketed here by the highest value in each bucket; running at the concurrency limit means the limit was the ceiling, running far below it with devices queued means something other than the global limit held work back (a jump server or vCenter cap, or a dispatch delay), and nothing queued and nothing running is an idle collector")
	}
	nd, ok := stats["no_recorded_duration"].(map[string]any)
	if !ok {
		return
	}
	snaps, err := s.Snapshots(ctx, in.NetworkID)
	if err != nil {
		return
	}
	for _, sn := range snaps {
		if string(sn.ID) != snapID || sn.CollectionTaskID == "" {
			continue
		}
		es, err := taskEndStates(ctx, s, string(sn.CollectionTaskID))
		if err != nil {
			*limits = append(*limits, "the collector task's subtasks could not be read, so the devices with no recorded collection are not classified as timed out or cancelled: "+err.Error())
			return
		}
		nd["end_states"] = es
		*limits = append(*limits, "end_states counts the collector task's subtasks that did not simply succeed (Forward lists running, failed, timed-out and cancelled ones first, up to a cap): TIMED_OUT and CANCELED are Forward's own status, and a timed-out device's ran_seconds should match the per-device collection timeout; subtasks waiting in the queue are never listed")
	}
}
