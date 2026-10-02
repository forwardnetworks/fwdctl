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

const editWanCircuitName = "edit-wan-circuit"

func init() { Register(editWanCircuitName, editWanCircuit) }

type wanEnd struct {
	Device string `json:"device"`
	Port   string `json:"port"`
	VLAN   *int   `json:"vlan"`
	Name   string `json:"name"`
}

type editWanCircuitInput struct {
	NetworkID          string  `json:"network_id"`
	Name               string  `json:"name"`
	Delete             bool    `json:"delete"`
	Connection1        *wanEnd `json:"connection1"`
	Connection2        *wanEnd `json:"connection2"`
	BackdateSnapshotID string  `json:"backdate_snapshot_id"`
	Apply              bool    `json:"apply"`
}

func (e wanEnd) sdk() forward.WanCircuitConnection {
	return forward.WanCircuitConnection{Device: e.Device, Port: e.Port, VLAN: e.VLAN, Name: e.Name}
}

func wanText(c *forward.WanCircuit) string {
	if c == nil {
		return ""
	}
	b, _ := json.Marshal(map[string]any{"name": c.Name, "connection1": c.Connection1, "connection2": c.Connection2})
	return string(b)
}

// editWanCircuit creates, replaces or deletes one hand-defined WAN circuit: a point-to-point layer 2 link a provider carries between two sites,
// modelled as a bridge with exactly two connections (a different VLAN may be used on each side). It reads the circuit first, so the dry run shows
// before and after and the undo is the exact prior circuit; the write is read back. Circuits are not definable by an NQE query.
func editWanCircuit(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in editWanCircuitInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.NetworkID == "" || in.Name == "" {
		return result.Result{}, fmt.Errorf("%w: network_id and name are required", ErrInvalidInput)
	}
	if in.Delete == (in.Connection1 != nil || in.Connection2 != nil) {
		return result.Result{}, fmt.Errorf("%w: give both connection1 and connection2 (create or replace), or delete: true", ErrInvalidInput)
	}
	if !in.Delete {
		for i, c := range []*wanEnd{in.Connection1, in.Connection2} {
			if c == nil || strings.TrimSpace(c.Device) == "" || strings.TrimSpace(c.Port) == "" {
				return result.Result{}, fmt.Errorf("%w: connection%d needs a device and a port (a WAN circuit has exactly two connections)", ErrInvalidInput, i+1)
			}
			if c.VLAN != nil && (*c.VLAN < 1 || *c.VLAN > 4095) {
				return result.Result{}, fmt.Errorf("%w: connection%d vlan is 1 to 4095; leave it out for untagged", ErrInvalidInput, i+1)
			}
		}
	}
	mode := result.ModeDryRun
	if in.Apply {
		mode = result.ModeApplied
	}
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	next := []string{"inspect-topology", "investigate-reachability"}
	var plan *backdatePlan
	if in.BackdateSnapshotID != "" {
		var err error
		if plan, err = planBackdate(ctx, s, in.NetworkID, in.BackdateSnapshotID); err != nil {
			return result.Result{}, err
		}
		if plan == nil {
			return result.NewUnknown(editWanCircuitName, fmt.Sprintf("The network holds no snapshot %s to backdate to", in.BackdateSnapshotID), cx,
				[]string{"no such snapshot (ids are matched exactly); nothing was changed"}, result.Options{NextActions: []string{"inspect-snapshots"}})
		}
	}
	prior, err := s.WanCircuit(ctx, in.NetworkID, in.Name)
	if err != nil {
		return result.Result{}, err
	}
	var want *forward.WanCircuit
	if !in.Delete {
		want = &forward.WanCircuit{Name: in.Name, Connection1: in.Connection1.sdk(), Connection2: in.Connection2.sdk()}
	}
	limits := []string{"a change reaches the next processed snapshot (or, with backdate_snapshot_id, an existing one now); a WAN circuit is a bridge with exactly two connections and is defined by hand (an NQE query cannot generate one)"}
	if plan != nil {
		limits = append(limits, plan.Note)
	}
	ev := func(extra map[string]any) []result.Evidence {
		d := map[string]any{"name": in.Name, "existed": prior != nil, "mode": mode}
		if prior != nil {
			d["before"] = json.RawMessage(wanText(prior))
		}
		if want != nil {
			d["after"] = json.RawMessage(wanText(want))
		}
		if plan != nil {
			d["backdate"] = plan
		}
		for k, v := range extra {
			d[k] = v
		}
		return []result.Evidence{result.NewEvidence(result.EvTopology, "wanCircuit", nil, d, "")}
	}
	switch {
	case in.Delete && prior == nil:
		return result.Build(editWanCircuitName, result.OK, fmt.Sprintf("The network has no WAN circuit %q; nothing to delete", in.Name), result.Deterministic, cx, result.Options{Mode: mode, Evidence: ev(nil), Limits: limits, NextActions: next})
	case !in.Delete && prior != nil && wanText(prior) == wanText(want):
		return result.Build(editWanCircuitName, result.OK, fmt.Sprintf("The WAN circuit %q already is that; nothing to change", in.Name), result.Deterministic, cx, result.Options{Mode: mode, Evidence: ev(nil), Limits: limits, NextActions: next})
	}
	action, undo := "put_wan_circuit", fmt.Sprintf("run edit-wan-circuit with name %q, delete=true and apply=true", in.Name)
	if prior != nil {
		undo = "run edit-wan-circuit again with the prior circuit (this change's before) and apply=true"
	}
	if in.Delete {
		action, undo = "delete_wan_circuit", "run edit-wan-circuit again with the prior circuit (this change's before: its two connections) and apply=true"
	}
	if plan != nil {
		undo += "; the backdate itself cannot be undone (the snapshots it invalidated are reprocessed, recomputing the same data)"
	}
	ch := result.Change{Action: action, Target: "WAN circuit " + in.Name, Before: wanText(prior), After: wanText(want), Reversible: true, Undo: undo}
	if !in.Apply {
		extra := ""
		if plan != nil {
			extra = fmt.Sprintf(", then backdate to snapshot %s (invalidating %d snapshot(s) so they reprocess now)", plan.Snapshot, len(plan.Affected))
		}
		return result.Build(editWanCircuitName, result.OK, fmt.Sprintf("Dry run: would %s the WAN circuit %q%s. Nothing was changed; run again with apply=true to make it",
			map[bool]string{true: "delete", false: map[bool]string{true: "replace", false: "create"}[prior != nil]}[in.Delete], in.Name, extra),
			result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Evidence: ev(nil), Limits: limits, NextActions: next})
	}
	if in.Delete {
		err = s.DeleteWanCircuit(ctx, in.NetworkID, in.Name)
	} else {
		err = s.PutWanCircuit(ctx, in.NetworkID, *want)
	}
	if err != nil {
		return result.Result{}, fmt.Errorf("the change failed, nothing is known to have changed: %w", err)
	}
	ch.Applied = true
	now, err := s.WanCircuit(ctx, in.NetworkID, in.Name)
	if err != nil {
		return result.Result{}, fmt.Errorf("the change was sent but reading the circuit back failed, so it is not proven: %w", err)
	}
	if (in.Delete && now != nil) || (!in.Delete && (now == nil || wanText(now) != wanText(want))) {
		return result.Build(editWanCircuitName, result.Failed, fmt.Sprintf("Forward accepted the change but the WAN circuit %q is not in the requested state", in.Name), result.Deterministic, cx,
			result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(map[string]any{"held": false}), Limits: limits})
	}
	finding := fmt.Sprintf("Applied: %s the WAN circuit %q", map[bool]string{true: "deleted", false: "saved"}[in.Delete], in.Name)
	if plan != nil {
		if err := s.BackdateSynthetic(ctx, in.NetworkID, "wan-circuit", in.BackdateSnapshotID); err != nil {
			return result.Build(editWanCircuitName, result.Failed, fmt.Sprintf("The circuit change was applied but the backdate to snapshot %s failed: %v", in.BackdateSnapshotID, err), result.Deterministic, cx,
				result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(map[string]any{"held": true}), Limits: append(limits, "the circuit change applies from the next snapshot; retry the backdate or wait for one"), NextActions: []string{"inspect-snapshots"}})
		}
		finding += fmt.Sprintf("; backdated to snapshot %s (%d snapshot(s) now UNPROCESSED; Forward does not reprocess them by itself: run edit-snapshot-reprocess for each one, then edit-advanced-reachability if internet exposure is needed, since that only runs after a snapshot is PROCESSED)", in.BackdateSnapshotID, len(plan.Affected))
		next = []string{"edit-snapshot-reprocess", "edit-advanced-reachability", "inspect-snapshots", "inspect-topology", "investigate-reachability"}
	}
	return result.Build(editWanCircuitName, result.OK, finding, result.Deterministic, cx,
		result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(map[string]any{"held": true}), Limits: limits, NextActions: next})
}
