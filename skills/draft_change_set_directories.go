package skills

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// draftDirectoryOp is edit-change-set directory_op: add, rename, move or delete a directory that change sets are filed under. A dry run reads the directories and says what would change;
// deleting a directory keeps its change sets (they become unfiled) unless also_delete_change_sets is set, which deletes them too, cannot be undone and needs confirm = the path.
type draftDirectoryOp struct {
	Op                   string `json:"op"`
	Path                 string `json:"path"`
	Name                 string `json:"name"`
	To                   string `json:"to"`
	AlsoDeleteChangeSets bool   `json:"also_delete_change_sets"`
}

func validDirPath(p string) bool {
	return strings.HasPrefix(p, "/") && (p == "/" || !strings.HasSuffix(p, "/")) && !strings.Contains(p, "//")
}

func changeSetDirectoryOp(ctx context.Context, s *fwd.Session, in draftChangeSetInput) (result.Result, error) {
	d := in.DirectoryOp
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	if len(in.Devices) > 0 || len(in.BGP) > 0 || len(in.Firewall) > 0 || in.Run || in.SnapshotID != "" || in.ChangeSetID != "" || in.Lifecycle != nil {
		return result.Result{}, fmt.Errorf("%w: directory_op works on directories only; it does not combine with a change set, lifecycle, devices, bgp_advertisements, firewall_rules, snapshot_id or run", ErrInvalidInput)
	}
	op := strings.ToLower(strings.TrimSpace(d.Op))
	need := func(ok bool, what string) error {
		if !ok {
			return fmt.Errorf("%w: directory_op %s needs %s (paths look like /team/wan: a leading slash, no trailing or doubled slash)", ErrInvalidInput, op, what)
		}
		return nil
	}
	var err error
	switch op {
	case "add":
		err = need(validDirPath(d.Path) && strings.TrimSpace(d.Name) != "" && !strings.Contains(d.Name, "/"), "path (the parent, / for the top) and name (one segment, no slash)")
	case "rename":
		err = need(validDirPath(d.Path) && d.Path != "/" && strings.TrimSpace(d.Name) != "" && !strings.Contains(d.Name, "/"), "path and name (the new last segment)")
	case "move":
		err = need(validDirPath(d.Path) && d.Path != "/" && validDirPath(d.To), "path and to (the new parent)")
	case "delete":
		err = need(validDirPath(d.Path) && d.Path != "/", "path")
	default:
		return result.Result{}, fmt.Errorf("%w: directory_op.op is add, rename, move or delete", ErrInvalidInput)
	}
	if err != nil {
		return result.Result{}, err
	}
	dirs, _, derr := s.Client.Predict.ListChangeSetDirectories(ctx, in.NetworkID)
	if derr != nil {
		if r, ok := denialResult(draftChangeSetName, derr, cx, "listing the network's change-set directories"); ok {
			return r, nil
		}
		return result.Result{}, derr
	}
	paths := make([]string, 0, len(dirs))
	for p := range dirs {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	shown, total := result.CapRow(paths, 50)
	before := map[string]any{"directories": shown, "directories_total": total}
	if op != "add" {
		if ids, ok := dirs[d.Path]; ok {
			before["change_sets_in_path"] = len(ids)
		} else {
			return result.NewUnknown(draftChangeSetName, fmt.Sprintf("Directory %s does not exist in network %s", d.Path, in.NetworkID), cx,
				[]string{"Forward lists no such directory; nothing was changed (verify-change view history shows where a change set is filed)"}, result.Options{NextActions: []string{"verify-change"}})
		}
	}
	ch := result.Change{Target: "change-set directory " + d.Path, Before: before, Reversible: true}
	confirmWith := ""
	var do func() error
	var verb string
	switch op {
	case "add":
		ch.Action, verb, ch.After = "add_change_set_directory", "create directory "+strings.TrimRight(d.Path, "/")+"/"+d.Name, map[string]any{"directory": strings.TrimRight(d.Path, "/") + "/" + d.Name}
		ch.Undo = "delete the empty directory (directory_op delete)"
		do = func() error {
			_, e := s.Client.Predict.AddChangeSetDirectory(ctx, in.NetworkID, d.Path, strings.TrimSpace(d.Name))
			return e
		}
	case "rename":
		ch.Action, verb, ch.After = "rename_change_set_directory", "rename directory "+d.Path, map[string]any{"new_name": strings.TrimSpace(d.Name)}
		ch.Undo = "rename it back (directory_op rename with its old last segment)"
		do = func() error {
			_, e := s.Client.Predict.RenameChangeSetDirectory(ctx, in.NetworkID, d.Path, strings.TrimSpace(d.Name))
			return e
		}
	case "move":
		ch.Action, verb, ch.After = "move_change_set_directory", "move directory "+d.Path+" under "+d.To, map[string]any{"new_parent": d.To}
		ch.Undo = "move it back to its earlier parent"
		do = func() error {
			_, e := s.Client.Predict.MoveChangeSetDirectory(ctx, in.NetworkID, d.Path, d.To)
			return e
		}
	case "delete":
		ch.Action = "delete_change_set_directory"
		if d.AlsoDeleteChangeSets {
			verb, ch.Reversible, confirmWith = "delete directory "+d.Path+" AND the change sets in it", false, d.Path
			ch.After = map[string]any{"deleted": true, "change_sets_deleted": before["change_sets_in_path"]}
			ch.Undo = "none: the change sets and their versions are gone"
		} else {
			verb = "delete directory " + d.Path + " (its change sets are kept, unfiled)"
			ch.After = map[string]any{"deleted": true, "change_sets_kept": before["change_sets_in_path"]}
			ch.Undo = "create the directory again and move the change sets back; their earlier filing is in this result's before"
		}
		do = func() error {
			_, e := s.Client.Predict.DeleteChangeSetDirectory(ctx, in.NetworkID, d.Path, d.AlsoDeleteChangeSets)
			return e
		}
	}
	limits := []string{"directories only organise change sets; nothing here reaches a device. Preview routes, source-read, not run on a live Forward"}
	if !in.Apply {
		msg := fmt.Sprintf("Dry run: would %s. Nothing was changed; run again with apply=true", verb)
		if confirmWith != "" {
			msg += fmt.Sprintf(" and confirm=%q", confirmWith)
		}
		return result.Build(draftChangeSetName, result.OK, msg, result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Limits: limits, NextActions: []string{"verify-change"},
			Evidence: []result.Evidence{result.NewEvidence(result.EvPredict, "changeSetDirectory", nil, map[string]any{"mode": result.ModeDryRun, "op": op, "before": before}, "")}})
	}
	if confirmWith != "" && strings.TrimSpace(in.Confirm) != confirmWith {
		return result.Build(draftChangeSetName, result.Failed, fmt.Sprintf("Refused, nothing was changed: %s needs confirm=%q", verb, confirmWith), result.Deterministic, cx,
			result.Options{Mode: result.ModeApplied, Limits: limits, Evidence: []result.Evidence{result.NewEvidence(result.EvPredict, "changeSetDirectory", nil, map[string]any{"mode": result.ModeApplied, "op": op, "changed": false}, "")}})
	}
	if err := do(); err != nil {
		return result.Result{}, fmt.Errorf("%s failed, it may be unchanged: %w", verb, err)
	}
	ch.Applied = true
	finding := "Applied: " + verb
	return result.Build(draftChangeSetName, result.OK, finding, result.Deterministic, cx, result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Limits: limits, NextActions: []string{"verify-change"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvPredict, "changeSetDirectory", nil, map[string]any{"mode": result.ModeApplied, "op": op, "before": before}, finding)}})
}
