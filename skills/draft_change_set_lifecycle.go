package skills

import (
	"context"
	"fmt"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// changeSetLifecycle is edit-change-set on an EXISTING change set (change_set_id with lifecycle): rename or describe it, copy it, delete it, throw away one device's uncommitted edits, or restore an earlier version.
// Nothing here reaches a device. A dry run reads the change set and says what would change; apply does it; delete needs confirm equal to the change set's id. A predicted snapshot a change set already made is not
// touched by any of these: it stays when the change set is deleted.
func changeSetLifecycle(ctx context.Context, s *fwd.Session, in draftChangeSetInput) (result.Result, error) {
	l := in.Lifecycle
	switch {
	case l == nil || in.ChangeSetID == "":
		return result.Result{}, fmt.Errorf("%w: lifecycle (an op) and change_set_id go together", ErrInvalidInput)
	case len(in.Devices) > 0 || len(in.BGP) > 0 || len(in.Firewall) > 0 || in.Run || in.SnapshotID != "":
		return result.Result{}, fmt.Errorf("%w: lifecycle works on an existing change set; it does not combine with devices, bgp_advertisements, firewall_rules, snapshot_id or run (create another change set to stage content)", ErrInvalidInput)
	}
	op := strings.ToLower(strings.TrimSpace(l.Op))
	if op == "add_check" || op == "edit_check" || op == "remove_check" {
		return changeSetCheck(ctx, s, in, op)
	}
	switch op {
	case "rename":
		if strings.TrimSpace(l.Name) == "" && strings.TrimSpace(in.Description) == "" {
			return result.Result{}, fmt.Errorf("%w: rename needs lifecycle.name and/or description", ErrInvalidInput)
		}
	case "duplicate", "delete", "predict":
	case "discard_device":
		if strings.TrimSpace(l.Device) == "" {
			return result.Result{}, fmt.Errorf("%w: discard_device needs lifecycle.device", ErrInvalidInput)
		}
	case "restore_commit":
		if strings.TrimSpace(l.CommitID) == "" {
			return result.Result{}, fmt.Errorf("%w: restore_commit needs lifecycle.commit_id (the versions are listed by verify-change view describe)", ErrInvalidInput)
		}
	case "commit":
	case "edit_commit_note":
		if strings.TrimSpace(l.CommitID) == "" || strings.TrimSpace(l.Note) == "" {
			return result.Result{}, fmt.Errorf("%w: edit_commit_note needs lifecycle.commit_id and lifecycle.note", ErrInvalidInput)
		}
	case "delete_commit", "restore_commit_as_new":
		if strings.TrimSpace(l.CommitID) == "" {
			return result.Result{}, fmt.Errorf("%w: %s needs lifecycle.commit_id (the versions are listed by verify-change view history)", ErrInvalidInput, op)
		}
	case "duplicate_to_network":
		if strings.TrimSpace(l.TargetNetworkID) == "" {
			return result.Result{}, fmt.Errorf("%w: duplicate_to_network needs lifecycle.target_network_id", ErrInvalidInput)
		}
	case "add_tags", "remove_tags":
		if len(l.Tags) == 0 {
			return result.Result{}, fmt.Errorf("%w: %s needs lifecycle.tags", ErrInvalidInput, op)
		}
	case "clear_tags":
	case "move_to_directory":
		if d := strings.TrimSpace(l.Directory); d == "" || !strings.HasPrefix(d, "/") || strings.HasSuffix(d, "/") && d != "/" || strings.Contains(d, "//") {
			return result.Result{}, fmt.Errorf("%w: move_to_directory needs lifecycle.directory as a path such as /team/wan (no trailing or doubled slash; the directories are listed by verify-change view history)", ErrInvalidInput)
		}
	default:
		return result.Result{}, fmt.Errorf("%w: lifecycle.op is rename, duplicate, duplicate_to_network, delete, discard_device, commit, edit_commit_note, delete_commit, restore_commit, restore_commit_as_new, add_tags, remove_tags, clear_tags, move_to_directory, predict, add_check, edit_check or remove_check", ErrInvalidInput)
	}
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	list, err := s.ChangeSets(ctx, in.NetworkID)
	if err != nil {
		if r, ok := denialResult(draftChangeSetName, err, cx, "listing the network's change sets"); ok {
			return r, nil
		}
		return result.Result{}, err
	}
	var cs *forward.ChangeSet
	for i := range list {
		if string(list[i].ID) == in.ChangeSetID {
			cs = &list[i]
		}
	}
	if cs == nil {
		return result.NewUnknown(draftChangeSetName, fmt.Sprintf("Change set %s is not in network %s", in.ChangeSetID, in.NetworkID), cx,
			[]string{"no change set with that id was listed; nothing was changed (verify-change view list shows the ids)"}, result.Options{NextActions: []string{"verify-change"}})
	}
	id := in.ChangeSetID
	before := map[string]any{"change_set_id": id, "name": cs.Name, "base_snapshot_id": string(cs.SnapshotID)}
	ch := result.Change{Target: "change set " + id + " (" + cs.Name + ")", Before: before, Reversible: true}
	var do func() (any, error)
	var verb string
	switch op {
	case "rename":
		patch := forward.ChangeSetPatch{}
		after := map[string]any{}
		if n := strings.TrimSpace(l.Name); n != "" {
			patch.Name, after["name"] = &n, n
		}
		if d := strings.TrimSpace(in.Description); d != "" {
			patch.Description, after["description"] = &d, d
		}
		ch.Action, ch.After, verb = "update_change_set", after, "rename or describe"
		ch.Undo = fmt.Sprintf("rename it back to %q (lifecycle rename)", cs.Name)
		do = func() (any, error) {
			_, _, e := s.Client.Predict.UpdateChangeSet(ctx, in.NetworkID, id, patch)
			return after, e
		}
	case "duplicate":
		opts := forward.DuplicateChangeSetOptions{TargetName: strings.TrimSpace(l.Name)}
		ch.Action, verb = "duplicate_change_set", "copy"
		ch.After = map[string]any{"name": firstNonEmpty(opts.TargetName, "(Forward picks the name)"), "base_snapshot_id": string(cs.SnapshotID)}
		ch.Undo = "delete the copy (lifecycle delete on its id)"
		do = func() (any, error) {
			c, _, e := s.Client.Predict.DuplicateChangeSet(ctx, in.NetworkID, id, opts)
			if c != nil {
				return map[string]any{"new_change_set_id": string(c.ID), "name": c.Name}, e
			}
			return nil, e
		}
	case "delete":
		ch.Action, verb, ch.Reversible = "delete_change_set", "delete", false
		ch.Undo = "none: the change set and its versions are gone (duplicate it first to keep a copy); predicted snapshots it made stay"
		do = func() (any, error) { return nil, s.DeleteChangeSet(ctx, in.NetworkID, id) }
	case "discard_device":
		ch.Action, verb = "discard_draft_device", "throw away the uncommitted edits of "+l.Device+" in"
		ch.Target += ", device " + l.Device
		ch.Reversible, ch.Undo = false, "none: uncommitted edits are not kept (a committed version can be restored with restore_commit)"
		do = func() (any, error) {
			_, e := s.Client.Predict.DiscardDraftDevice(ctx, in.NetworkID, id, l.Device)
			return nil, e
		}
	case "predict":
		ch.Action, verb = "start_prediction", "start a prediction of"
		ch.Reversible = false
		ch.Undo = "none: a started prediction leaves a predicted snapshot that these skills cannot delete (edit-jobs cancels a wedged job past a threshold, with ADMINISTER_SYSTEM)"
		do = func() (any, error) {
			known := map[string]bool{}
			if prev, perr := s.PredictedSnapshots(ctx, in.NetworkID, id); perr == nil {
				for _, q := range prev {
					known[string(q.ID)] = true
				}
			}
			p, _, e := s.Client.Predict.Run(ctx, in.NetworkID, id, "fwdctl")
			if p == nil {
				return nil, e
			}
			state := p.State
			if now, perr := s.PredictedSnapshots(ctx, in.NetworkID, id); perr == nil {
				for _, q := range now {
					if q.ID == p.ID && q.State != "" {
						state = q.State
					}
				}
			}
			if state == "" {
				state = "not reported"
			}
			return map[string]any{"predicted_snapshot_id": string(p.ID), "state": state, "new_prediction": !known[string(p.ID)]}, e
		}
	case "commit":
		ch.Action, verb = "commit_change_set", "save the draft as a new version of"
		ch.After = map[string]any{"note": firstNonEmpty(strings.TrimSpace(l.Note), "(none)")}
		ch.Undo = "restore the previous version (restore_commit); versions are kept, so this loses nothing"
		do = func() (any, error) {
			_, e := s.Client.Predict.Commit(ctx, in.NetworkID, id, strings.TrimSpace(l.Note))
			return nil, e
		}
	case "edit_commit_note":
		ch.Action, verb = "edit_commit_note", "change the note of version "+l.CommitID+" of"
		ch.After = map[string]any{"commit_id": l.CommitID, "note": strings.TrimSpace(l.Note)}
		ch.Undo = "edit the note again; the old note is not kept by Forward, so it is not in this result unless you read it first (verify-change view history)"
		do = func() (any, error) {
			m, _, e := s.Client.Predict.EditCommitNote(ctx, in.NetworkID, id, l.CommitID, strings.TrimSpace(l.Note))
			return m, e
		}
	case "delete_commit":
		ch.Action, verb, ch.Reversible = "delete_commit", "delete version "+l.CommitID+" of", false
		ch.Undo = "none: the version is gone (restore_commit_as_new on another version keeps a copy before deleting)"
		do = func() (any, error) {
			_, e := s.Client.Predict.DeleteCommit(ctx, in.NetworkID, id, l.CommitID)
			return nil, e
		}
	case "restore_commit_as_new":
		ch.Action, verb = "restore_commit_as_new", "copy version "+l.CommitID+" of"
		ch.After = map[string]any{"commit_id": l.CommitID, "new_name": firstNonEmpty(strings.TrimSpace(l.Name), "(Forward picks the name)"), "base_snapshot_id": firstNonEmpty(l.BaseSnapshotID, string(cs.SnapshotID))}
		ch.Undo = "delete the new change set (lifecycle delete on its id)"
		do = func() (any, error) {
			c, _, e := s.Client.Predict.RestoreCommitAsNew(ctx, in.NetworkID, id, l.CommitID, strings.TrimSpace(l.Name), l.BaseSnapshotID)
			if c != nil {
				return map[string]any{"new_change_set_id": string(c.ID), "name": c.Name}, e
			}
			return nil, e
		}
	case "duplicate_to_network":
		opts := forward.DuplicateChangeSetOptions{TargetName: strings.TrimSpace(l.Name), TargetBaseSnapshotID: l.BaseSnapshotID}
		ch.Action, verb = "duplicate_change_set_to_network", "copy to network "+l.TargetNetworkID+" the change set"
		ch.After = map[string]any{"target_network_id": l.TargetNetworkID, "name": firstNonEmpty(opts.TargetName, "(Forward picks the name)"), "base_snapshot_id": firstNonEmpty(opts.TargetBaseSnapshotID, "(the target network's latest)")}
		ch.Undo = "delete the copy in the target network (lifecycle delete there, on its id)"
		do = func() (any, error) {
			c, _, e := s.Client.Predict.DuplicateChangeSetToNetwork(ctx, in.NetworkID, id, l.TargetNetworkID, opts)
			if c != nil {
				return map[string]any{"new_change_set_id": string(c.ID), "network_id": l.TargetNetworkID, "name": c.Name}, e
			}
			return nil, e
		}
	case "add_tags", "remove_tags", "clear_tags":
		act := map[string]forward.ChangeSetTagAction{"add_tags": forward.ChangeSetTagsAdd, "remove_tags": forward.ChangeSetTagsRemove, "clear_tags": forward.ChangeSetTagsRemoveAll}[op]
		ch.Action, verb = "tag_change_set", map[string]string{"add_tags": "add tags to", "remove_tags": "remove tags from", "clear_tags": "remove every tag from"}[op]
		ch.After = map[string]any{"tags_" + strings.TrimSuffix(op, "_tags"): l.Tags}
		ch.Undo = "the opposite op (add_tags / remove_tags); clear_tags is undone by add_tags with the tags it showed before"
		before["tags"] = cs.Tags
		do = func() (any, error) {
			_, e := s.Client.Predict.PatchChangeSetsTags(ctx, in.NetworkID, []string{id}, act, l.Tags)
			return nil, e
		}
	case "move_to_directory":
		ch.Action, verb = "move_change_set_to_directory", "file under "+l.Directory+" the change set"
		ch.After = map[string]any{"directory": l.Directory}
		ch.Undo = "move it back (move_to_directory with its earlier directory, shown by verify-change view history)"
		do = func() (any, error) {
			_, e := s.Client.Predict.MoveChangeSetToDirectory(ctx, in.NetworkID, l.Directory, id)
			return nil, e
		}
	case "restore_commit":
		ch.Action, verb = "restore_commit", "restore version "+l.CommitID+" as the current content of"
		ch.After = map[string]any{"commit_id": l.CommitID}
		ch.Undo = "restore the version that was current before (the earlier versions stay in the history); an uncommitted draft is committed first as \"Draft changes auto-saved\""
		do = func() (any, error) {
			m, _, e := s.Client.Predict.RestoreCommit(ctx, in.NetworkID, id, l.CommitID)
			return m, e
		}
	}
	limits := []string{"nothing here reaches a device; a change set only feeds Predict", "predicted snapshots a change set already made are not touched here; edit-snapshot action delete removes one (Forward decides whether it allows it)"}
	need := op == "delete"
	confirmWith := id
	if op == "delete_commit" {
		need, confirmWith = true, l.CommitID
	}
	if !in.Apply {
		msg := fmt.Sprintf("Dry run: would %s change set %s (%s). Nothing was changed; run again with apply=true", verb, id, cs.Name)
		if need {
			msg += fmt.Sprintf(" and confirm=%q", confirmWith)
		}
		return result.Build(draftChangeSetName, result.OK, msg, result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Limits: limits, NextActions: []string{"verify-change"},
			Evidence: []result.Evidence{result.NewEvidence(result.EvPredict, "changeSetLifecycle", nil, map[string]any{"mode": result.ModeDryRun, "op": op, "change_set": before}, "")}})
	}
	if need && strings.TrimSpace(in.Confirm) != confirmWith {
		return result.Build(draftChangeSetName, result.Failed, fmt.Sprintf("Refused, nothing was changed: %s on change set %s needs confirm=%q", verb, id, confirmWith), result.Deterministic, cx,
			result.Options{Mode: result.ModeApplied, Limits: limits, Evidence: []result.Evidence{result.NewEvidence(result.EvPredict, "changeSetLifecycle", nil, map[string]any{"mode": result.ModeApplied, "op": op, "change_set": before, "refused": "confirm missing"}, "")}})
	}
	detail, err := do()
	if err != nil {
		return result.Result{}, fmt.Errorf("%s change set %s failed, it may be unchanged: %w", verb, id, err)
	}
	ch.Applied = true
	finding := fmt.Sprintf("Applied: %s change set %s (%s)", verb, id, cs.Name)
	if m, ok := detail.(map[string]any); ok && op == "predict" && m["new_prediction"] == false {
		finding = fmt.Sprintf("Forward answered with predicted snapshot %v, which already existed for change set %s (%s) in state %v: no new prediction was visible, so nothing is known to have started (Forward may reuse the earlier prediction while the change set is unchanged since it); list the change set's predictions with verify-change view describe or list", m["predicted_snapshot_id"], id, cs.Name, m["state"])
	} else if m, ok := detail.(map[string]any); ok && op == "predict" {
		finding = fmt.Sprintf("Started a prediction of change set %s (%s) as predicted snapshot %v (state %v); it did NOT wait: wait for it with `fwdctl wait snapshot --network %s --snapshot %v`, then read verify-change view list or describe", id, cs.Name, m["predicted_snapshot_id"], m["state"], in.NetworkID, m["predicted_snapshot_id"])
	}
	return result.Build(draftChangeSetName, result.OK, finding, result.Deterministic, cx, result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Limits: limits, NextActions: []string{"verify-change"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvPredict, "changeSetLifecycle", nil, map[string]any{"mode": result.ModeApplied, "op": op, "change_set": before, "result": detail}, finding)}})
}
