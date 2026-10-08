package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"
	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const editJobsName = "edit-jobs"

func init() { Register(editJobsName, editJobs) }

type editJobsInput struct {
	// OlderThanMinutes selects which active jobs are stuck: either longest_running_time_seconds at or past it
	// (a job that has been computing too long), or -- for a job sitting at running=0 -- duration_seconds at or
	// past it (a job that has been queued, never once dispatched to a worker, for too long: Forward's own
	// longest_running_time_seconds cannot see this case, since it only ever measures time actually running).
	// Default 20, matching Forward's own NQE_JOB_TIMEOUT_MINUTES default -- past that, Forward's orchestration
	// layer has already given up on the job even if the worker thread computing it has not, so there is nothing
	// left upstream that will clear it on its own. 0 is a valid threshold (cancel immediately), not "unset".
	OlderThanMinutes *int `json:"older_than_minutes"`
	// NetworkID narrows cancellation to one network; omit to consider every network.
	NetworkID string `json:"network_id"`
	// Limit caps how many jobs one run cancels (default 10), so a single invocation cannot sweep an unbounded
	// amount of work even if OlderThanMinutes is set very low by mistake.
	Limit int  `json:"limit"`
	Apply bool `json:"apply"`
}

// editJobs finds active jobs that have been running past a threshold and cancels them (DELETE
// /api/jobs/{cancelLink}, JobsController.cancelJob) -- the same action Forward's own admin UI and Forward support
// use, instead of restarting the worker pod running the job. Dry run unless apply is true.
//
// This is meant to run unattended (a scheduled check): a job running past Forward's own NQE_JOB_TIMEOUT_MINUTES
// is doomed by Forward's own definition, so canceling it automatically is enforcing that timeout, not overriding
// it. It only ever cancels jobs it found past the threshold; it never touches anything younger.
func editJobs(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in editJobsInput
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
	thresholdMinutes := 20
	if in.OlderThanMinutes != nil {
		thresholdMinutes = *in.OlderThanMinutes
	}
	if thresholdMinutes < 0 {
		return result.Result{}, fmt.Errorf("%w: older_than_minutes must not be negative", ErrInvalidInput)
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 10
	}
	thresholdSeconds := thresholdMinutes * 60

	jobs, _, err := s.Client.Jobs.ListActive(ctx)
	if err != nil {
		return result.NewUnknown(editJobsName, "Active jobs could not be read", cx,
			[]string{"GET /api/jobs/active failed: " + err.Error()}, result.Options{})
	}
	wantNetwork, filterNetwork := parseNetworkID(in.NetworkID)
	var stuck []forward.ActiveJobInfo
	for _, j := range jobs {
		if filterNetwork && j.NetworkID != wantNetwork {
			continue
		}
		stuckRunning := j.LongestRunningTimeSeconds >= thresholdSeconds
		stuckQueued := j.Running == 0 && j.DurationSeconds >= thresholdSeconds
		if stuckRunning || stuckQueued {
			stuck = append(stuck, j)
		}
	}
	mode := result.ModeDryRun
	if in.Apply {
		mode = result.ModeApplied
	}
	if len(stuck) == 0 {
		return result.Build(editJobsName, result.OK, fmt.Sprintf("No active job has run %d+ minutes; nothing is stuck", thresholdMinutes),
			result.Deterministic, cx, result.Options{Mode: mode,
				Evidence:    []result.Evidence{result.NewEvidence(result.EvState, "jobs/active", nil, map[string]any{"active_total": len(jobs), "threshold_minutes": thresholdMinutes}, "")},
				NextActions: []string{"inspect-jobs"}})
	}
	stuck, pageOmitted, _ := window(stuck, 0, 0, limit, limit, "stuck jobs")

	var changes []result.Change
	var canceled, failed []map[string]any
	for _, j := range stuck {
		target := fmt.Sprintf("%s job on network %d", j.JobType, j.NetworkID)
		if j.SnapshotID != nil {
			target += fmt.Sprintf(" snapshot %d", *j.SnapshotID)
		}
		before := map[string]any{"longest_running_time_seconds": j.LongestRunningTimeSeconds, "total_count": j.TotalCount, "cancel_link": j.CancelLink}
		ch := result.Change{Action: "cancel_job", Target: target, Before: before, After: map[string]any{"state": "canceled"}, Reversible: false}
		if !in.Apply {
			changes = append(changes, ch)
			continue
		}
		if _, cerr := s.Client.Jobs.Cancel(ctx, j.CancelLink); cerr != nil {
			failed = append(failed, map[string]any{"target": target, "error": cerr.Error()})
			continue
		}
		ch.Applied = true
		changes = append(changes, ch)
		canceled = append(canceled, map[string]any{"target": target, "ran_minutes": j.LongestRunningTimeSeconds / 60})
	}

	d := map[string]any{"threshold_minutes": thresholdMinutes, "matched": len(stuck), "canceled": canceled, "failed": failed}
	evd := []result.Evidence{result.NewEvidence(result.EvState, "jobs/active", nil, d, "")}

	if !in.Apply {
		return result.Build(editJobsName, result.OK, fmt.Sprintf("Dry run: would cancel %d job(s) running %d+ minutes. Run again with apply=true", len(stuck), thresholdMinutes),
			result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: changes, Omitted: pageOmitted, Evidence: evd, NextActions: []string{"inspect-jobs"}})
	}
	if len(failed) > 0 {
		return result.Build(editJobsName, result.Failed, fmt.Sprintf("Canceled %d of %d stuck job(s); %d failed", len(canceled), len(stuck), len(failed)),
			result.Deterministic, cx, result.Options{Mode: result.ModeApplied, Changes: changes, Omitted: pageOmitted, Evidence: evd, NextActions: []string{"inspect-jobs"}})
	}
	return result.Build(editJobsName, result.OK, fmt.Sprintf("Canceled %d job(s) that had run %d+ minutes", len(canceled), thresholdMinutes),
		result.Deterministic, cx, result.Options{Mode: result.ModeApplied, Changes: changes, Omitted: pageOmitted, Evidence: evd, NextActions: []string{"inspect-jobs"}})
}
