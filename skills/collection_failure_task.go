package skills

import (
	"context"
	"fmt"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// failedSubTaskCap bounds how many subtasks of a failed task are read and listed.
const failedSubTaskCap = 500

// addTaskReason puts whatever the collector task API says about why a task ended badly into detail: the task's note and progress, and the subtasks that did not succeed with their status and note.
// Nothing here is a log: when the task has no note and no failed subtask note, Forward gave no reason through the API, and the limit says so (a task can fail before it starts, with no start time).
func addTaskReason(ctx context.Context, s *fwd.Session, t fwd.CollectorTask, detail map[string]any, limits *[]string) {
	reasons := false
	full, _, err := s.Client.CollectorTasks.GetWithSubTasks(ctx, t.ID, failedSubTaskCap)
	if err != nil {
		*limits = append(*limits, fmt.Sprintf("the subtasks of task %s could not be read (they need permission to view collector task queues): %v", t.ID, err))
	} else if full != nil {
		if full.Note != "" {
			detail["note"] = full.Note
			reasons = true
		}
		if len(full.Progress) > 0 {
			detail["progress"] = full.Progress
		}
		var bad []map[string]any
		counts := map[string]int{}
		for _, st := range full.SubTasks {
			counts[st.Status]++
			if strings.EqualFold(st.Status, "SUCCEEDED") {
				continue
			}
			row := map[string]any{"device": st.Description, "status": st.Status}
			if st.Note != "" {
				row["note"] = st.Note
				reasons = true
			}
			if !st.StartedAt.IsZero() {
				row["started_at"] = st.StartedAt
			}
			bad = append(bad, row)
		}
		if len(full.SubTasks) > 0 {
			detail["subtasks_by_status"] = counts
		}
		if len(bad) > 0 {
			shown, _ := result.CapRow(bad, 25)
			detail["subtasks_not_succeeded"] = shown
			detail["subtasks_not_succeeded_count"] = len(bad)
		}
	}
	if !reasons {
		*limits = append(*limits, "the collector task API gave no reason for this ending: no task note and no note on a failed subtask"+
			map[bool]string{true: " (the task never started, so it failed before any device work)", false: ""}[t.StartedAt == ""]+
			". The collector's own log or the Forward UI may say more; a network whose only collectable source is an endpoint may need an enabled classic device (unconfirmed)")
	}
}
