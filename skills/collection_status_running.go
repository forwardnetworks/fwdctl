package skills

import (
	"context"
	"fmt"
	"sort"
	"time"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const (
	maxRunningTasks   = 2
	maxRunningDevices = 25
	runningSubTaskCap = 1000
)

// addRunningNow says what a running collection is doing at this instant: for each running NETWORK_COLLECTION task of the network, the devices being collected and the device command each is running right now (Forward's Job progress
// dialog shows the same). The task comes from the recent tasks when it is there, else from a read filtered on running tasks, because Forward lists the organisation's newest tasks before filtering by network. A failure to read is a limit.
func addRunningNow(ctx context.Context, s *fwd.Session, networkID string, recent []forward.CollectorTask, detail map[string]any, limits *[]string) {
	var active []forward.CollectorTask
	for _, t := range recent {
		if t.Type == "NETWORK_COLLECTION" && (t.Status == forward.CollectorTaskRunning || t.Status == forward.CollectorTaskQueued) {
			active = append(active, t)
		}
	}
	if len(active) == 0 {
		lim := int32(50)
		run, _, err := s.Client.CollectorTasks.List(ctx, forward.CollectorTaskListOptions{NetworkID: networkID, Statuses: []string{forward.CollectorTaskRunning}, Limit: &lim})
		if err != nil {
			*limits = append(*limits, "the running collector task could not be listed, so the command each device is running is not shown: "+err.Error())
			return
		}
		for _, t := range run {
			if t.Type == "NETWORK_COLLECTION" {
				active = append(active, t)
			}
		}
	}
	if len(active) == 0 {
		*limits = append(*limits, "a collection is in progress but no running NETWORK_COLLECTION task of this network was found, so the command each device is running is not shown")
		return
	}
	shownTasks, totalTasks := result.CapRow(active, maxRunningTasks)
	var out []map[string]any
	now := time.Now()
	for _, t := range shownTasks {
		full, _, err := s.Client.CollectorTasks.GetWithSubTasks(ctx, string(t.ID), runningSubTaskCap)
		if err != nil {
			*limits = append(*limits, fmt.Sprintf("the subtasks of task %s could not be read (it needs permission to view collector task queues): %v", t.ID, err))
			continue
		}
		counts := map[string]int{}
		var devs []map[string]any
		for _, st := range full.SubTasks {
			counts[st.Status]++
			if st.Status != forward.CollectorTaskRunning {
				continue
			}
			row := map[string]any{"device": st.Description, "operation": st.Operation}
			if st.CollectorID != "" {
				row["collector_id"] = string(st.CollectorID)
			}
			if !st.StartedAt.IsZero() {
				row["started_at"] = st.StartedAt.UTC().Format(time.RFC3339)
				row["running_seconds"] = int(now.Sub(st.StartedAt).Seconds())
			}
			devs = append(devs, row)
		}
		sort.SliceStable(devs, func(i, j int) bool {
			a, _ := devs[i]["running_seconds"].(int)
			b, _ := devs[j]["running_seconds"].(int)
			return a > b
		})
		shown, total := result.CapRow(devs, maxRunningDevices)
		row := map[string]any{"task": string(t.ID), "status": t.Status, "subtasks_by_status": counts, "devices_running": total, "running": shown}
		if len(full.SubTasks) >= runningSubTaskCap {
			row["subtask_cap_reached"] = fmt.Sprintf("only the first %d subtasks were read (running ones come first), so the by-status counts of finished devices are partial", runningSubTaskCap)
		}
		out = append(out, row)
	}
	if len(out) > 0 {
		detail["running_now"] = out
		*limits = append(*limits,
			"running_now is one instant: operation is the device command running at the moment of the read (Forward records it only for running subtasks of a collection), so a command that finishes between two reads is missed; ask again, or use wait_seconds and read once more",
			"the subtask route is a Preview, unpublished Forward API (the Job progress dialog reads it); an empty operation means Forward did not report one for that device")
		if totalTasks > len(shownTasks) {
			*limits = append(*limits, fmt.Sprintf("%d running tasks; the first %d are shown", totalTasks, len(shownTasks)))
		}
	}
}
