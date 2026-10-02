package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const editLinkOverridesName = "edit-link-overrides"

func init() { Register(editLinkOverridesName, editLinkOverrides) }

type linkPair struct {
	Port1 string `json:"port1"`
	Port2 string `json:"port2"`
}

type editLinkOverridesInput struct {
	NetworkID     string     `json:"network_id"`
	SnapshotID    string     `json:"snapshot_id"`
	AddPresent    []linkPair `json:"add_present"`
	RemovePresent []linkPair `json:"remove_present"`
	AddAbsent     []linkPair `json:"add_absent"`
	RemoveAbsent  []linkPair `json:"remove_absent"`
	Apply         bool       `json:"apply"`
	// Force lets an add_present name a port the snapshot does not have; without it such an edit is refused, dry run or applied.
	Force bool `json:"force"`
}

const maxLinkEdits = 100

// same reports whether two overrides name the same link: a link has no direction, so (a, b) and (b, a) are one link.
func (l linkPair) same(o linkPair) bool {
	return (l.Port1 == o.Port1 && l.Port2 == o.Port2) || (l.Port1 == o.Port2 && l.Port2 == o.Port1)
}

func hasLink(list []forward.TopologyOverrideLink, l linkPair) bool {
	return slices.ContainsFunc(list, func(x forward.TopologyOverrideLink) bool { return l.same(linkPair{x.Port1, x.Port2}) })
}

func toSDK(ls []linkPair) []forward.TopologyOverrideLink {
	var out []forward.TopologyOverrideLink
	for _, l := range ls {
		out = append(out, forward.TopologyOverrideLink{Port1: l.Port1, Port2: l.Port2})
	}
	return out
}

// editLinkOverrides adds or removes the manual links ("present": a link Forward did not discover) and the suppressed links ("absent":
// a discovered link to ignore) of one snapshot. It reads the current overrides first, sends only the edits that change something,
// and returns the exact inverse edit as the undo.
func editLinkOverrides(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in editLinkOverridesInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	total := len(in.AddPresent) + len(in.RemovePresent) + len(in.AddAbsent) + len(in.RemoveAbsent)
	if in.NetworkID == "" || in.SnapshotID == "" || total == 0 {
		return result.Result{}, fmt.Errorf("%w: network_id, snapshot_id and at least one of add_present, remove_present, add_absent, remove_absent are required", ErrInvalidInput)
	}
	if total > maxLinkEdits {
		return result.Result{}, fmt.Errorf("%w: at most %d link edits per run", ErrInvalidInput, maxLinkEdits)
	}
	for _, group := range [][]linkPair{in.AddPresent, in.RemovePresent, in.AddAbsent, in.RemoveAbsent} {
		for _, l := range group {
			if strings.TrimSpace(l.Port1) == "" || strings.TrimSpace(l.Port2) == "" || l.Port1 == l.Port2 {
				return result.Result{}, fmt.Errorf("%w: a link names two different ports (port1, port2)", ErrInvalidInput)
			}
		}
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
		return result.NewUnknown(editLinkOverridesName, fmt.Sprintf("The network holds no snapshot %s", in.SnapshotID), cx,
			[]string{"no such snapshot in this network (ids are matched exactly); nothing was changed"}, result.Options{NextActions: []string{"inspect-snapshots"}})
	}
	cx = fwd.Context(in.NetworkID, sn)
	cur, err := s.LinkOverrides(ctx, in.SnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	// only the edits that change something
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
	problems, notes, vlimits := checkNewLinks(ctx, s, in.NetworkID, in.SnapshotID, addP, addA)
	ev := func(extra map[string]any) []result.Evidence {
		// counts, not the lists: a snapshot can hold hundreds of overrides, and inspect-topology (kind link_overrides) reads them
		d := map[string]any{"snapshot_id": in.SnapshotID, "edit": edit, "mode": mode,
			"present_before": len(cur.Present), "absent_before": len(cur.Absent),
			"present_after": len(cur.Present) + len(addP) - len(remP), "absent_after": len(cur.Absent) + len(addA) - len(remA)}
		if len(problems) > 0 {
			d["problems"] = problems
		}
		if len(notes) > 0 {
			d["notes"] = notes
		}
		for k, v := range extra {
			d[k] = v
		}
		return []result.Evidence{result.NewEvidence(result.EvTopology, "topologyOverrides", cx.SnapshotID, d, "")}
	}
	next := []string{"edit-snapshot-reprocess", "edit-advanced-reachability", "inspect-topology", "verify-change"}
	limits := []string{
		"applying INVALIDATES this snapshot (and the snapshots after it up to the end of its override range), setting them UNPROCESSED; Forward does not reprocess an invalidated snapshot by itself (confirmed live), so their answers are unavailable until edit-snapshot-reprocess is run for each one, then edit-advanced-reachability if internet exposure is needed (it only runs after a snapshot is PROCESSED) (Forward's LinkOverridesService invalidates the affected snapshots on a snapshot-scoped write)",
		"overrides are stored per snapshot here; whether a snapshot collected later carries them is not stated by Forward's API, so inspect-topology (kind link_overrides, compare_to_snapshot_id) on the next snapshot shows what it actually holds",
		"this skill uses Forward's snapshot-scoped override endpoint, which Forward has deprecated for removal in release 26.11; the network-level operations (staged for the next snapshot, no invalidation until a backdate) are not used by this skill"}
	limits = append(limits, vlimits...)
	if n == 0 {
		return result.Build(editLinkOverridesName, result.OK, "The overrides are already as requested; nothing to change", result.Deterministic, cx,
			result.Options{Mode: mode, Evidence: ev(nil), NextActions: next})
	}
	if len(problems) > 0 && !in.Force {
		return result.Build(editLinkOverridesName, result.Failed, fmt.Sprintf("Refused, nothing was changed: %s. An override naming a port Forward cannot find cannot take effect, and applying one invalidates the snapshot. Fix the port names (inspect-topology kind link_overrides, inspect-inventory kind interfaces), or set force=true", strings.Join(problems, "; ")),
			result.Deterministic, cx, result.Options{Mode: mode, Evidence: ev(nil), Limits: limits, NextActions: []string{"inspect-inventory", "inspect-topology"}})
	}
	undoJSON, _ := json.Marshal(undo)
	ch := result.Change{Action: "edit_link_overrides", Target: "snapshot " + in.SnapshotID, Before: fmt.Sprintf("present %d, absent %d", len(cur.Present), len(cur.Absent)),
		After: fmt.Sprintf("%d link edit(s)", n), Reversible: true,
		Undo: "apply the inverse edit " + string(undoJSON) + " (as add_present/remove_present/add_absent/remove_absent) with apply=true"}
	if !in.Apply {
		warn := ""
		if len(notes) > 0 {
			warn = " Notes: " + strings.Join(notes, "; ") + "."
		}
		return result.Build(editLinkOverridesName, result.OK, fmt.Sprintf("Dry run: would make %d link edit(s) on snapshot %s (present %d to %d, absent %d to %d). Nothing was changed; run again with apply=true to make them.%s", n, in.SnapshotID,
			len(cur.Present), len(cur.Present)+len(addP)-len(remP), len(cur.Absent), len(cur.Absent)+len(addA)-len(remA), warn),
			result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Evidence: ev(nil), Limits: limits, NextActions: next})
	}
	if err := s.EditLinkOverrides(ctx, in.SnapshotID, edit); err != nil {
		return result.Result{}, fmt.Errorf("the edit failed, nothing is known to have changed: %w", err)
	}
	ch.Applied = true
	after, err := s.LinkOverrides(ctx, in.SnapshotID)
	if err != nil {
		return result.Result{}, fmt.Errorf("the edit was sent but reading the overrides back failed, so it is not proven: %w", err)
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
		return result.Build(editLinkOverridesName, result.Failed, "Forward accepted the edit but the snapshot's overrides are not in the requested state", result.Deterministic, cx,
			result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(map[string]any{"held": false}), Limits: limits})
	}
	return result.Build(editLinkOverridesName, result.OK, fmt.Sprintf("Applied %d link edit(s) on snapshot %s", n, in.SnapshotID), result.Deterministic, cx,
		result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(map[string]any{"held": true}), Limits: limits, NextActions: next})
}

// checkNewLinks validates the links an edit adds against the snapshot, reusing the ports_exist and link_in_topology derivation of
// inspect-topology. problems (a port or device the snapshot does not have) block the edit unless forced; notes are advisory (a link
// to add that is already discovered, a link to suppress that is not). A derivation that failed is said in limits and blocks nothing:
// unknown is never a pass, so the limit says the ports were not checked.
func checkNewLinks(ctx context.Context, s *fwd.Session, networkID, snapshotID string, addP, addA []linkPair) (problems, notes, limits []string) {
	var page []linkOverride
	ordered := func(state string, l linkPair) linkOverride { // flagOverrideRows expects Forward's order, port1 <= port2
		if l.Port2 < l.Port1 {
			l.Port1, l.Port2 = l.Port2, l.Port1
		}
		return linkOverride{State: state, Port1: l.Port1, Port2: l.Port2}
	}
	for _, l := range addP {
		page = append(page, ordered("present", l))
	}
	for _, l := range addA {
		page = append(page, ordered("absent", l))
	}
	if len(page) == 0 {
		return nil, nil, nil
	}
	rows := make([]map[string]any, len(page))
	for i := range rows {
		rows[i] = map[string]any{}
	}
	limits = flagOverrideRows(ctx, s, networkID, snapshotID, page, rows)
	// the explanatory limit flagOverrideRows appends is about listed rows; here the flags are summarised in the finding instead
	if len(limits) > 0 && strings.HasPrefix(limits[len(limits)-1], "ports_exist:") {
		limits = limits[:len(limits)-1]
	}
	checked := false
	for i, l := range page {
		r := rows[i]
		label := l.Port1 + " <-> " + l.Port2
		if ok, known := r["ports_exist"].(bool); known {
			checked = true
			if !ok {
				problems = append(problems, fmt.Sprintf("%s: %s", label, strings.Join(r["missing_ports"].([]string), ", ")))
			}
		}
		if in, known := r["link_in_topology"].(bool); known {
			switch {
			case l.State == "present" && in:
				notes = append(notes, label+" is already a discovered link")
			case l.State == "absent" && !in:
				notes = append(notes, label+" is not a link in the snapshot, so there is nothing to suppress")
			}
		}
	}
	if !checked {
		limits = append(limits, "the new links' ports were NOT checked against the snapshot; a wrong port name would not take effect")
	}
	return problems, notes, limits
}
