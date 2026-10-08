package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"
	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const inspectJobsName = "inspect-jobs"

func init() { Register(inspectJobsName, inspectJobs) }

type inspectJobsInput struct {
	// State selects which list to read: "active" (default) or "completed".
	State string `json:"state"`
	// NetworkID narrows to one network; omit for every network the login can see jobs for.
	NetworkID string `json:"network_id"`
	// OlderThanMinutes flags (does not filter out) active rows as likely stuck: either running that long (Forward's
	// own orchestration layer has already given up even though the worker may still be computing), or -- a row at
	// running=0 -- queued that long with no worker ever dispatched to it (longest_running_time_seconds cannot see
	// this case, since it only measures time actually running). 20 matches Forward's own NQE_JOB_TIMEOUT_MINUTES
	// default. 0 is a valid threshold (flag everything), not "unset".
	OlderThanMinutes *int   `json:"older_than_minutes"`
	Match            string `json:"match"`
	Limit            int    `json:"limit"`
	Offset           int    `json:"offset"`
}

// inspectJobs lists Forward's own top-level backend jobs (NQE executions, snapshot processing, device-processing
// batches and similar) -- the same admin surface Forward's GUI (System Overview > Jobs) and Forward support use to
// see what is running or finished, and to find the one job to cancel instead of restarting a worker. Requires
// ADMINISTER_SYSTEM, which an organization administrator already has.
func inspectJobs(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in inspectJobsInput
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil {
			return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
	}
	cx := result.Context{Scope: "account", State: "current"}
	if strings.TrimSpace(in.NetworkID) != "" {
		if _, err := strconv.ParseInt(strings.TrimSpace(in.NetworkID), 10, 64); err != nil {
			return result.Result{}, fmt.Errorf("%w: network_id %q is not a number", ErrInvalidInput, in.NetworkID)
		}
	}
	state := strings.ToLower(strings.TrimSpace(in.State))
	if state == "" {
		state = "active"
	}
	thresholdMinutes := 20
	if in.OlderThanMinutes != nil {
		thresholdMinutes = *in.OlderThanMinutes
	}
	thresholdSeconds := thresholdMinutes * 60

	switch state {
	case "active":
		jobs, _, err := s.Client.Jobs.ListActive(ctx)
		if err != nil {
			return result.NewUnknown(inspectJobsName, "Active jobs could not be read", cx,
				[]string{"GET /api/jobs/active failed: " + err.Error()}, result.Options{})
		}
		rows, stuck := activeJobRows(jobs, in.NetworkID, in.Match, thresholdSeconds)
		return buildJobsResult(inspectJobsName, cx, "active", rows, stuck, len(jobs), in.Limit, in.Offset, thresholdSeconds)
	case "completed":
		jobs, _, err := s.Client.Jobs.ListCompleted(ctx)
		if err != nil {
			return result.NewUnknown(inspectJobsName, "Completed jobs could not be read", cx,
				[]string{"GET /api/jobs/completed failed: " + err.Error()}, result.Options{})
		}
		rows := completedJobRows(jobs, in.NetworkID, in.Match)
		return buildJobsResult(inspectJobsName, cx, "completed", rows, 0, len(jobs), in.Limit, in.Offset, 0)
	default:
		return result.Result{}, fmt.Errorf("%w: state must be \"active\" or \"completed\", got %q", ErrInvalidInput, in.State)
	}
}

func activeJobRows(jobs []forward.ActiveJobInfo, networkID, match string, thresholdSeconds int) ([]map[string]any, int) {
	var rows []map[string]any
	stuck := 0
	m := strings.ToLower(strings.TrimSpace(match))
	wantNetwork, filterNetwork := parseNetworkID(networkID)
	for _, j := range jobs {
		if filterNetwork && j.NetworkID != wantNetwork {
			continue
		}
		if m != "" && !strings.Contains(strings.ToLower(j.JobType), m) && !strings.Contains(strings.ToLower(j.OrgName), m) {
			continue
		}
		likelyStuck := j.LongestRunningTimeSeconds >= thresholdSeconds || (j.Running == 0 && j.DurationSeconds >= thresholdSeconds)
		if likelyStuck {
			stuck++
		}
		row := map[string]any{
			"job_type": j.JobType, "org_name": j.OrgName, "network_id": j.NetworkID,
			"creation_time": j.CreationTime.Time, "queued": j.Queued, "running": j.Running,
			"longest_queued_time_seconds": j.LongestQueuedTimeSeconds, "longest_running_time_seconds": j.LongestRunningTimeSeconds,
			"duration_seconds": j.DurationSeconds, "total_count": j.TotalCount, "cancel_link": j.CancelLink,
			"likely_stuck": likelyStuck,
		}
		if j.SnapshotID != nil {
			row["snapshot_id"] = *j.SnapshotID
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(a, b int) bool {
		return rows[a]["longest_running_time_seconds"].(int) > rows[b]["longest_running_time_seconds"].(int)
	})
	return rows, stuck
}

func completedJobRows(jobs []forward.CompletedJobInfo, networkID, match string) []map[string]any {
	var rows []map[string]any
	m := strings.ToLower(strings.TrimSpace(match))
	wantNetwork, filterNetwork := parseNetworkID(networkID)
	for _, j := range jobs {
		if filterNetwork && j.NetworkID != wantNetwork {
			continue
		}
		if m != "" && !strings.Contains(strings.ToLower(j.JobType), m) && !strings.Contains(strings.ToLower(j.OrgName), m) {
			continue
		}
		row := map[string]any{
			"job_type": j.JobType, "org_name": j.OrgName, "network_id": j.NetworkID, "state": j.State,
			"earliest_start_time": j.EarliestStartTime.Time, "latest_end_time": j.LatestEndTime.Time,
			"longest_running_time_seconds": j.LongestRunningTimeSeconds, "duration_seconds": j.DurationSeconds, "count": j.Count,
		}
		if j.SnapshotID != nil {
			row["snapshot_id"] = *j.SnapshotID
		}
		rows = append(rows, row)
	}
	return rows
}

func buildJobsResult(skill string, cx result.Context, state string, rows []map[string]any, stuck, total, limit, offset, thresholdSeconds int) (result.Result, error) {
	win, omitted, ok := window(rows, limit, offset, 50, 200, state+" jobs")
	if !ok {
		return result.NewUnknown(skill, fmt.Sprintf("Offset %d is beyond the %d %s jobs", offset, len(rows), state), cx,
			[]string{"offset is past the end of the list"}, result.Options{})
	}
	finding := fmt.Sprintf("%d %s job row(s)", len(rows), state)
	if state == "active" {
		finding = fmt.Sprintf("%d active job row(s), %d likely stuck (running %d+ minutes)", len(rows), stuck, thresholdSeconds/60)
	}
	d := map[string]any{"state": state, "total": len(rows), "jobs": win}
	if state == "active" {
		d["likely_stuck"] = stuck
		d["threshold_minutes"] = thresholdSeconds / 60
	}
	var limits []string
	if state == "active" {
		limits = append(limits, "likely_stuck is a heuristic (longest_running_time_seconds past the threshold); it is not Forward's own judgement -- a job can legitimately run long")
	}
	next := []string{"inspect-environment"}
	if state == "active" && stuck > 0 {
		next = append(next, "edit-jobs")
	}
	return result.Build(skill, result.OK, finding, result.Deterministic, cx, result.Options{
		Limits: limits, Omitted: omitted, NextActions: next,
		Evidence: []result.Evidence{result.NewEvidence(result.EvState, "jobs/"+state, nil, d, finding)},
	})
}

// parseNetworkID reports the parsed id and whether a filter was actually requested; an empty or unparsable input
// means "no filter" rather than silently matching network 0.
func parseNetworkID(s string) (id int64, filter bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}
