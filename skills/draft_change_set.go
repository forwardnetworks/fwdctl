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

const draftChangeSetName = "edit-change-set"

func init() { Register(draftChangeSetName, draftChangeSet) }

type draftDevice struct {
	Device   string `json:"device"`
	Commands string `json:"commands"`
}

type draftAdvert struct {
	Device       string   `json:"device"`
	VRF          string   `json:"vrf"`
	ExternalPeer string   `json:"external_peer"`
	Prefix       string   `json:"prefix"`
	NextHop      string   `json:"next_hop"`
	Type         string   `json:"type"`
	Origin       string   `json:"origin"`
	LocalPref    *int64   `json:"local_pref"`
	ASPath       []int64  `json:"as_path"`
	MED          *int64   `json:"med"`
	Communities  []string `json:"communities"`
}

type draftChangeSetInput struct {
	NetworkID   string        `json:"network_id"`
	SnapshotID  string        `json:"snapshot_id"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Devices     []draftDevice `json:"devices"`
	BGP         []draftAdvert `json:"bgp_advertisements"`
	Run         bool          `json:"run"`
	Apply       bool          `json:"apply"`
}

// draftChangeSet builds a Predict change set from a change the caller wrote, so its effect can be predicted with no device
// touched. It authors nothing: the commands are staged exactly as given. The draft is created, every command is validated,
// and only a draft that validates is kept; otherwise it is deleted again. Running the prediction costs compute and leaves a
// predicted snapshot no skill can delete, so it needs run as well as apply.
func draftChangeSet(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in draftChangeSetInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if strings.TrimSpace(in.Name) == "" {
		return result.Result{}, fmt.Errorf("%w: name is required", ErrInvalidInput)
	}
	if len(in.Devices) == 0 && len(in.BGP) == 0 {
		return result.Result{}, fmt.Errorf("%w: give devices (with commands) or bgp_advertisements", ErrInvalidInput)
	}
	touched := map[string]bool{}
	for i, d := range in.Devices {
		if strings.TrimSpace(d.Device) == "" || strings.TrimSpace(d.Commands) == "" {
			return result.Result{}, fmt.Errorf("%w: devices[%d] needs device and commands", ErrInvalidInput, i)
		}
		touched[d.Device] = true
	}
	for i, a := range in.BGP {
		if a.Device == "" || a.Prefix == "" || a.ExternalPeer == "" || a.NextHop == "" {
			return result.Result{}, fmt.Errorf("%w: bgp_advertisements[%d] needs device, external_peer, prefix and next_hop", ErrInvalidInput, i)
		}
		touched[a.Device] = true
	}
	if in.Run && !in.Apply {
		return result.Result{}, fmt.Errorf("%w: run needs apply", ErrInvalidInput)
	}
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	sn, err := resolveSnapshot(ctx, s, in.NetworkID, in.SnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	if sn == nil {
		return result.NewUnknown(draftChangeSetName, "There is no processed snapshot to base a change set on", cx,
			[]string{"no such snapshot (or none processed); nothing was created"}, result.Options{NextActions: []string{"inspect-snapshots"}})
	}
	cx = fwd.Context(in.NetworkID, sn)
	base := string(sn.ID)
	if list, err := s.ChangeSets(ctx, in.NetworkID); err == nil {
		for _, c := range list {
			if c.Name == in.Name && string(c.SnapshotID) == base {
				return result.NewUnknown(draftChangeSetName, fmt.Sprintf("A change set named %q on snapshot %s already exists (id %s); nothing was created", in.Name, base, c.ID), cx,
					[]string{"an existing change set with this name and base was left alone; review it, or pick another name"}, result.Options{NextActions: []string{"verify-change"}})
			}
		}
	}
	devices := make([]string, 0, len(touched))
	for d := range touched {
		devices = append(devices, d)
	}
	plan := map[string]any{"name": in.Name, "base_snapshot_id": base, "devices": devices, "cli_devices": len(in.Devices), "bgp_advertisements": len(in.BGP), "run_prediction": in.Run}
	ch := result.Change{Action: "create_change_set", Target: fmt.Sprintf("network %s, base snapshot %s", in.NetworkID, base), After: plan, Reversible: true,
		Undo: "delete the draft change set (nothing reaches a device)"}
	ev := func(mode string, extra map[string]any) []result.Evidence {
		d := map[string]any{"mode": mode}
		for k, v := range plan {
			d[k] = v
		}
		for k, v := range extra {
			d[k] = v
		}
		return []result.Evidence{result.NewEvidence(result.EvPredict, "draftChangeSet", cx.SnapshotID, d, "")}
	}
	if !in.Apply {
		return result.Build(draftChangeSetName, result.OK, fmt.Sprintf("Dry run: would create change set %q on snapshot %s editing %d device(s), validate the commands and stage them. Nothing reaches a device; nothing was created. Run again with apply=true (and run=true to also predict it)", in.Name, base, len(devices)),
			result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Evidence: ev(result.ModeDryRun, nil), NextActions: []string{"verify-change"},
				Limits: []string{"the commands are not validated until the draft exists, so this dry run cannot say they parse"}})
	}
	cs, err := s.CreateChangeSet(ctx, in.NetworkID, in.Name, in.Description, base)
	if err != nil {
		return result.Result{}, fmt.Errorf("creating the change set failed, nothing is known to have been created: %w", err)
	}
	csID := string(cs.ID)
	ch.Applied = true
	ch.After = map[string]any{"change_set_id": csID, "name": in.Name}
	ch.Undo = fmt.Sprintf("delete change set %s (nothing reaches a device)", csID)
	changes := []result.Change{ch}
	rollback := func(why string, detail map[string]any) (result.Result, error) {
		derr := s.DeleteChangeSet(ctx, in.NetworkID, csID)
		limits := []string{why}
		if derr != nil {
			limits = append(limits, fmt.Sprintf("the draft %s could NOT be deleted and still exists: %v", csID, derr))
		} else {
			changes = append(changes, result.Change{Action: "delete_change_set", Target: "change set " + csID, Applied: true, Reversible: false})
		}
		detail["change_set_id"] = csID
		detail["draft_deleted"] = derr == nil
		return result.Build(draftChangeSetName, result.Failed, "The change set was not kept: "+why, result.Deterministic, cx,
			result.Options{Mode: result.ModeApplied, Changes: changes, Evidence: ev(result.ModeApplied, detail), Limits: limits, NextActions: []string{"verify-change"}})
	}
	for _, d := range in.Devices {
		errs, verr := s.ValidateCommands(ctx, in.NetworkID, csID, d.Device, d.Commands)
		if verr != nil {
			return rollback(fmt.Sprintf("the commands for %s could not be validated: %v", d.Device, verr), map[string]any{"device": d.Device})
		}
		if len(errs) > 0 {
			rows := make([]map[string]any, 0, len(errs))
			for _, e := range errs {
				rows = append(rows, map[string]any{"line": e.LineNumber, "type": e.ErrorType, "message": e.ErrorMsg})
			}
			return rollback(fmt.Sprintf("Forward rejected %d line(s) of the commands for %s", len(errs), d.Device), map[string]any{"device": d.Device, "command_errors": rows})
		}
	}
	for _, d := range in.Devices {
		if err := s.StageCommands(ctx, in.NetworkID, csID, d.Device, d.Commands); err != nil {
			return rollback(fmt.Sprintf("staging the commands for %s failed: %v", d.Device, err), map[string]any{"device": d.Device})
		}
	}
	byDev := map[string][]forward.BGPAdvertisement{}
	for _, a := range in.BGP {
		byDev[a.Device] = append(byDev[a.Device], forward.BGPAdvertisement{Device: a.Device, VRF: a.VRF, ExternalPeer: a.ExternalPeer, Prefix: a.Prefix, NextHop: a.NextHop,
			Type: a.Type, Origin: a.Origin, LocalPref: a.LocalPref, ASPath: a.ASPath, MED: a.MED, Communities: a.Communities})
	}
	for dev, ads := range byDev {
		if err := s.StageBGPAdvertisements(ctx, in.NetworkID, csID, dev, ads); err != nil {
			return rollback(fmt.Sprintf("staging the BGP advertisements for %s failed: %v", dev, err), map[string]any{"device": dev})
		}
	}
	var limits []string
	extra := map[string]any{"change_set_id": csID}
	finding := fmt.Sprintf("Created change set %q (id %s) on snapshot %s editing %d device(s); commands validated and staged, nothing reached a device", in.Name, csID, base, len(devices))
	if in.Run {
		p, err := s.PredictedSnapshot(ctx, in.NetworkID, csID, true)
		if err != nil {
			limits = append(limits, "the change set was created and staged but the prediction did not finish: "+err.Error())
		} else if p != nil {
			extra["predicted_snapshot_id"] = string(p.ID)
			changes = append(changes, result.Change{Action: "run_prediction", Target: "change set " + csID, After: string(p.ID), Applied: true, Reversible: false})
			finding += fmt.Sprintf("; predicted as snapshot %s", p.ID)
			limits = append(limits, "the predicted snapshot cannot be deleted by these skills and stays after the draft is removed")
		}
	} else {
		limits = append(limits, "the change was not predicted (run=false), so its effect is not measured")
	}
	return result.Build(draftChangeSetName, result.OK, finding, result.Deterministic, cx,
		result.Options{Mode: result.ModeApplied, Changes: changes, Evidence: ev(result.ModeApplied, extra), Limits: limits, NextActions: []string{"verify-change"}})
}
