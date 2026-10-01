package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const collectionStatusName = collectionName

type collectionStatusInput struct {
	NetworkID string `json:"network_id"`
	Limit     int    `json:"limit"`
	// WaitSeconds waits (polling every few seconds, at most maxWaitSeconds) for a running collection to finish before reading its outcome.
	WaitSeconds int `json:"wait_seconds"`
}

const maxWaitSeconds = 120

// parseTaskTime reads a collector task's time: RFC 3339, or epoch milliseconds.
func parseTaskTime(v string) (time.Time, bool) {
	if v == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
		return t, true
	}
	if ms, err := strconv.ParseInt(v, 10, 64); err == nil && ms > 1e11 {
		return time.UnixMilli(ms), true
	}
	return time.Time{}, false
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
	if in.WaitSeconds > 0 {
		wait := min(in.WaitSeconds, maxWaitSeconds)
		deadline := time.Now().Add(time.Duration(wait) * time.Second)
		waited := false
		for {
			p, err := s.CollectionProgress(ctx, in.NetworkID)
			if err != nil || p == nil || !p.InProgress {
				break
			}
			waited = true
			if !time.Now().Add(5 * time.Second).Before(deadline) {
				limits = append(limits, fmt.Sprintf("waited %ds and the collection is still running; ask again (wait_seconds) or read the progress below", wait))
				break
			}
			select {
			case <-ctx.Done():
				return result.Result{}, ctx.Err()
			case <-time.After(5 * time.Second):
			}
		}
		if waited {
			detail["waited"] = true
		}
	}
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
	var newest *forward.Snapshot
	if sn, err := s.NewestSnapshot(ctx, in.NetworkID); err != nil {
		limits = append(limits, "the newest snapshot could not be read, so whether Forward collected it is unknown: "+err.Error())
	} else if sn != nil {
		newest = sn
		origin, originID = snapshotOrigin(*sn), string(sn.ID)
		detail["newest_snapshot"] = map[string]any{"id": originID, "kind": kindOf(*sn), "origin": origin}
		if origin == "unknown" {
			limits = append(limits, "the newest snapshot reports no processing trigger, so whether Forward collected it or it was imported is unknown")
		}
	}
	// No recent collector task (Forward lists only the organization's newest ones): fall back to what the newest collected snapshot recorded, which is how a collection a few
	// days old is still judged. It is the snapshot's own result, not a current task, and says so.
	if len(tasks) == 0 && terr == nil && origin == "collection" && newest != nil && fwd.IsReady(newest) {
		if m, merr := s.SnapshotMetrics(ctx, string(newest.ID)); merr != nil {
			limits = append(limits, "the newest snapshot's collection counts could not be read: "+merr.Error())
		} else {
			signals++
			snapInfo := map[string]any{"snapshot": string(newest.ID), "created_at": newest.CreatedAt, "devices_collected": m.SuccessfulDevices, "collection_failed": m.FailedDevices, "processing_failed": m.ProcessingFailedDevices}
			if rows, _, _, qerr := s.RunNQEAll(ctx, in.NetworkID, string(newest.ID), cloudAccountFlags, 5000); qerr == nil && len(rows) > 0 {
				collected := 0
				for _, r := range rows {
					if c, ok := r["collected"].(bool); ok && c {
						collected++
					}
				}
				snapInfo["cloud_accounts"], snapInfo["cloud_accounts_collected"] = len(rows), collected
				if collected < len(rows) {
					failed = append(failed, fmt.Sprintf("%d of %d cloud account(s) were not collected in the newest snapshot", len(rows)-collected, len(rows)))
				}
			}
			detail["newest_snapshot_result"] = snapInfo
			limits = append(limits, "no recent collector task was in Forward's window, so this reads the newest collected snapshot's own result (devices and cloud accounts as that snapshot recorded them), which may be days old")
			if m.FailedDevices+m.ProcessingFailedDevices > 0 {
				failed = append(failed, fmt.Sprintf("the newest collected snapshot has %d device(s) that failed collection and %d that failed processing", m.FailedDevices, m.ProcessingFailedDevices))
			} else if m.SuccessfulDevices > 0 {
				positive = true
			}
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
		finding = runningFinding(detail)
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

// runningFinding says how far a running collection is: its task, how long it has run, how many sources finished, and a rough estimate of the rest.
func runningFinding(detail map[string]any) string {
	finding := "A collection is running now"
	var started time.Time
	if rows, ok := detail["recent_tasks"].([]map[string]any); ok {
		for _, r := range rows {
			if st, _ := r["status"].(string); st == "RUNNING" || st == "QUEUED" {
				finding = fmt.Sprintf("A collection is running now (task %v)", r["id"])
				started, _ = parseTaskTime(fmt.Sprint(r["started_at"]))
				break
			}
		}
	}
	elapsed := time.Duration(0)
	if !started.IsZero() {
		elapsed = time.Since(started).Round(time.Second)
		finding += fmt.Sprintf(", running for %s", elapsed)
	}
	if p, ok := detail["progress"].(map[string]any); ok {
		fin, _ := p["finished"].(int)
		tot, _ := p["total"].(int)
		if tot > 0 {
			finding += fmt.Sprintf(", %d of %d sources finished", fin, tot)
			if fin > 0 && elapsed > 0 && fin < tot {
				rest := time.Duration(float64(elapsed) * float64(tot-fin) / float64(fin)).Round(time.Minute)
				finding += fmt.Sprintf("; a rough estimate of the rest is %s (sources are not equally slow)", rest)
			}
		}
	}
	return finding
}

// cloudAccountFlags reads each cloud account's collected flag in a snapshot.
const cloudAccountFlags = `foreach account in network.cloudAccounts
select { collected: account.collected }`
