package skills

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const annotateSnapshotName = "edit-snapshot-note"

func init() { Register(annotateSnapshotName, annotateSnapshot) }

const maxNoteLen = 1000

type annotateSnapshotInput struct {
	NetworkID  string  `json:"network_id"`
	SnapshotID string  `json:"snapshot_id"`
	Note       *string `json:"note"`
	Apply      bool    `json:"apply"`
}

// annotateSnapshot sets the note on one snapshot, for example "before change X". It reads the current note first, so the dry run
// shows what would be replaced and the applied result carries the value to put back. It never sets a favourite: the SDK has no
// call to undo one.
func annotateSnapshot(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in annotateSnapshotInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if in.SnapshotID == "" || in.Note == nil {
		return result.Result{}, fmt.Errorf("%w: snapshot_id and note are required (note may be empty to clear it)", ErrInvalidInput)
	}
	if len(*in.Note) > maxNoteLen {
		return result.Result{}, fmt.Errorf("%w: note is %d bytes; at most %d", ErrInvalidInput, len(*in.Note), maxNoteLen)
	}
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	sn, err := s.Snapshot(ctx, in.NetworkID, in.SnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	if sn == nil {
		return result.NewUnknown(annotateSnapshotName, fmt.Sprintf("The network holds no snapshot %s", in.SnapshotID), cx,
			[]string{"no such snapshot in this network (ids are matched exactly); nothing was changed"}, result.Options{NextActions: []string{"inspect-snapshots"}})
	}
	cx = fwd.Context(in.NetworkID, sn)
	target := "snapshot " + in.SnapshotID
	prior, want := sn.Note, *in.Note
	ch := result.Change{Action: "set_note", Target: target, Before: prior, After: want, Reversible: true,
		Undo: fmt.Sprintf("run edit-snapshot-note again on snapshot %s with note %q and apply=true", in.SnapshotID, prior)}
	ev := func(mode string, extra map[string]any) []result.Evidence {
		d := map[string]any{"snapshot_id": in.SnapshotID, "note_before": prior, "note_requested": want, "mode": mode}
		for k, v := range extra {
			d[k] = v
		}
		return []result.Evidence{result.NewEvidence(result.EvState, "annotateSnapshot", cx.SnapshotID, d, "")}
	}
	next := []string{"inspect-snapshots"}
	if prior == want {
		mode := result.ModeDryRun
		if in.Apply {
			mode = result.ModeApplied
		}
		return result.Build(annotateSnapshotName, result.OK, fmt.Sprintf("Snapshot %s already carries that note; nothing to change", in.SnapshotID),
			result.Deterministic, cx, result.Options{Mode: mode, Evidence: ev(mode, nil), NextActions: next})
	}
	if !in.Apply {
		return result.Build(annotateSnapshotName, result.OK, fmt.Sprintf("Dry run: would replace the note on snapshot %s (%q -> %q). Nothing was changed; run again with apply=true to make it", in.SnapshotID, prior, want),
			result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Evidence: ev(result.ModeDryRun, nil), NextActions: next})
	}
	updated, err := s.SetSnapshotNote(ctx, in.SnapshotID, want)
	if err != nil {
		return result.Result{}, fmt.Errorf("setting the note failed, nothing is known to have changed: %w", err)
	}
	ch.Applied = true
	if updated == nil || updated.Note != want {
		got := ""
		if updated != nil {
			got = updated.Note
		}
		return result.Build(annotateSnapshotName, result.Failed, fmt.Sprintf("Forward accepted the change but snapshot %s now carries %q, not the requested note", in.SnapshotID, got),
			result.Deterministic, cx, result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(result.ModeApplied, map[string]any{"note_after": got}),
				Limits: []string{"the note Forward returned differs from the one sent; read the snapshot to see its state"}})
	}
	return result.Build(annotateSnapshotName, result.OK, fmt.Sprintf("Set the note on snapshot %s (was %q)", in.SnapshotID, prior),
		result.Deterministic, cx, result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(result.ModeApplied, map[string]any{"note_after": updated.Note}), NextActions: next})
}
