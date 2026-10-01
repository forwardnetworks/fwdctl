package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const collectionStatusName = collectionName

type collectionStatusInput struct {
	NetworkID string `json:"network_id"`
	Limit     int    `json:"limit"`
}

// inspectCollectionStatus says where collection stands NOW: running or not, how the last tasks ended, which collector serves the
// network and whether it is connected, and which devices failed. It complements investigate-collection-failure, which explains
// why a finished snapshot is missing devices.
func inspectCollectionStatus(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in collectionStatusInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	var limits []string
	detail := map[string]any{}
	signals := 0
	positive := false // something measured says collection worked: a finished task that succeeded, or device statuses read
	failed := []string{}

	if p, err := s.CollectionProgress(ctx, in.NetworkID); err != nil {
		limits = append(limits, "the running collection could not be read: "+err.Error())
	} else if p != nil {
		signals++
		detail["running"] = p.InProgress
		if p.InProgress {
			detail["progress"] = map[string]any{"finished": p.Finished, "total": p.Total, "active": p.Active}
		}
	}

	tasks, terr := s.RecentTasks(ctx, in.NetworkID, 20)
	switch {
	case terr != nil:
		limits = append(limits, "recent collector tasks could not be read: "+terr.Error())
	case len(tasks) == 0:
		limits = append(limits, "no recent collector tasks for this network were found in Forward's window (it lists the organisation's newest tasks first, so an older one can be outside it)")
	default:
		signals++
		n := min(max(in.Limit, 0), 20)
		if n == 0 {
			n = 5
		}
		rows := make([]map[string]any, 0, n)
		for i, t := range tasks {
			if i == n {
				break
			}
			row := map[string]any{"id": string(t.ID), "status": t.Status, "type": t.Type}
			if t.StartedAt != "" {
				row["started_at"] = t.StartedAt
			}
			if t.FinishedAt != "" {
				row["finished_at"] = t.FinishedAt
			}
			if t.CreatedBy != "" {
				row["by"] = t.CreatedBy
			}
			rows = append(rows, row)
		}
		detail["recent_tasks"] = rows
		if last := lastFinished(tasks); last != nil {
			if failedStatus(last.Status) {
				failed = append(failed, fmt.Sprintf("the last finished collection (task %s) ended %s", last.ID, last.Status))
			} else {
				positive = true
			}
		}
	}

	if a, err := s.CollectorAttachment(ctx, in.NetworkID); err != nil {
		limits = append(limits, "the collector attachment could not be read: "+err.Error())
	} else if a != nil {
		signals++
		set := a.IsSet || a.CollectorName != "" || a.ConnectionStatus != ""
		att := map[string]any{"set": set, "name": a.CollectorName, "connection": a.ConnectionStatus, "update": a.UpdateStatus}
		detail["collector"] = att
		if set && a.ConnectionStatus != "" && !strings.EqualFold(a.ConnectionStatus, "CONNECTED") {
			failed = append(failed, fmt.Sprintf("the collector %q is %s", a.CollectorName, a.ConnectionStatus))
		}
		if !set {
			limits = append(limits, "no collector is attached to this network (it may collect from Forward's own collector)")
		}
	}

	if st, err := s.DeviceCollectionStatuses(ctx, in.NetworkID); err != nil {
		limits = append(limits, "per-device collection statuses could not be read: "+err.Error())
	} else if len(st) > 0 {
		signals++
		positive = true
		byStatus := map[string]int{}
		var bad []string
		for _, d := range st {
			name := firstNonEmptyStr(d.DeviceName, d.Name)
			status := firstNonEmptyStr(d.CollectionStatus, d.Status, d.State, "UNKNOWN")
			byStatus[status]++
			if d.CollectionFailed || failedStatus(status) {
				line := name
				if d.CollectionError != "" {
					line += ": " + oneLineNote(d.CollectionError)
				}
				bad = append(bad, line)
			}
		}
		detail["devices"] = map[string]any{"total": len(st), "by_status": byStatus}
		if len(bad) > 0 {
			shown := bad
			if len(shown) > 10 {
				shown = shown[:10]
				limits = append(limits, fmt.Sprintf("%d devices failed collection; the first 10 are listed", len(bad)))
			}
			detail["failed_devices"] = shown
			failed = append(failed, fmt.Sprintf("%d device(s) failed collection", len(bad)))
		}
	}

	// Which kind of snapshot is the newest decides what silence means: an IMPORT or REPROCESS snapshot was never collected by Forward.
	origin, originID := "", ""
	if sn, err := s.NewestSnapshot(ctx, in.NetworkID); err != nil {
		limits = append(limits, "the newest snapshot could not be read, so whether Forward collected it is unknown: "+err.Error())
	} else if sn != nil {
		origin, originID = snapshotOrigin(*sn), string(sn.ID)
		detail["newest_snapshot"] = map[string]any{"id": originID, "kind": kindOf(*sn), "origin": origin}
		if origin == "unknown" {
			limits = append(limits, "the newest snapshot reports no processing trigger, so whether Forward collected it or it was imported is unknown")
		}
	}
	running0, _ := detail["running"].(bool)
	if (origin == "import" || origin == "reprocess") && !running0 && len(failed) == 0 {
		verb := map[string]string{"import": "imported", "reprocess": "reprocessed"}[origin]
		finding := fmt.Sprintf("The newest snapshot (%s) was %s; no collection ran in Forward for it", originID, verb)
		next := []string{"inspect-snapshots", "inspect-inventory"}
		lim := append(limits, "status stays unknown: Forward has no collection to report on for this snapshot, so this is neither a pass nor a failure of collection")
		if origin == "import" {
			lim = append(lim, "check the original source of the import (the system or archive it was exported from) for how its collection went, or the import itself (inspect-snapshots shows its state and message)")
		} else {
			lim = append(lim, "a reprocess recomputes from data an earlier collection or import already gathered; how that collection went is on the original snapshot it was reprocessed from (inspect-snapshots lists the older snapshots), or check the original source")
			next = append(next, "investigate-collection-failure")
		}
		return result.NewUnknown(collectionStatusName, finding, cx, lim, result.Options{NextActions: next,
			Evidence: []result.Evidence{result.NewEvidence(result.EvCollection, "collectionStatus", nil, detail, finding)}})
	}

	if signals == 0 {
		return result.NewUnknown(collectionStatusName, "No collection state could be read", cx,
			append(limits, "nothing was readable: the network id may be wrong or the login lacks access"), result.Options{NextActions: []string{"inspect-networks"}})
	}
	finding := "Collection looks healthy: the latest task and the device statuses show no failure"
	status := result.OK
	running, _ := detail["running"].(bool)
	switch {
	case len(failed) > 0:
		status, finding = result.Failed, strings.Join(failed, "; ")
	case running:
		finding = "A collection is running now"
	case !positive:
		// Nothing failed, but nothing shows it worked either: silence is not health.
		return result.NewUnknown(collectionStatusName, "No collection is running and nothing shows how the last one went", cx,
			append(limits, "no finished task and no device statuses were readable, so this skill cannot say collection is healthy"), result.Options{NextActions: []string{"inspect-snapshots", "investigate-collection-failure"}})
	}
	return result.Build(collectionStatusName, status, finding, result.Deterministic, cx, result.Options{Limits: limits,
		NextActions: []string{"investigate-collection-failure", "inspect-snapshots"},
		Evidence:    []result.Evidence{result.NewEvidence(result.EvCollection, "collectionStatus", nil, detail, finding)}})
}

// snapshotOrigin reads how a snapshot came to exist from the trigger inspect-snapshots shows in its kind: "import", "reprocess",
// "collection", or "unknown" when Forward reports no trigger (or one this skill does not know).
func snapshotOrigin(sn forward.Snapshot) string {
	switch strings.ToUpper(sn.ProcessingTrigger) {
	case "IMPORT":
		return "import"
	case "REPROCESS":
		return "reprocess"
	case "COLLECTION":
		return "collection"
	}
	return "unknown"
}

func lastFinished(tasks []forward.CollectorTask) *forward.CollectorTask {
	var best *forward.CollectorTask
	for i := range tasks {
		if tasks[i].FinishedAt == "" {
			continue
		}
		if best == nil || tasks[i].FinishedAt > best.FinishedAt {
			best = &tasks[i]
		}
	}
	return best
}

func failedStatus(s string) bool {
	u := strings.ToUpper(s)
	return strings.Contains(u, "FAIL") || strings.Contains(u, "ERROR") || u == "CANCELED" || u == "CANCELLED"
}

func firstNonEmptyStr(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
