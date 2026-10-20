package skills

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// snapshotCancel is edit-snapshot action cancel. Forward has NO snapshot-level cancel route: the only server-side stop is cancelling backend jobs (DELETE /api/jobs/{cancelLink}), which needs ADMINISTER_SYSTEM, as does the
// list of active jobs it takes the links from. So this is an explicit admin-gated action on the active job rows that carry this snapshot's id: a dry run lists them, apply needs confirm equal to the snapshot id, the result reads the
// rows back and says what it did not cover. It never runs on its own (waits do not cancel).
func snapshotCancel(ctx context.Context, s *fwd.Session, in editSnapshotInput) (result.Result, error) {
	if in.NetworkID == "" || in.SnapshotID == "" {
		return result.Result{}, fmt.Errorf("%w: network_id and snapshot_id are required", ErrInvalidInput)
	}
	if len(in.Definition) > 0 {
		return result.Result{}, fmt.Errorf("%w: definition belongs to action retention_policy, export or import", ErrInvalidInput)
	}
	sid, perr := strconv.ParseInt(strings.TrimSpace(in.SnapshotID), 10, 64)
	if perr != nil || sid <= 0 {
		return result.Result{}, fmt.Errorf("%w: snapshot_id %q is not a numeric snapshot id, so it cannot be matched to a job row", ErrInvalidInput, in.SnapshotID)
	}
	if in.Apply && in.Confirm != in.SnapshotID {
		return result.Result{}, fmt.Errorf("%w: cancel stops the running jobs of snapshot %s, including other users' queries, and cannot be undone; set confirm to the exact snapshot_id %q to apply", ErrInvalidInput, in.SnapshotID, in.SnapshotID)
	}
	if !in.Apply && in.Confirm != "" {
		return result.Result{}, fmt.Errorf("%w: confirm belongs to apply", ErrInvalidInput)
	}
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	sn, err := s.Snapshot(ctx, in.NetworkID, in.SnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	if sn == nil {
		return result.NewUnknown(editSnapshotName, fmt.Sprintf("The network holds no snapshot %s", in.SnapshotID), cx,
			[]string{"no such snapshot in this network (ids are matched exactly); nothing was changed"}, result.Options{NextActions: []string{"inspect-snapshots"}})
	}
	cx = fwd.Context(in.NetworkID, sn)
	rows, matchedRows, err := snapshotJobRows(ctx, s, sid)
	if err != nil {
		reason := "the list of active jobs could not be read: " + err.Error()
		if st := fwd.Status(err); st == 401 || st == 403 {
			reason = "Forward refused the list of active jobs (" + strconv.Itoa(st) + "): cancelling jobs needs an ADMINISTER_SYSTEM credential (a system administrator, and on some deployments an organization administrator), and an ordinary user cannot; " + err.Error()
		}
		return result.NewUnknown(editSnapshotName, "Cannot cancel: the active jobs of snapshot "+in.SnapshotID+" could not be read", cx, []string{reason, "nothing was changed"}, result.Options{NextActions: []string{"inspect-jobs"}})
	}
	limits := []string{
		"Forward has no snapshot-level cancel: this cancels the ACTIVE JOB ROWS that carry this snapshot's id (a predicted snapshot's Protocol Modeling is one), through the jobs routes, which need ADMINISTER_SYSTEM",
		"it cancels EVERY top-level job of the snapshot, including other users' queries, diffs and path searches (a grouped row's link cancels all of its type on the snapshot), and shared work is shared: cancelling a base snapshot's job can fail it for every other prediction waiting on it",
		"it does NOT cover the per-device processing of an import, a reprocess or a collection (those rows carry no snapshot id), so an import can keep parsing after a cancel; a collection is stopped with the collector task stop, and a snapshot still waiting in the processing throttle queue is removed by edit-snapshot action invalidate",
		"the stop is cooperative: Protocol Modeling checks for cancellation between path-selection prefixes, route-propagation seeds and DAG node creation, and some steps run to their end, so expect a delay, not a thread interrupt",
		"waits (fwdctl wait, edit-change-set run, verify-change) never cancel by themselves: this is the explicit action",
	}
	rowDetail := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		rowDetail = append(rowDetail, map[string]any{"job_type": r.JobType, "organization": r.OrgName, "queued": r.Queued, "running": r.Running, "seconds": r.DurationSeconds, "rows_in_group": r.TotalCount})
	}
	if matchedRows == 0 {
		return result.NewUnknown(editSnapshotName, fmt.Sprintf("Snapshot %s has no active job row to cancel", in.SnapshotID), cx,
			append([]string{"no active job of this snapshot is listed: it may have finished, be per-device import or reprocess work that carries no snapshot id, or be waiting in the processing throttle queue (invalidate removes it); this is not a successful cancel"}, limits...),
			result.Options{NextActions: []string{"inspect-snapshots", "inspect-jobs"}})
	}
	types := map[string]int{}
	for _, r := range rows {
		types[r.JobType]++
	}
	names := make([]string, 0, len(types))
	for t, n := range types {
		names = append(names, fmt.Sprintf("%s (%d)", t, n))
	}
	sort.Strings(names)
	ch := result.Change{Action: "cancel_snapshot_jobs", Target: "active jobs of snapshot " + in.SnapshotID, Before: rowDetail, Reversible: false,
		Undo: "none: cancelled jobs are not resumed; reprocess the snapshot or run the prediction again"}
	ev := func(mode string, extra map[string]any) []result.Evidence {
		d := map[string]any{"mode": mode, "snapshot_id": in.SnapshotID, "state": sn.State, "job_rows": rowDetail}
		for k, v := range extra {
			d[k] = v
		}
		return []result.Evidence{result.NewEvidence(result.EvState, "cancelSnapshotJobs", cx.SnapshotID, d, "")}
	}
	if !in.Apply {
		return result.Build(editSnapshotName, result.OK, fmt.Sprintf("Dry run: would cancel %d active job row(s) of snapshot %s (%s). Nothing was changed; run again with apply=true and confirm=%q", matchedRows, in.SnapshotID, strings.Join(names, ", "), in.SnapshotID),
			result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Evidence: ev(result.ModeDryRun, nil), Limits: limits, NextActions: []string{"inspect-jobs"}})
	}
	res, cerr := s.Client.Jobs.CancelForSnapshot(ctx, in.SnapshotID)
	ch.Applied = true
	if res != nil {
		ch.After = map[string]any{"matched": res.Matched, "canceled": res.Canceled, "already_done": res.AlreadyDone}
	}
	_, still, rerr := snapshotJobRows(ctx, s, sid)
	extra := map[string]any{}
	if res != nil {
		extra["matched"], extra["canceled"], extra["already_done"] = res.Matched, res.Canceled, res.AlreadyDone
	}
	if rerr == nil {
		extra["rows_still_listed"] = still
	}
	switch {
	case cerr != nil:
		return result.Build(editSnapshotName, result.Failed, fmt.Sprintf("Cancel of snapshot %s did not complete for every row: %v", in.SnapshotID, cerr), result.Deterministic, cx,
			result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(result.ModeApplied, extra), Limits: append(limits, "rows that did cancel stay cancelled; read inspect-jobs to see what is left"), NextActions: []string{"inspect-jobs"}})
	case rerr != nil:
		return result.Build(editSnapshotName, result.OK, fmt.Sprintf("Cancel sent for %d job row(s) of snapshot %s; the read-back of the active jobs failed (%v), so whether they stopped is not known", res.Canceled, in.SnapshotID, rerr), result.Deterministic, cx,
			result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(result.ModeApplied, extra), Limits: limits, NextActions: []string{"inspect-jobs"}})
	case still > 0:
		return result.Build(editSnapshotName, result.OK, fmt.Sprintf("Cancel sent for %d job row(s) of snapshot %s; %d row(s) are still listed (the stop is cooperative and some steps run to their end): read inspect-jobs again before relying on it", res.Canceled, in.SnapshotID, still), result.Deterministic, cx,
			result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(result.ModeApplied, extra), Limits: limits, NextActions: []string{"inspect-jobs"}})
	}
	return result.Build(editSnapshotName, result.OK, fmt.Sprintf("Cancelled %d job row(s) of snapshot %s (%d already done); no active job of the snapshot is listed now", res.Canceled, in.SnapshotID, res.AlreadyDone), result.Deterministic, cx,
		result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(result.ModeApplied, extra), Limits: limits, NextActions: []string{"inspect-snapshots"}})
}

// snapshotJobRows reads the active jobs and returns the rows that carry the snapshot id, and how many there are.
func snapshotJobRows(ctx context.Context, s *fwd.Session, snapshotID int64) ([]forward.ActiveJobInfo, int, error) {
	all, _, err := s.Client.Jobs.ListActive(ctx)
	if err != nil {
		return nil, 0, err
	}
	var rows []forward.ActiveJobInfo
	for _, r := range all {
		if r.SnapshotID != nil && *r.SnapshotID == snapshotID {
			rows = append(rows, r)
		}
	}
	return rows, len(rows), nil
}
