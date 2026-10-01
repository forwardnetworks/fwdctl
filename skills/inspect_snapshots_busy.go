package skills

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// busyNetworks is how many networks are read at once.
const busyNetworks = 6

// activelyBusy are the states of a snapshot that is being worked on right now. UNPROCESSED is not one of them: it is a snapshot that is not processed (an invalidated or
// evicted one, waiting to be reprocessed), and a real organization holds many that sat for months (measured: 117 across 8 networks, none active).
func activelyBusy(state string) bool {
	return state == "UNPACKING" || state == "PROCESSING" || state == "RESTORING"
}

// busyAcrossOrg answers "is anything being processed right now, in any network this login can see": every snapshot that is UNPACKING, PROCESSING or RESTORING.
// It reads each network's snapshot list; a network that cannot be read is named, and then "nothing is processing" is not claimed (unknown, never a pass).
func busyAcrossOrg(ctx context.Context, s *fwd.Session, cx result.Context) (result.Result, error) {
	nets, err := s.Networks(ctx)
	if err != nil {
		return result.Result{}, err
	}
	if len(nets) == 0 {
		return result.NewUnknown(inspectSnapshotsName, "This login sees no networks", cx, []string{"no network is visible, so nothing could be read: that is not proof nothing is processing"}, result.Options{})
	}
	type found struct {
		row map[string]any
		at  string
	}
	var mu sync.Mutex
	var busy []found
	unread := map[string]string{}
	unprocessed := 0
	var wg sync.WaitGroup
	sem := make(chan struct{}, busyNetworks)
	for _, n := range nets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			snaps, err := s.Snapshots(ctx, string(n.ID))
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				unread[string(n.ID)] = err.Error()
				return
			}
			for _, sn := range snaps {
				if sn.State == "UNPROCESSED" {
					unprocessed++
				}
				if activelyBusy(sn.State) {
					busy = append(busy, found{map[string]any{"network_id": string(n.ID), "network": n.Name, "snapshot_id": string(sn.ID), "state": sn.State, "kind": kindOf(sn), "since": snapTime(sn)}, snapTime(sn)})
				}
			}
		}()
	}
	wg.Wait()
	sort.SliceStable(busy, func(i, j int) bool {
		if a, b := busy[i].row["network_id"].(string), busy[j].row["network_id"].(string); a != b {
			return a < b
		}
		return busy[i].at < busy[j].at
	})
	rows := make([]map[string]any, len(busy))
	for i, b := range busy {
		rows[i] = b.row
	}
	d := map[string]any{"networks_read": len(nets) - len(unread), "networks_total": len(nets), "in_progress": rows, "unprocessed_not_counted": unprocessed}
	limits := []string{"in progress means a snapshot state of UNPACKING, PROCESSING or RESTORING; each network's own snapshot list was read just now, so this is a moment in time",
		fmt.Sprintf("%d snapshot(s) are UNPROCESSED (not processed, for example invalidated or evicted and waiting to be reprocessed): they are not being worked on and are not counted as in progress", unprocessed)}
	if len(unread) > 0 {
		d["unreadable_networks"] = unread
		limits = append(limits, fmt.Sprintf("%d of %d network(s) could not be read, so processing there is not known", len(unread), len(nets)))
	}
	switch {
	case len(rows) > 0:
		finding := fmt.Sprintf("%d snapshot(s) in progress across %d network(s)", len(rows), distinctNetworks(rows))
		return result.Build(inspectSnapshotsName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits,
			Evidence: []result.Evidence{result.NewEvidence(result.EvCollection, "listSnapshots", nil, d, finding)}, NextActions: []string{"investigate-collection-failure"}})
	case len(unread) > 0:
		return result.NewUnknown(inspectSnapshotsName, fmt.Sprintf("None of the %d readable network(s) has a snapshot in progress, but %d could not be read", len(nets)-len(unread), len(unread)), cx, limits,
			result.Options{Evidence: []result.Evidence{result.NewEvidence(result.EvCollection, "listSnapshots", nil, d, "")}})
	}
	finding := fmt.Sprintf("No snapshot is in progress in any of the %d network(s)", len(nets))
	return result.Build(inspectSnapshotsName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits,
		Evidence: []result.Evidence{result.NewEvidence(result.EvCollection, "listSnapshots", nil, d, finding)}})
}

func distinctNetworks(rows []map[string]any) int {
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r["network_id"].(string)] = true
	}
	return len(seen)
}
