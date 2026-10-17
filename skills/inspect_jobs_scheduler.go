package skills

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// schedulerView is inspect-jobs state scheduler: the job scheduler's debug view (system administrators only), grouped to one row per organization, worker type, job type, worker class (REGULAR or LARGE) and task status
// (INITIALIZED, QUEUED, RUNNING). It answers "is this organization's Predict running or waiting, and in which worker class". It is a point-in-time dump with no stability promise, not a history of transitions, and it
// does not carry the per-organization worker cap (that is backend configuration with no route).
func schedulerView(ctx context.Context, s *fwd.Session, cx result.Context, match string, limit, offset int) (result.Result, error) {
	view, _, err := s.Client.Jobs.DebugView(ctx)
	if err != nil {
		reason := "GET /api/debug/jobs failed: " + err.Error()
		if fwd.Status(err) == 403 {
			reason = "Forward refused the scheduler view (403): it needs ADMINISTER_SYSTEM, a system administrator login; " + err.Error()
		}
		return result.NewUnknown(inspectJobsName, "The scheduler's jobs could not be read", cx, []string{reason + "; nothing is known about the queue"}, result.Options{})
	}
	type key struct{ org, workerType, jobType, class, status string }
	type agg struct{ tasks, jobs int }
	groups := map[key]*agg{}
	m := strings.ToLower(strings.TrimSpace(match))
	for workerType, byPriority := range view {
		for _, group := range byPriority {
			for _, j := range group.IDToJobDetails {
				org := strings.Trim(string(j.OrgID), `"`)
				if m != "" && !strings.Contains(strings.ToLower(org), m) && !strings.Contains(strings.ToLower(j.JobType), m) {
					continue
				}
				seen := map[key]bool{}
				for _, t := range j.Tasks {
					k := key{org, workerType, j.JobType, firstNonEmpty(t.ResourceExpectancy, "unknown"), t.TaskStatus}
					g := groups[k]
					if g == nil {
						g = &agg{}
						groups[k] = g
					}
					g.tasks++
					if !seen[k] {
						seen[k] = true
						g.jobs++
					}
				}
			}
		}
	}
	rows := make([]map[string]any, 0, len(groups))
	queued, running := 0, 0
	for k, g := range groups {
		rows = append(rows, map[string]any{"org_id": k.org, "worker_type": k.workerType, "job_type": k.jobType, "worker_class": k.class, "status": k.status, "tasks": g.tasks, "jobs": g.jobs})
		switch k.status {
		case "QUEUED", "INITIALIZED":
			queued += g.tasks
		case "RUNNING":
			running += g.tasks
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a["org_id"] != b["org_id"] {
			return fmt.Sprint(a["org_id"]) < fmt.Sprint(b["org_id"])
		}
		return fmt.Sprint(a["job_type"], a["worker_class"], a["status"]) < fmt.Sprint(b["job_type"], b["worker_class"], b["status"])
	})
	win, omitted, ok := window(rows, limit, offset, 50, 200, "scheduler groups")
	if !ok {
		return result.NewUnknown(inspectJobsName, fmt.Sprintf("Offset %d is beyond the %d scheduler groups", offset, len(rows)), cx, []string{"offset is past the end of the list"}, result.Options{})
	}
	finding := fmt.Sprintf("%d scheduler group(s): %d task(s) waiting, %d running", len(rows), queued, running)
	d := map[string]any{"state": "scheduler", "groups_total": len(rows), "tasks_waiting": queued, "tasks_running": running, "groups": win}
	limits := []string{
		"this is the scheduler's point-in-time debug view (system administrators only): a snapshot of now, not a history of queue transitions, with no stability promise",
		"it carries no per-organization worker cap: maxRegularComputeWorkerPerOrg and the large-class cap are backend configuration with no route, so they can only be read from the deployment's own settings",
		"worker_class is the class a task needs (REGULAR or LARGE); a QUEUED task has no worker yet",
	}
	return result.Build(inspectJobsName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, Omitted: omitted, NextActions: []string{"inspect-jobs", "inspect-environment"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvState, "debug/jobs", nil, d, finding)}})
}
