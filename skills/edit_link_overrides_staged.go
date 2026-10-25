package skills

import (
	"context"
	"fmt"
	"slices"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const stagedOverridesLimit = "staged (network-level) overrides: an edit leaves every existing snapshot alone and applies to the network's NEXT snapshot; they reach an existing snapshot only through a backdate, which invalidates it and every later one. Forward 26.8 and later; the snapshot-scoped overrides are deprecated for removal in 26.11"

// editStagedLinkOverrides edits or backdates the network's staged link overrides. An edit (add_present, remove_present, add_absent, remove_absent) changes only what is staged. backdate_to_snapshot_id (alone, with confirm
// equal to that snapshot id) pushes the staged set onto that snapshot and every one created after it, invalidating them. The two are separate runs so the person is asked before anything is invalidated.
func editStagedLinkOverrides(ctx context.Context, s *fwd.Session, in editLinkOverridesInput) (result.Result, error) {
	total := len(in.AddPresent) + len(in.RemovePresent) + len(in.AddAbsent) + len(in.RemoveAbsent)
	if in.NetworkID == "" || (total == 0 && in.BackdateTo == "") {
		return result.Result{}, fmt.Errorf("%w: staged needs network_id and either link edits (add_present, remove_present, add_absent, remove_absent) or backdate_to_snapshot_id", ErrInvalidInput)
	}
	if total > 0 && in.BackdateTo != "" {
		return result.Result{}, fmt.Errorf("%w: edit the staged overrides in one run and backdate in another: a backdate invalidates snapshots and the person is asked first", ErrInvalidInput)
	}
	if total > maxLinkEdits {
		return result.Result{}, fmt.Errorf("%w: at most %d link edits per run", ErrInvalidInput, maxLinkEdits)
	}
	if in.SnapshotID != "" || in.Force {
		return result.Result{}, fmt.Errorf("%w: snapshot_id and force belong to the snapshot-scoped mode, not to staged", ErrInvalidInput)
	}
	for _, group := range [][]linkPair{in.AddPresent, in.RemovePresent, in.AddAbsent, in.RemoveAbsent} {
		for _, l := range group {
			if strings.TrimSpace(l.Port1) == "" || strings.TrimSpace(l.Port2) == "" || l.Port1 == l.Port2 {
				return result.Result{}, fmt.Errorf("%w: a link names two different ports (port1, port2)", ErrInvalidInput)
			}
		}
	}
	if in.Confirm != "" && (!in.Apply || in.BackdateTo == "") {
		return result.Result{}, fmt.Errorf("%w: confirm belongs to apply of a backdate", ErrInvalidInput)
	}
	mode := result.ModeDryRun
	if in.Apply {
		mode = result.ModeApplied
	}
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	limits := []string{stagedOverridesLimit}
	top := s.Client.Topology
	if in.BackdateTo != "" {
		return stagedBackdate(ctx, s, in, cx, mode, limits)
	}
	cur, _, err := top.NetworkOverrides(ctx, in.NetworkID)
	if err != nil {
		return result.Result{}, err
	}
	var edit, undo forward.TopologyOverridesEdit
	pick := func(want []linkPair, list []forward.TopologyOverrideLink, mustHave bool) []linkPair {
		var out []linkPair
		for _, l := range want {
			if hasLink(list, l) == mustHave && !slices.ContainsFunc(out, l.same) {
				out = append(out, l)
			}
		}
		return out
	}
	addP, remP := pick(in.AddPresent, cur.Present, false), pick(in.RemovePresent, cur.Present, true)
	addA, remA := pick(in.AddAbsent, cur.Absent, false), pick(in.RemoveAbsent, cur.Absent, true)
	edit = forward.TopologyOverridesEdit{PresentAdditions: toSDK(addP), PresentRemovals: toSDK(remP), AbsentAdditions: toSDK(addA), AbsentRemovals: toSDK(remA)}
	undo = forward.TopologyOverridesEdit{PresentAdditions: toSDK(remP), PresentRemovals: toSDK(addP), AbsentAdditions: toSDK(remA), AbsentRemovals: toSDK(addA)}
	n := len(addP) + len(remP) + len(addA) + len(remA)
	if snap, serr := resolveSnapshot(ctx, s, in.NetworkID, ""); serr == nil && snap != nil && fwd.IsReady(snap) {
		problems, notes, vlimits := checkNewLinks(ctx, s, in.NetworkID, string(snap.ID), addP, addA)
		if len(problems) > 0 {
			return result.Result{}, fmt.Errorf("%w: refused, nothing was changed: %s (checked against the newest processed snapshot %s; Forward answers 400 Illegal ports in link overrides for these)", ErrInvalidInput, strings.Join(problems, "; "), snap.ID)
		}
		limits = append(limits, notes...)
		limits = append(limits, vlimits...)
		limits = append(limits, "ports were checked against the newest processed snapshot "+string(snap.ID)+", the one Forward checks a staged edit against")
	} else {
		limits = append(limits, "ports were not pre-checked (no processed snapshot could be read); Forward checks them against the latest processed snapshot and answers 400 Illegal ports in link overrides for an unknown one")
	}
	ev := func(extra map[string]any) []result.Evidence {
		d := map[string]any{"staged": true, "edit": edit, "mode": mode, "api": map[string]any{"route": "PATCH /api/networks/" + in.NetworkID + "/link-overrides", "body": edit, "curl": linkOverridesCurl("PATCH", "/api/networks/"+in.NetworkID+"/link-overrides")}, "present_before": len(cur.Present), "absent_before": len(cur.Absent)}
		for k, v := range extra {
			d[k] = v
		}
		return []result.Evidence{result.NewEvidence(result.EvTopology, "networkLinkOverrides", nil, d, "")}
	}
	next := []string{"inspect-topology", "edit-link-overrides"}
	if n == 0 {
		return result.Build(editLinkOverridesName, result.OK, "The staged overrides are already as requested; nothing to change", result.Deterministic, cx,
			result.Options{Mode: mode, Evidence: ev(nil), Limits: limits, NextActions: next})
	}
	ch := result.Change{Action: "edit_staged_link_overrides", Target: "staged link overrides of network " + in.NetworkID, Before: fmt.Sprintf("%d present, %d absent staged", len(cur.Present), len(cur.Absent)),
		After: fmt.Sprintf("%d link edit(s)", n), Reversible: true, Undo: fmt.Sprintf("apply the inverse edit %+v as staged add_present/remove_present/add_absent/remove_absent with apply=true", undo)}
	if !in.Apply {
		return result.Build(editLinkOverridesName, result.OK, fmt.Sprintf("Dry run: would make %d staged link edit(s) on network %s. Nothing was changed and no snapshot is touched; run again with apply=true to stage them. They reach an existing snapshot only through a backdate", n, in.NetworkID),
			result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Evidence: ev(nil), Limits: limits, NextActions: next})
	}
	if _, err := top.EditNetworkOverrides(ctx, in.NetworkID, edit); err != nil {
		return result.Result{}, fmt.Errorf("the staged edit failed, nothing is known to have changed: %w", err)
	}
	ch.Applied = true
	after, _, err := top.NetworkOverrides(ctx, in.NetworkID)
	if err != nil {
		return result.Build(editLinkOverridesName, result.Unknown, "The staged edit was sent but could not be read back, so it is NOT proven; do not apply it again", result.NoBasis, cx,
			result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(map[string]any{"read_back_error": err.Error()}), Limits: append(limits, "read the staged overrides again before anything else"), NextActions: next})
	}
	held := true
	for _, l := range addP {
		held = held && hasLink(after.Present, l)
	}
	for _, l := range remP {
		held = held && !hasLink(after.Present, l)
	}
	for _, l := range addA {
		held = held && hasLink(after.Absent, l)
	}
	for _, l := range remA {
		held = held && !hasLink(after.Absent, l)
	}
	if !held {
		return result.Build(editLinkOverridesName, result.Failed, "Forward accepted the staged edit but the staged overrides are not in the requested state", result.Deterministic, cx,
			result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(map[string]any{"held": false}), Limits: limits})
	}
	return result.Build(editLinkOverridesName, result.OK, fmt.Sprintf("Staged %d link edit(s) on network %s; no existing snapshot changed", n, in.NetworkID), result.Deterministic, cx,
		result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(map[string]any{"held": true}), Limits: limits, NextActions: next})
}

// stagedBackdate pushes the staged overrides onto a snapshot and every one created after it (Forward's rule: every non-draft snapshot of the network created at or after the chosen one's creation time, with no end).
// The dry run lists them from the snapshot list, worded as Forward's rule, not as something Forward reported.
func stagedBackdate(ctx context.Context, s *fwd.Session, in editLinkOverridesInput, cx result.Context, mode string, limits []string) (result.Result, error) {
	snaps, err := s.Snapshots(ctx, in.NetworkID)
	if err != nil {
		return result.Result{}, err
	}
	var target string
	found := false
	for _, sn := range snaps {
		if string(sn.ID) == in.BackdateTo {
			target, found = sn.CreatedAt, true
		}
	}
	if !found || target == "" {
		return result.NewUnknown(editLinkOverridesName, fmt.Sprintf("The network holds no snapshot %s to backdate to", in.BackdateTo), cx,
			[]string{"no such snapshot in this network (ids are matched exactly); nothing was changed"}, result.Options{NextActions: []string{"inspect-snapshots"}})
	}
	hit := []map[string]any{}
	for _, sn := range snaps {
		if sn.CreatedAt >= target && !sn.IsDraft {
			hit = append(hit, map[string]any{"snapshot_id": string(sn.ID), "state": sn.State, "created_at": sn.CreatedAt})
		}
	}
	staged, _, serr := s.Client.Topology.StagedOverrideChanges(ctx, in.NetworkID)
	limits = append(limits,
		"per Forward's rule (not reported by Forward): a backdate INVALIDATES every non-draft snapshot of the network created at or after snapshot "+in.BackdateTo+"'s creation time, with no end; this list is computed from the snapshot list, and a draft snapshot in it is not invalidated",
		"a backdate to an OLDER snapshot replaces that snapshot's overrides with the full staged set, which can change more than the staged-changes view shows; it needs EDIT_TOPOLOGY_LINKS and INVALIDATE_SNAPSHOTS, and the network has no staged overrides afterwards",
		reprocessAskLimit)
	d := map[string]any{"staged": true, "backdate_to": in.BackdateTo, "would_invalidate": hit, "mode": mode}
	if serr == nil {
		d["staged_changes_since_latest_processed"] = staged
	} else {
		limits = append(limits, "the staged changes could not be read: "+serr.Error())
	}
	ev := []result.Evidence{result.NewEvidence(result.EvTopology, "backdateLinkOverrides", nil, d, "")}
	ch := result.Change{Action: "backdate_link_overrides", Target: fmt.Sprintf("apply the staged link overrides of network %s from snapshot %s onward", in.NetworkID, in.BackdateTo),
		Before: fmt.Sprintf("%d snapshot(s) at or after it", len(hit)), After: "those snapshots invalidated and reprocessing", Reversible: false,
		Undo: "none: the staged set is consumed; restore the earlier overrides by staging them again and backdating again (each backdate invalidates again)"}
	if !in.Apply {
		return result.Build(editLinkOverridesName, result.OK, fmt.Sprintf("Dry run: would backdate the staged overrides onto snapshot %s, invalidating %d snapshot(s) per Forward's rule. Nothing was changed; run again with apply=true and confirm=%q", in.BackdateTo, len(hit), in.BackdateTo),
			result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Evidence: ev, Limits: limits, NextActions: []string{"inspect-snapshots"}})
	}
	if in.Confirm != in.BackdateTo {
		return result.Result{}, fmt.Errorf("%w: a backdate invalidates %d snapshot(s) and cannot be undone; set confirm to the exact snapshot id %q to apply", ErrInvalidInput, len(hit), in.BackdateTo)
	}
	if _, err := s.Client.Topology.BackdateLinkOverrides(ctx, in.NetworkID, in.BackdateTo); err != nil {
		return result.Result{}, fmt.Errorf("the backdate failed, nothing is known to have changed: %w", err)
	}
	ch.Applied = true
	// read the states back: a backdate that Forward accepted should leave the named snapshots reprocessing or unprocessed
	still := 0
	if after, aerr := s.Snapshots(ctx, in.NetworkID); aerr != nil {
		limits = append(limits, "the snapshot states could not be read back, so the invalidation is not proven: "+aerr.Error())
	} else {
		inHit := map[string]bool{}
		for _, h := range hit {
			inHit[fmt.Sprint(h["snapshot_id"])] = true
		}
		for _, sn := range after {
			if inHit[string(sn.ID)] && sn.State == "PROCESSED" {
				still++
			}
		}
		if still > 0 {
			limits = append(limits, fmt.Sprintf("%d of the %d snapshot(s) still read PROCESSED right after the backdate; read inspect-snapshots again before relying on it", still, len(hit)))
		}
	}
	return result.Build(editLinkOverridesName, result.OK, fmt.Sprintf("Backdate sent for snapshot %s: %d snapshot(s) are invalidated per Forward's rule and reprocess is now the person's decision; ask before running edit-snapshot reprocess, one at a time", in.BackdateTo, len(hit)),
		result.Deterministic, cx, result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev, Limits: append(limits, "the invalidated snapshots are UNPROCESSED until reprocessed; Forward does not reprocess them by itself; verify afterwards with inspect-topology kind link_overrides"), NextActions: []string{"inspect-snapshots", "edit-snapshot"}})
}
