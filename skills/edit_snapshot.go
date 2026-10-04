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

const editSnapshotName = "edit-snapshot"

func init() { Register(editSnapshotName, editSnapshot) }

type editSnapshotInput struct {
	NetworkID  string          `json:"network_id"`
	SnapshotID string          `json:"snapshot_id"`
	Action     string          `json:"action"`
	Confirm    string          `json:"confirm"`
	Definition json.RawMessage `json:"definition"`
	Apply      bool            `json:"apply"`
}

// editSnapshot is the one place to change a snapshot or how a network keeps its snapshots: set its note, reprocess or invalidate it, favorite it, delete it, or set the network's
// retention policy. The note and reprocess actions are the retired skills edit-snapshot and edit-snapshot (which stay as aliases); every other action is new. Each is
// a dry run unless apply is true, says what cannot be undone, and the destructive ones (delete, favorite, retention policy) need confirm.
func editSnapshot(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in editSnapshotInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	switch in.Action {
	case "note":
		return annotateSnapshot(ctx, s, raw)
	case "reprocess":
		return reprocessSnapshot(ctx, s, raw)
	case "invalidate", "favorite", "delete":
		return snapshotLifecycle(ctx, s, in)
	case "retention_policy":
		return snapshotRetentionPolicy(ctx, s, in)
	}
	return result.Result{}, fmt.Errorf("%w: action must be note, reprocess, invalidate, favorite, delete or retention_policy", ErrInvalidInput)
}

// snapshotBusy says a snapshot is being worked on now, so it is not changed under Forward.
func snapshotBusy(state string) bool {
	switch strings.ToUpper(state) {
	case "UNPACKING", "PROCESSING", "RESTORING":
		return true
	}
	return false
}

func snapshotLifecycle(ctx context.Context, s *fwd.Session, in editSnapshotInput) (result.Result, error) {
	if in.NetworkID == "" || in.SnapshotID == "" {
		return result.Result{}, fmt.Errorf("%w: network_id and snapshot_id are required", ErrInvalidInput)
	}
	if len(in.Definition) > 0 {
		return result.Result{}, fmt.Errorf("%w: definition belongs to action retention_policy", ErrInvalidInput)
	}
	needsConfirm := in.Action == "delete" || in.Action == "favorite"
	if needsConfirm && in.Apply && in.Confirm != in.SnapshotID {
		return result.Result{}, fmt.Errorf("%w: %s cannot be undone through the API; set confirm to the exact snapshot_id %q to apply", ErrInvalidInput, in.Action, in.SnapshotID)
	}
	if !needsConfirm && in.Confirm != "" {
		return result.Result{}, fmt.Errorf("%w: confirm belongs to delete and favorite", ErrInvalidInput)
	}
	mode := result.ModeDryRun
	if in.Apply {
		mode = result.ModeApplied
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
	before := map[string]any{"snapshot_id": in.SnapshotID, "state": sn.State, "created_at": sn.CreatedAt, "note": nilIfEmpty(sn.Note), "devices": sn.TotalDevices}
	ev := func(extra map[string]any) []result.Evidence {
		d := map[string]any{"action": in.Action, "mode": mode, "before": before}
		for k, v := range extra {
			d[k] = v
		}
		return []result.Evidence{result.NewEvidence(result.EvState, "editSnapshot", cx.SnapshotID, d, "")}
	}
	if snapshotBusy(sn.State) {
		return result.Build(editSnapshotName, result.Failed, fmt.Sprintf("Refused, nothing was changed: snapshot %s is %s, which Forward is working on now", in.SnapshotID, sn.State), result.Deterministic, cx,
			result.Options{Mode: mode, Evidence: ev(nil), Limits: []string{"wait for it to finish (inspect-snapshots shows its state), then run this again"}})
	}
	var ch result.Change
	var limits []string
	switch in.Action {
	case "invalidate":
		ch = result.Change{Action: "invalidate_snapshot", Target: "snapshot " + in.SnapshotID, Before: sn.State, After: "UNPROCESSED", Reversible: true, Undo: "reprocess it (action reprocess): it recomputes the same derived data from the collected data"}
		limits = append(limits, "invalidating empties the snapshot's derived model (paths, checks, NQE answers) until it is reprocessed; nothing collected is lost, and Forward does not reprocess an invalidated snapshot by itself")
	case "favorite":
		ch = result.Change{Action: "favorite_snapshot", Target: "snapshot " + in.SnapshotID, Before: false, After: true, Reversible: false, Undo: "none: the Forward API has no call to remove a favorite (the UI does)"}
		limits = append(limits, "a favorite snapshot is never thinned by the retention policy and is kept; the SDK can set it but not clear it")
	case "delete":
		ch = result.Change{Action: "delete_snapshot", Target: "snapshot " + in.SnapshotID, Before: before, After: nil, Reversible: false, Undo: "none: a deleted snapshot and its model are gone (a backup restore may bring one back if it was backed up)"}
		limits = append(limits, "deleting a snapshot removes it and its derived data for good; other snapshots are untouched. Forward's own answer decides whether a snapshot that others were predicted from can be deleted; this skill does not check")
		if sn.Note != "" {
			limits = append(limits, "the snapshot carries a note: "+sn.Note)
		}
	}
	if !in.Apply {
		return result.Build(editSnapshotName, result.OK, fmt.Sprintf("Dry run: would %s snapshot %s (%s). Nothing was changed; run again with apply=true%s", strings.ReplaceAll(in.Action, "_", " "), in.SnapshotID, sn.State,
			map[bool]string{true: fmt.Sprintf(" and confirm=%q", in.SnapshotID), false: ""}[needsConfirm]), result.Deterministic, cx,
			result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Evidence: ev(nil), Limits: limits, NextActions: []string{"inspect-snapshots"}})
	}
	var aerr error
	switch in.Action {
	case "invalidate":
		_, _, aerr = s.Client.Snapshots.Invalidate(ctx, in.SnapshotID)
	case "favorite":
		_, aerr = s.Client.Snapshots.Favorite(ctx, in.SnapshotID)
	case "delete":
		_, aerr = s.Client.Snapshots.Delete(ctx, in.SnapshotID)
	}
	if aerr != nil {
		return result.Result{}, fmt.Errorf("the %s failed, nothing is known to have changed: %w", in.Action, aerr)
	}
	ch.Applied = true
	now, rerr := s.Snapshot(ctx, in.NetworkID, in.SnapshotID)
	if rerr != nil {
		return result.Result{}, fmt.Errorf("the %s was sent but reading the snapshot back failed, so it is not proven: %w", in.Action, rerr)
	}
	switch {
	case in.Action == "delete" && now != nil:
		return result.Build(editSnapshotName, result.Failed, fmt.Sprintf("Forward accepted the delete but snapshot %s is still listed (%s)", in.SnapshotID, now.State), result.Deterministic, cx,
			result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(nil), Limits: limits})
	case in.Action == "invalidate" && (now == nil || strings.EqualFold(now.State, sn.State) && !strings.EqualFold(sn.State, "UNPROCESSED")):
		return result.Build(editSnapshotName, result.Failed, fmt.Sprintf("Forward accepted the invalidation but snapshot %s is still %s", in.SnapshotID, sn.State), result.Deterministic, cx,
			result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(nil), Limits: limits})
	}
	finding := fmt.Sprintf("%s snapshot %s", map[string]string{"invalidate": "Invalidated", "favorite": "Favorited", "delete": "Deleted"}[in.Action], in.SnapshotID)
	after := map[string]any{}
	if now != nil {
		after["state"] = now.State
	}
	return result.Build(editSnapshotName, result.OK, finding, result.Deterministic, cx,
		result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(map[string]any{"after": after}), Limits: limits, NextActions: []string{"inspect-snapshots"}})
}

// retentionDefinition is the policy body; granularities are Forward's (ALL, ONE_PER_DAY, ONE_PER_TWO_DAYS, ONE_PER_WEEK, ONE_PER_TWO_WEEKS, ONE_PER_MONTH, ONE_PER_QUARTER, NONE).
type retentionDefinition struct {
	Enabled     *bool  `json:"enabled"`
	LastWeek    string `json:"lastWeek"`
	LastMonth   string `json:"lastMonth"`
	LastQuarter string `json:"lastQuarter"`
	LastYear    string `json:"lastYear"`
	Older       string `json:"older"`
}

func snapshotRetentionPolicy(ctx context.Context, s *fwd.Session, in editSnapshotInput) (result.Result, error) {
	if in.NetworkID == "" {
		return result.Result{}, fmt.Errorf("%w: retention_policy needs network_id", ErrInvalidInput)
	}
	if in.SnapshotID != "" {
		return result.Result{}, fmt.Errorf("%w: retention_policy is per network: leave snapshot_id out", ErrInvalidInput)
	}
	if len(in.Definition) == 0 {
		return result.Result{}, fmt.Errorf("%w: retention_policy needs definition: {enabled, lastWeek, lastMonth, lastQuarter, lastYear, older}", ErrInvalidInput)
	}
	var def retentionDefinition
	dec := json.NewDecoder(strings.NewReader(string(in.Definition)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&def); err != nil {
		return result.Result{}, fmt.Errorf("%w: definition: %v (fields: enabled, lastWeek, lastMonth, lastQuarter, lastYear, older)", ErrInvalidInput, err)
	}
	if in.Apply && in.Confirm != in.NetworkID {
		return result.Result{}, fmt.Errorf("%w: the policy decides which snapshots the daily cleanup deletes, which cannot be undone; set confirm to the exact network_id %q to apply", ErrInvalidInput, in.NetworkID)
	}
	mode := result.ModeDryRun
	if in.Apply {
		mode = result.ModeApplied
	}
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	cur, _, err := s.Client.Networks.GetSnapshotRetentionPolicy(ctx, in.NetworkID)
	if err != nil {
		return result.Result{}, err
	}
	want := *cur
	if def.Enabled != nil {
		want.Enabled = *def.Enabled
	}
	for _, f := range []struct {
		from string
		to   *forward.SnapshotRetentionGranularity
	}{{def.LastWeek, &want.LastWeek}, {def.LastMonth, &want.LastMonth}, {def.LastQuarter, &want.LastQuarter}, {def.LastYear, &want.LastYear}, {def.Older, &want.Older}} {
		if f.from != "" {
			*f.to = forward.SnapshotRetentionGranularity(strings.ToUpper(f.from))
		}
	}
	toMap := func(p forward.SnapshotRetentionPolicy) map[string]any {
		return map[string]any{"enabled": p.Enabled, "lastWeek": p.LastWeek, "lastMonth": p.LastMonth, "lastQuarter": p.LastQuarter, "lastYear": p.LastYear, "older": p.Older}
	}
	ch := result.Change{Action: "set_snapshot_retention_policy", Target: "network " + in.NetworkID, Before: toMap(*cur), After: toMap(want), Reversible: false,
		Undo: "set the policy back to the before values: but snapshots a stricter policy already caused the cleanup to delete are gone"}
	limits := []string{
		"on-premises Forward only: the SaaS policy is fixed and Forward answers 404 to a change",
		"Forward's rules: last week must be ALL; last month ONE_PER_DAY, ONE_PER_TWO_DAYS or ONE_PER_WEEK; last quarter ALL, ONE_PER_DAY, ONE_PER_WEEK, ONE_PER_TWO_WEEKS or NONE; last year ONE_PER_DAY, ONE_PER_WEEK, ONE_PER_MONTH or NONE; older those plus ONE_PER_QUARTER. Forward refuses a combination outside these with a 400",
		"the policy is applied by the daily cleanup; a stricter policy deletes snapshots then. inspect-snapshots with retention: true shows what the SAVED policy would delete next, not what this change would",
		"Forward always keeps the 10 newest processed snapshots, favorites, and predictions or forks with the snapshots they derive from"}
	ev := func(extra map[string]any) []result.Evidence {
		d := map[string]any{"action": "retention_policy", "mode": mode, "before": toMap(*cur), "after": toMap(want)}
		for k, v := range extra {
			d[k] = v
		}
		return []result.Evidence{result.NewEvidence(result.EvState, "editSnapshotRetentionPolicy", nil, d, "")}
	}
	if *cur == want {
		return result.Build(editSnapshotName, result.OK, "The network already has that retention policy; nothing to change", result.Deterministic, cx, result.Options{Mode: mode, Evidence: ev(nil), Limits: limits, NextActions: []string{"inspect-snapshots"}})
	}
	if !in.Apply {
		return result.Build(editSnapshotName, result.OK, fmt.Sprintf("Dry run: would change the snapshot retention policy of network %s. Nothing was changed; run again with apply=true and confirm=%q", in.NetworkID, in.NetworkID), result.Deterministic, cx,
			result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Evidence: ev(nil), Limits: limits, NextActions: []string{"inspect-snapshots"}})
	}
	if _, err := s.Client.Networks.SetSnapshotRetentionPolicy(ctx, in.NetworkID, want); err != nil {
		return result.Result{}, fmt.Errorf("the policy was not saved, nothing is known to have changed: %w", err)
	}
	ch.Applied = true
	now, _, rerr := s.Client.Networks.GetSnapshotRetentionPolicy(ctx, in.NetworkID)
	if rerr != nil {
		return result.Result{}, fmt.Errorf("the policy was sent but reading it back failed, so it is not proven: %w", rerr)
	}
	if *now != want {
		return result.Build(editSnapshotName, result.Failed, "Forward accepted the policy but reads back a different one", result.Deterministic, cx,
			result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(map[string]any{"read_back": toMap(*now)}), Limits: limits})
	}
	return result.Build(editSnapshotName, result.OK, fmt.Sprintf("Saved the snapshot retention policy of network %s", in.NetworkID), result.Deterministic, cx,
		result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(nil), Limits: limits, NextActions: []string{"inspect-snapshots"}})
}
