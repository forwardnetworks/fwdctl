package skills

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const (
	defaultCollectionHistory = 10
	maxCollectionHistory     = 30
	collectionHistoryWorkers = 6
	// a collection this many times the median of the others is called out
	slowFactor = 1.5
)

type historyPoint struct {
	sn      forward.Snapshot
	metrics *forward.SnapshotMetrics
	task    *forward.CollectorTask
	notes   []string
}

func rfc(t string) (time.Time, bool) {
	v, err := time.Parse(time.RFC3339, t)
	return v, err == nil
}

// collectionHistory is view history: how long each recent collection took, with the devices it covered. Forward records a collection and a processing duration on every
// collected snapshot and ties the snapshot to the collector task that made it, so this reads the snapshots themselves and is not limited to the short window of recent
// collector tasks that inspect-collection shows. A reprocess or an import is not a collection and is left out.
func collectionHistory(ctx context.Context, s *fwd.Session, in collectionInput, cx result.Context) (result.Result, error) {
	all, err := s.Snapshots(ctx, in.NetworkID)
	if err != nil {
		return result.Result{}, err
	}
	var collected []forward.Snapshot
	for _, sn := range all {
		if !sn.Predicted() && !sn.IsDraft && sn.ProcessingTrigger == "COLLECTION" {
			collected = append(collected, sn)
		}
	}
	sort.SliceStable(collected, func(i, j int) bool { return snapCreated(collected[i]) > snapCreated(collected[j]) })
	if len(collected) == 0 {
		return result.NewUnknown(collectionFailureName, "The network has no collected snapshot to read collection history from", cx,
			[]string{"only snapshots Forward collected count (a reprocess, an import or a prediction is not a collection); nothing was measured"}, result.Options{NextActions: []string{"inspect-snapshots"}})
	}
	n := in.Limit
	if n <= 0 {
		n = defaultCollectionHistory
	}
	n = min(n, maxCollectionHistory)
	shown := collected[:min(n, len(collected))]

	pts := make([]*historyPoint, len(shown))
	var wg sync.WaitGroup
	sem := make(chan struct{}, collectionHistoryWorkers)
	for i := range shown {
		pts[i] = &historyPoint{sn: shown[i]}
		wg.Add(1)
		go func(p *historyPoint) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			m, _, err := s.Client.Snapshots.Metrics(ctx, string(p.sn.ID))
			switch {
			case err == nil:
				p.metrics = m
			case fwd.NotFound(err):
				p.notes = append(p.notes, "Forward holds no metrics for this snapshot")
			default:
				p.notes = append(p.notes, "metrics could not be read: "+err.Error())
			}
			if id := string(p.sn.CollectionTaskID); id != "" {
				if !strings.HasPrefix(id, "P") {
					id = "P" + id // Forward records the task id without its prefix
				}
				if t, _, err := s.Client.CollectorTasks.Get(ctx, id); err == nil {
					p.task = t
				} else if !fwd.NotFound(err) {
					p.notes = append(p.notes, "the collector task could not be read: "+err.Error())
				}
			}
		}(pts[i])
	}
	wg.Wait()

	var secs []float64
	rows := make([]map[string]any, 0, len(pts))
	for i, p := range pts {
		row := map[string]any{"snapshot_id": string(p.sn.ID), "created_at": p.sn.CreatedAt, "state": p.sn.State}
		if p.sn.TotalDevices > 0 {
			row["devices"] = p.sn.TotalDevices
			if i+1 < len(collected) && collected[i+1].TotalDevices > 0 {
				row["devices_change"] = p.sn.TotalDevices - collected[i+1].TotalDevices // against the next older collected snapshot, which may be outside the rows shown
			}
		}
		if p.metrics != nil {
			if p.metrics.CollectionDurationMillis != nil {
				c := float64(*p.metrics.CollectionDurationMillis) / 1000
				row["collection_seconds"] = c
				secs = append(secs, c)
			}
			if p.metrics.ProcessingDurationMillis != nil {
				row["processing_seconds"] = float64(*p.metrics.ProcessingDurationMillis) / 1000
			}
			row["collected_devices"] = p.metrics.NumSuccessfulDevices
			row["collection_failed_devices"] = p.metrics.NumCollectionFailureDevices
		}
		if p.task != nil {
			row["task_id"] = string(p.task.ID)
			row["task_started_at"], row["task_finished_at"] = p.task.StartedAt, p.task.FinishedAt
			a, aok := rfc(p.task.StartedAt)
			b, bok := rfc(p.task.FinishedAt)
			if aok && bok {
				row["task_seconds"] = b.Sub(a).Seconds()
			}
			// from the collection ending to the snapshot being usable (processed)
			if ready, rok := rfc(p.sn.ProcessedAt); rok && bok && ready.After(b) {
				row["collection_end_to_processed_seconds"] = ready.Sub(b).Seconds()
			}
		}
		if len(p.notes) > 0 {
			row["notes"] = p.notes
		}
		rows = append(rows, row)
	}

	var limits []string
	stats := map[string]any{"collected_snapshots_in_network": len(collected), "snapshots_read": len(rows), "with_collection_duration": len(secs)}
	finding := fmt.Sprintf("%d collected snapshot(s) read of %d", len(rows), len(collected))
	if len(secs) >= 2 {
		sorted := append([]float64(nil), secs...)
		sort.Float64s(sorted)
		median := sorted[len(sorted)/2]
		stats["median_collection_seconds"], stats["max_collection_seconds"], stats["min_collection_seconds"] = median, sorted[len(sorted)-1], sorted[0]
		slow := 0
		for _, row := range rows {
			if c, ok := row["collection_seconds"].(float64); ok && median > 0 {
				row["vs_median"] = c / median
				if len(secs) >= 4 && c > median*slowFactor {
					row["slower_than_usual"] = true
					slow++
				}
			}
		}
		stats["slower_than_usual"] = slow
		if latest, ok := rows[0]["collection_seconds"].(float64); ok {
			finding = fmt.Sprintf("the latest collection took %s, %.1fx the median %s of the %d read", dur(latest), latest/median, dur(median), len(secs))
			if ch, ok := rows[0]["devices_change"].(int); ok && ch != 0 {
				finding += fmt.Sprintf("; it covered %+d devices against the collection before it", ch)
			}
		}
		if len(secs) < 4 {
			limits = append(limits, "fewer than 4 collections have a recorded duration, so none is flagged slower than usual")
		}
	} else if len(secs) == 1 {
		finding = fmt.Sprintf("the latest recorded collection took %s; one duration is not a trend", dur(secs[0]))
	} else {
		limits = append(limits, "none of the snapshots read has a recorded collection duration: Forward keeps none for an imported or partially collected snapshot, or for one that is still processing")
	}
	limits = append(limits,
		"the median is over collections that may have covered very different numbers of devices: read collection_seconds next to devices before calling a run slow, since a network that grew several times over is expected to take longer",
		"a collection's duration is Forward's own figure for the snapshot (collection_seconds); task_seconds is the collector task's start to finish and includes time outside the device collection, so the two differ",
		"devices and devices_change are the snapshot's device count against the next older collected snapshot; a jump is a change in what the network collects, not a slower collector, and the cause (devices added, a discovery or scope change) is not recorded by Forward on the snapshot",
		"collection_end_to_processed_seconds is the collector task's end to the snapshot being processed; a snapshot reprocessed since has lost its original processing time and shows none",
		"only Forward-collected snapshots are read; reprocesses, imports and predictions are not collections. To see which devices are slow in one of these collections, run view slow on that snapshot")
	if len(collected) > len(rows) {
		limits = append(limits, fmt.Sprintf("%d older collected snapshot(s) were not read; raise limit (at most %d) to read more", len(collected)-len(rows), maxCollectionHistory))
	}
	if len(rows) == 0 {
		return result.NewUnknown(collectionFailureName, "No collected snapshot could be read", cx, limits, result.Options{})
	}
	d := map[string]any{"stats": stats, "snapshots": rows}
	return result.Build(collectionFailureName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits,
		NextActions: []string{"investigate-collection-failure", "inspect-collection", "inspect-snapshots"},
		Evidence:    []result.Evidence{result.NewEvidence(result.EvCollection, "getSnapshotMetrics", fwd.SnapshotIDPtr(&shown[0]), d, finding)}})
}

// dur writes seconds as 3h20m, 12m05s or 45s.
func dur(sec float64) string {
	t := time.Duration(sec * float64(time.Second)).Round(time.Second)
	h, m, s := int(t.Hours()), int(t.Minutes())%60, int(t.Seconds())%60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%02dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
