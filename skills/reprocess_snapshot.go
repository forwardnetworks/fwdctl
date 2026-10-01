package skills

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const reprocessSnapshotName = "edit-snapshot-reprocess"

func init() { Register(reprocessSnapshotName, reprocessSnapshot) }

type reprocessSnapshotInput struct {
	NetworkID  string `json:"network_id"`
	SnapshotID string `json:"snapshot_id"`
	Apply      bool   `json:"apply"`
}

// reprocessSnapshot recomputes the derived model of one snapshot from the data it holds. It is for a snapshot whose processing
// failed or that was processed before a Forward upgrade or setting changed; it collects nothing and changes no device. It never
// invalidates without reprocessing, and refuses a snapshot that is still being processed.
func reprocessSnapshot(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in reprocessSnapshotInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if in.NetworkID == "" || in.SnapshotID == "" {
		return result.Result{}, fmt.Errorf("%w: network_id and snapshot_id are required", ErrInvalidInput)
	}
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	sn, err := s.Snapshot(ctx, in.NetworkID, in.SnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	if sn == nil {
		return result.NewUnknown(reprocessSnapshotName, fmt.Sprintf("The network holds no snapshot %s", in.SnapshotID), cx,
			[]string{"no such snapshot in this network (ids are matched exactly); nothing was changed"}, result.Options{NextActions: []string{"inspect-snapshots"}})
	}
	cx = fwd.Context(in.NetworkID, sn)
	mode := result.ModeDryRun
	if in.Apply {
		mode = result.ModeApplied
	}
	ev := func(extra map[string]any) []result.Evidence {
		d := map[string]any{"snapshot_id": in.SnapshotID, "state_before": sn.State, "mode": mode}
		for k, v := range extra {
			d[k] = v
		}
		return []result.Evidence{result.NewEvidence(result.EvState, "reprocessSnapshot", cx.SnapshotID, d, "")}
	}
	next := []string{"inspect-snapshots", "investigate-collection-failure"}
	if sn.State != "PROCESSED" && sn.State != "FAILED" {
		return result.Build(reprocessSnapshotName, result.Failed, fmt.Sprintf("Snapshot %s is %s, not PROCESSED or FAILED; it is not reprocessed while it is in that state", in.SnapshotID, sn.State),
			result.Deterministic, cx, result.Options{Mode: mode, Evidence: ev(nil), NextActions: next,
				Limits: []string{"nothing was changed; wait for the snapshot to finish processing, then run this again"}})
	}
	ch := result.Change{Action: "reprocess", Target: "snapshot " + in.SnapshotID, Before: sn.State, After: "PROCESSED (after reprocessing)", Reversible: false,
		Undo: "none: reprocessing recomputes the derived data from the same collected data, so running it again gives the same result; nothing collected is lost"}
	limits := []string{"the snapshot's derived data (paths, checks, NQE answers) is unavailable while it reprocesses; this skill does not wait for it to finish, check inspect-snapshots"}
	if !in.Apply {
		return result.Build(reprocessSnapshotName, result.OK, fmt.Sprintf("Dry run: would reprocess snapshot %s (now %s). Nothing was changed; run again with apply=true to start it", in.SnapshotID, sn.State),
			result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Evidence: ev(nil), Limits: limits, NextActions: next})
	}
	tr, err := s.ReprocessSnapshot(ctx, in.SnapshotID)
	if err != nil {
		return result.Result{}, fmt.Errorf("starting the reprocess failed, nothing is known to have changed: %w", err)
	}
	ch.Applied = true
	after := ""
	if tr != nil {
		after = tr.State
		ch.After = tr.State
	}
	return result.Build(reprocessSnapshotName, result.OK, fmt.Sprintf("Started reprocessing snapshot %s (%s -> %s)", in.SnapshotID, sn.State, after),
		result.Deterministic, cx, result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(map[string]any{"state_after": after}), Limits: limits, NextActions: next})
}
