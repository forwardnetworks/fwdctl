package skills

import (
	"context"
	"fmt"
	"sort"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// The diff view of inspect-topology (kind link_overrides with compare_to_snapshot_id; the retired compare-link-overrides skill).
const compareLinkOverridesName = topologyName

type compareLinkOverridesInput struct {
	NetworkID        string `json:"network_id"`
	BeforeSnapshotID string `json:"before_snapshot_id"`
	AfterSnapshotID  string `json:"after_snapshot_id"`
	Device           string `json:"device"`
	Limit            int    `json:"limit"`
}

type ovrKey struct{ P1, P2 string }

// compareLinkOverridesView says which link overrides one snapshot has and the other lacks: added (only in after), removed (only in before) and changed (present in one, absent in the other),
// with counts, the devices involved and bounded examples. Overrides are stored per snapshot, so a snapshot collected later or reprocessed can lack ones an earlier snapshot has.
func compareLinkOverridesView(ctx context.Context, s *fwd.Session, in compareLinkOverridesInput) (result.Result, error) {
	if in.NetworkID == "" || in.BeforeSnapshotID == "" || in.AfterSnapshotID == "" {
		return result.Result{}, fmt.Errorf("%w: network_id, before_snapshot_id and after_snapshot_id are required", ErrInvalidInput)
	}
	if in.Limit <= 0 {
		in.Limit = defaultDiffRows
	}
	in.Limit = min(in.Limit, maxOverrideLimit)
	before, err := s.Snapshot(ctx, in.NetworkID, in.BeforeSnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	after, err := s.Snapshot(ctx, in.NetworkID, in.AfterSnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	cx := fwd.Context(in.NetworkID, after)
	if before == nil || after == nil {
		return result.NewUnknown(compareLinkOverridesName, "A snapshot to compare does not exist in this network", cx,
			[]string{fmt.Sprintf("before snapshot %s is %s, after snapshot %s is %s; ids are matched exactly; nothing was compared", in.BeforeSnapshotID, fwd.StateOf(before), in.AfterSnapshotID, fwd.StateOf(after))},
			result.Options{NextActions: []string{"inspect-snapshots"}})
	}
	var limits []string
	read := func(id string) (map[ovrKey]string, int, error) {
		o, err := s.LinkOverrides(ctx, id)
		if err != nil {
			return nil, 0, err
		}
		m := map[ovrKey]string{}
		all := filterOverrides(listOverrides(o), in.Device)
		for _, l := range all {
			m[ovrKey{l.Port1, l.Port2}] = l.State
		}
		return m, len(listOverrides(o)), nil
	}
	b, btotal, err := read(in.BeforeSnapshotID)
	if err != nil {
		return result.NewUnknown(compareLinkOverridesName, fmt.Sprintf("The overrides of snapshot %s could not be read, so nothing was compared", in.BeforeSnapshotID), cx,
			[]string{"before snapshot " + in.BeforeSnapshotID + " is " + fwd.StateOf(before) + ": " + err.Error() + " (Forward refuses to read a snapshot's overrides while it is being processed)"}, result.Options{NextActions: []string{"inspect-snapshots"}})
	}
	a, atotal, err := read(in.AfterSnapshotID)
	if err != nil {
		return result.NewUnknown(compareLinkOverridesName, fmt.Sprintf("The overrides of snapshot %s could not be read, so nothing was compared", in.AfterSnapshotID), cx,
			[]string{"after snapshot " + in.AfterSnapshotID + " is " + fwd.StateOf(after) + ": " + err.Error() + " (Forward refuses to read a snapshot's overrides while it is being processed)"}, result.Options{NextActions: []string{"inspect-snapshots"}})
	}
	if fwd.IsPredicted(before) || fwd.IsPredicted(after) {
		limits = append(limits, "one snapshot is a prediction: the difference is between a collected state and a prediction")
	}
	var added, removed, changed []map[string]any
	unchanged := 0
	devCount := map[string]int{}
	note := func(k ovrKey) {
		d1, _ := splitPort(k.P1)
		d2, _ := splitPort(k.P2)
		devCount[d1]++
		if d2 != d1 {
			devCount[d2]++
		}
	}
	for k, st := range a {
		bs, ok := b[k]
		switch {
		case !ok:
			added = append(added, map[string]any{"state": st, "port1": k.P1, "port2": k.P2})
			note(k)
		case bs != st:
			changed = append(changed, map[string]any{"port1": k.P1, "port2": k.P2, "before": bs, "after": st})
			note(k)
		default:
			unchanged++
		}
	}
	for k, st := range b {
		if _, ok := a[k]; !ok {
			removed = append(removed, map[string]any{"state": st, "port1": k.P1, "port2": k.P2})
			note(k)
		}
	}
	for _, l := range [][]map[string]any{added, removed, changed} {
		sort.Slice(l, func(i, j int) bool {
			if l[i]["port1"] != l[j]["port1"] {
				return l[i]["port1"].(string) < l[j]["port1"].(string)
			}
			return l[i]["port2"].(string) < l[j]["port2"].(string)
		})
	}
	cut := func(l []map[string]any) []map[string]any {
		if len(l) > in.Limit {
			return l[:in.Limit]
		}
		return l
	}
	if len(added) > in.Limit || len(removed) > in.Limit || len(changed) > in.Limit {
		limits = append(limits, fmt.Sprintf("examples are bounded to %d per list (limit, at most %d); the counts are complete", in.Limit, maxOverrideLimit))
	}
	devs := make([]string, 0, len(devCount))
	for d := range devCount {
		devs = append(devs, d)
	}
	sort.Slice(devs, func(i, j int) bool {
		if devCount[devs[i]] != devCount[devs[j]] {
			return devCount[devs[i]] > devCount[devs[j]]
		}
		return devs[i] < devs[j]
	})
	devRows := make([]map[string]any, 0, min(len(devs), topOverrideGroups))
	for i, d := range devs {
		if i >= topOverrideGroups {
			break
		}
		devRows = append(devRows, map[string]any{"device": d, "differing_overrides": devCount[d]})
	}
	counts := map[string]any{"before_total": btotal, "after_total": atotal, "added": len(added), "removed": len(removed), "changed": len(changed), "unchanged": unchanged}
	detail := map[string]any{"before_snapshot_id": in.BeforeSnapshotID, "after_snapshot_id": in.AfterSnapshotID, "counts": counts,
		"added": cut(added), "removed": cut(removed), "changed": cut(changed), "devices_involved": devRows}
	if in.Device != "" {
		detail["device"] = in.Device
		limits = append(limits, "counts other than before_total and after_total cover only overrides with "+in.Device+" at either end")
	}
	limits = append(limits, "added = an override only the after snapshot has, removed = only the before snapshot has, changed = present in one and absent in the other; a link has no direction so (a, b) and (b, a) are one override",
		overrideLimitsProvenance, overrideLimitsScope)
	diff := len(added) + len(removed) + len(changed)
	finding := fmt.Sprintf("Link overrides, snapshot %s (%d) to snapshot %s (%d): %d added, %d removed, %d changed", in.BeforeSnapshotID, btotal, in.AfterSnapshotID, atotal, len(added), len(removed), len(changed))
	if diff == 0 {
		finding = fmt.Sprintf("Link overrides are identical in snapshot %s and snapshot %s (%d each)", in.BeforeSnapshotID, in.AfterSnapshotID, atotal)
		if in.Device != "" {
			finding += " for device " + in.Device
		}
	}
	return result.Build(compareLinkOverridesName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, NextActions: []string{"edit-link-overrides", "inspect-snapshots"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvTopology, "topologyOverrides", fwd.SnapshotIDPtr(after), detail, finding)}})
}
