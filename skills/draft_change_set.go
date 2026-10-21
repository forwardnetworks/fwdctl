package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
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
	// Config is the device's WHOLE replacement configuration (the "config file" option), instead of Commands; exactly one of the two.
	Config string `json:"config"`
}

// UnmarshalJSON accepts commands as one string of CLI text or as an array of lines (joined with newlines).
func (d *draftDevice) UnmarshalJSON(b []byte) error {
	var raw struct {
		Device   string          `json:"device"`
		Commands json.RawMessage `json:"commands"`
		Config   string          `json:"config"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	d.Device, d.Config = raw.Device, raw.Config
	if len(raw.Commands) == 0 {
		return nil
	}
	var text string
	if err := json.Unmarshal(raw.Commands, &text); err == nil {
		d.Commands = text
		return nil
	}
	var lines []string
	if err := json.Unmarshal(raw.Commands, &lines); err != nil {
		return fmt.Errorf("commands is a string of CLI text (lines separated by newlines) or an array of lines")
	}
	d.Commands = strings.Join(lines, "\n")
	return nil
}

type draftAdvert struct {
	// Action is inject (the default) or modify: stage an advertisement; withdraw: stage the loss of a received one; restore: drop what is staged for the key. Type below is the PEER type (EBGP or IBGP), not the action.
	Action       string   `json:"action"`
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

// draftFirewallRule stages a firewall security rule change (PAN-OS and similar) in the change set: add a rule after a predecessor, or remove one by its id.
type draftFirewallRule struct {
	Op          string                          `json:"op"`
	Device      string                          `json:"device"`
	ScopeID     string                          `json:"scope_id"`
	RulebaseID  string                          `json:"rulebase_id"`
	Predecessor string                          `json:"predecessor"`
	RuleID      string                          `json:"rule_id"`
	Rule        *forward.SecurityRuleDefinition `json:"rule"`
}

// draftLifecycle works on an EXISTING change set (change_set_id): rename or describe it, copy it, delete it, throw away one device's uncommitted edits, or restore an earlier version.
type draftLifecycle struct {
	Op       string `json:"op"`
	Name     string `json:"name"`
	Device   string `json:"device"`
	CommitID string `json:"commit_id"`
}

type draftChangeSetInput struct {
	ChangeSetID string              `json:"change_set_id"`
	Lifecycle   *draftLifecycle     `json:"lifecycle"`
	Confirm     string              `json:"confirm"`
	NetworkID   string              `json:"network_id"`
	SnapshotID  string              `json:"snapshot_id"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Devices     []draftDevice       `json:"devices"`
	BGP         []draftAdvert       `json:"bgp_advertisements"`
	Firewall    []draftFirewallRule `json:"firewall_rules"`
	Run         bool                `json:"run"`
	Apply       bool                `json:"apply"`
}

// draftChangeSet builds a Predict change set from a change the caller wrote, so its effect can be predicted with no device
// touched. It authors nothing: the commands are staged exactly as given. The draft is created, every command is validated,
// and only a draft that validates is kept; otherwise it is deleted again. Running the prediction costs compute and leaves a
// predicted snapshot no skill can delete, so it needs run as well as apply.
func draftChangeSet(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in draftChangeSetInput
	if err := json.Unmarshal(raw, &in); err != nil {
		if strings.Contains(err.Error(), "Specification") {
			return result.Result{}, fmt.Errorf("%w: %v. In a firewall rule the address, user, application and service fields are OBJECTS, not lists: sourceAddresses and destinationAddresses {\"subnets\":[\"8.8.8.8/32\"]} (objects and groups by id; {} means any), sourceUsers, applications and services objects too ({} means any); sourceZones and destinationZones are plain lists (empty means any); action is DENY, DROP or PERMIT", ErrInvalidInput, err)
		}
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if in.Lifecycle != nil || in.ChangeSetID != "" {
		return changeSetLifecycle(ctx, s, in)
	}
	if strings.TrimSpace(in.Name) == "" {
		return result.Result{}, fmt.Errorf("%w: name is required", ErrInvalidInput)
	}
	if len(in.Devices) == 0 && len(in.BGP) == 0 && len(in.Firewall) == 0 {
		return result.Result{}, fmt.Errorf("%w: give devices (with commands), bgp_advertisements or firewall_rules", ErrInvalidInput)
	}
	touched := map[string]bool{}
	cfgDevices := false
	for i, d := range in.Devices {
		hasCmd, hasCfg := strings.TrimSpace(d.Commands) != "", strings.TrimSpace(d.Config) != ""
		if strings.TrimSpace(d.Device) == "" || hasCmd == hasCfg {
			return result.Result{}, fmt.Errorf("%w: devices[%d] needs device and exactly one of commands (CLI to apply on top of the collected config) or config (the whole replacement configuration)", ErrInvalidInput, i)
		}
		if hasCfg {
			cfgDevices = true
		}
		touched[d.Device] = true
	}
	bgpWarn, injectWarn, droppedConfigure := false, false, false
	for i, a := range in.BGP {
		switch a.Action {
		case "", "inject", "modify":
			if a.Device == "" || a.Prefix == "" || a.ExternalPeer == "" || a.NextHop == "" {
				return result.Result{}, fmt.Errorf("%w: bgp_advertisements[%d] (%s) needs device, external_peer, prefix and next_hop", ErrInvalidInput, i, firstNonEmpty(a.Action, "inject"))
			}
		case "withdraw", "restore":
			if a.Device == "" || a.Prefix == "" || a.ExternalPeer == "" {
				return result.Result{}, fmt.Errorf("%w: bgp_advertisements[%d] (%s) needs device, external_peer and prefix (and vrf when not default)", ErrInvalidInput, i, a.Action)
			}
		default:
			return result.Result{}, fmt.Errorf("%w: bgp_advertisements[%d].action is inject, modify, withdraw or restore", ErrInvalidInput, i)
		}
		if a.Action == "modify" || a.Action == "withdraw" {
			bgpWarn = true
		}
		if a.Action == "" || a.Action == "inject" || a.Action == "modify" {
			injectWarn = true
		}
		touched[a.Device] = true
	}
	for i := range in.Devices {
		// commands are applied in configuration mode already, and Forward rejects a leading "configure terminal" as an invalid command (a patch file often starts with it)
		lines := strings.Split(in.Devices[i].Commands, "\n")
		for len(lines) > 0 {
			t := strings.ToLower(strings.TrimSpace(lines[0]))
			if t != "configure terminal" && t != "conf t" && t != "configure" && t != "" {
				break
			}
			if t != "" {
				droppedConfigure = true
			}
			lines = lines[1:]
		}
		in.Devices[i].Commands = strings.Join(lines, "\n")
	}
	cliShutdown := false
	for _, d := range in.Devices {
		if neighborShutdown.MatchString(d.Commands) {
			cliShutdown = true
		}
	}
	for i, f := range in.Firewall {
		switch {
		case f.Device == "":
			return result.Result{}, fmt.Errorf("%w: firewall_rules[%d] needs device", ErrInvalidInput, i)
		case f.Op == "add" && (f.Rule == nil || strings.TrimSpace(f.Rule.Name) == "" || f.Rule.Action == ""):
			return result.Result{}, fmt.Errorf("%w: firewall_rules[%d] (add) needs rule with name and action", ErrInvalidInput, i)
		case (f.Op == "remove" || f.Op == "move" || f.Op == "discard" || f.Op == "edit") && f.RuleID == "":
			return result.Result{}, fmt.Errorf("%w: firewall_rules[%d] (%s) needs rule_id (the rule's id, from inspect-device-files or a security rules diff)", ErrInvalidInput, i, f.Op)
		case f.Op == "edit" && (f.Rule == nil || strings.TrimSpace(f.Rule.Name) == "" || f.Rule.Action == ""):
			return result.Result{}, fmt.Errorf("%w: firewall_rules[%d] (edit) needs rule: the rule's WHOLE new definition, with name and action", ErrInvalidInput, i)
		case f.Op != "add" && f.Op != "remove" && f.Op != "edit" && f.Op != "move" && f.Op != "discard":
			return result.Result{}, fmt.Errorf("%w: firewall_rules[%d].op must be add, remove, edit, move or discard", ErrInvalidInput, i)
		}
		touched[f.Device] = true
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
	if !fwd.IsReady(sn) {
		return result.NewUnknown(draftChangeSetName, fmt.Sprintf("Base snapshot %s is %s, not PROCESSED: nothing was created", sn.ID, sn.State), cx,
			[]string{"a change set created on a snapshot that is still processing is staged and reports success but never produces a predicted snapshot; wait for PROCESSED (fwdctl wait snapshot, or inspect-snapshots) and run this again"}, result.Options{NextActions: []string{"inspect-snapshots"}})
	}
	base := string(sn.ID)
	list, lerr := s.ChangeSets(ctx, in.NetworkID)
	if lerr != nil {
		// the cheapest call that the licence and the role gate: an unlicensed organization or a login without the permission is answered here, not by an ok dry run
		if r, ok := denialResult(draftChangeSetName, lerr, cx, "listing the network's change sets (the check before creating one)"); ok {
			return r, nil
		}
	}
	var limits []string
	if missing, checked := missingSnapshotDevices(ctx, s, in.NetworkID, base, devicesOf(touched)); len(missing) > 0 {
		return result.Result{}, fmt.Errorf("%w: not in snapshot %s: %s (a change set can only edit devices of its base snapshot)", ErrInvalidInput, base, strings.Join(missing, ", "))
	} else if !checked {
		limits = append(limits, "whether the named devices exist in the base snapshot could not be checked")
	}
	if lerr == nil {
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
	plan := map[string]any{"name": in.Name, "base_snapshot_id": base, "devices": devices, "cli_devices": len(in.Devices), "bgp_advertisements": len(in.BGP), "firewall_rules": len(in.Firewall), "run_prediction": in.Run}
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
	if bgpWarn {
		limits = append(limits, "modify and withdraw need received-advertisement collection on the device (collectBgpAdvertisements on, BGP table type BOTH or ADJ_RIB_IN covering the peer): without it the route is left unchanged in the predicted snapshot and no warning is shown, so a prediction can look like it did nothing; preflight (verify-change view preflight) counts the devices that collect it. A key that matches no received advertisement is ignored when the change set runs",
			"a withdraw models the loss of a peer OUTSIDE the snapshot (an ISP); a peer that is itself a collected device needs no withdrawal")
	}
	if cfgDevices {
		limits = append(limits, "a device given config has its WHOLE running configuration replaced (send the complete configuration, at most 2 MB); Forward stores the text unchecked, so it is not validated here, and whether Predict can model the result depends on the device type, which Forward finds out when it runs")
	}
	if droppedConfigure {
		limits = append(limits, "dropped the leading \"configure terminal\" line of a device's commands: they are applied in configuration mode already, and Forward rejects that line as an invalid command")
	}
	if injectWarn {
		limits = append(limits, "inject and modify are not confirmed to take effect on every build: they can finish with status ok and change nothing (an injected prefix appeared on no device on one build, and a modify left route attributes unchanged). Pick a prefix the inbound policy permits (a denied or documentation prefix looks identical to a failure), and check the predicted snapshot (verify-change impact, or an NQE read of the prefix) before relying on one")
	}
	if cliShutdown {
		limits = append(limits, "a CLI `neighbor ... shutdown` (or `no neighbor`) removes the BGP session from the predicted snapshot, but the routes learned from it stay in the prediction (they are seeded from the collected received routes), so a de-peering what-if shows no impact; model the loss of a peer outside the snapshot with a bgp_advertisements withdraw")
	}
	if !in.Apply {
		return result.Build(draftChangeSetName, result.OK, fmt.Sprintf("Dry run: would create change set %q on snapshot %s editing %d device(s), validate the commands and stage them. Nothing reaches a device; nothing was created. Run again with apply=true (and run=true to also predict it)", in.Name, base, len(devices)),
			result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Evidence: ev(result.ModeDryRun, nil), NextActions: []string{"verify-change"},
				Limits: append(limits, "the commands are not validated until the draft exists, so this dry run cannot say they parse", "the licence and the permission to list change sets were checked, and the named devices were looked up in the base snapshot; the staging itself is not tried")})
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
		if d.Config != "" {
			continue // a whole configuration is stored as given and not validated by Forward
		}
		errs, verr := s.ValidateCommands(ctx, in.NetworkID, csID, d.Device, d.Commands)
		if verr != nil {
			return rollback(fmt.Sprintf("the commands for %s could not be validated: %v", d.Device, verr), map[string]any{"device": d.Device})
		}
		if len(errs) > 0 {
			rows := make([]map[string]any, 0, len(errs))
			for _, e := range errs {
				rows = append(rows, map[string]any{"line": e.LineNumber, "type": e.ErrorType, "message": e.ErrorMsg})
			}
			lines := strings.Split(d.Commands, "\n")
			var named []string
			for i, e := range errs {
				if i == 3 {
					named = append(named, fmt.Sprintf("and %d more", len(errs)-3))
					break
				}
				text := ""
				if e.LineNumber >= 1 && e.LineNumber <= len(lines) {
					text = strings.TrimSpace(lines[e.LineNumber-1])
				}
				named = append(named, fmt.Sprintf("line %d: %q (%s)", e.LineNumber, text, firstNonEmpty(e.ErrorMsg, e.ErrorType)))
			}
			return rollback(fmt.Sprintf("Forward rejected %d line(s) of the commands for %s: %s", len(errs), d.Device, strings.Join(named, "; ")), map[string]any{"device": d.Device, "command_errors": rows})
		}
	}
	for _, d := range in.Devices {
		if d.Config != "" {
			if _, err := s.Client.Predict.SaveDeviceConfig(ctx, in.NetworkID, csID, d.Device, d.Config); err != nil {
				return rollback(fmt.Sprintf("saving the configuration of %s failed: %v", d.Device, err), map[string]any{"device": d.Device})
			}
			continue
		}
		if err := s.StageCommands(ctx, in.NetworkID, csID, d.Device, d.Commands); err != nil {
			return rollback(fmt.Sprintf("staging the commands for %s failed: %v", d.Device, err), map[string]any{"device": d.Device})
		}
	}
	byDev := map[string][]forward.BGPAdvertisement{}
	keysDev := map[string]map[string][]forward.BGPAdvertisementKey{} // action -> device -> keys
	for _, a := range in.BGP {
		if a.Action == "withdraw" || a.Action == "restore" {
			if keysDev[a.Action] == nil {
				keysDev[a.Action] = map[string][]forward.BGPAdvertisementKey{}
			}
			keysDev[a.Action][a.Device] = append(keysDev[a.Action][a.Device], forward.BGPAdvertisementKey{VRF: firstNonEmpty(a.VRF, "default"), ExternalPeer: a.ExternalPeer, Prefix: a.Prefix})
			continue
		}
		byDev[a.Device] = append(byDev[a.Device], forward.BGPAdvertisement{Device: a.Device, VRF: a.VRF, ExternalPeer: a.ExternalPeer, Prefix: a.Prefix, NextHop: a.NextHop,
			Type: a.Type, Origin: a.Origin, LocalPref: a.LocalPref, ASPath: a.ASPath, MED: a.MED, Communities: a.Communities})
	}
	for dev, ads := range byDev {
		if err := s.StageBGPAdvertisements(ctx, in.NetworkID, csID, dev, ads); err != nil {
			return rollback(fmt.Sprintf("staging the BGP advertisements for %s failed: %v", dev, err), map[string]any{"device": dev})
		}
	}
	for dev, keys := range keysDev["withdraw"] {
		if _, err := s.Client.Predict.WithdrawBGPAdvertisements(ctx, in.NetworkID, csID, dev, keys); err != nil {
			return rollback(fmt.Sprintf("staging the BGP withdrawals for %s failed: %v", dev, err), map[string]any{"device": dev})
		}
	}
	for dev, keys := range keysDev["restore"] {
		if _, err := s.Client.Predict.RestoreBGPAdvertisements(ctx, in.NetworkID, csID, dev, keys); err != nil {
			return rollback(fmt.Sprintf("restoring the BGP advertisements for %s failed: %v", dev, err), map[string]any{"device": dev})
		}
	}
	for _, f := range in.Firewall {
		scope, rb := firstNonEmpty(f.ScopeID, forward.FirewallScopeLocal), firstNonEmpty(f.RulebaseID, forward.FirewallRulebasePrimary)
		var ferr error
		switch f.Op {
		case "add":
			_, ferr = s.Client.Predict.AddSecurityRule(ctx, in.NetworkID, csID, f.Device, scope, rb, forward.NewSecurityRule{Predecessor: f.Predecessor, Definition: *f.Rule})
		case "edit":
			_, ferr = s.Client.Predict.EditSecurityRule(ctx, in.NetworkID, csID, f.Device, scope, rb, f.RuleID, *f.Rule)
		case "move":
			_, ferr = s.Client.Predict.MoveSecurityRule(ctx, in.NetworkID, csID, f.Device, scope, rb, f.RuleID, f.Predecessor)
		case "discard":
			_, ferr = s.Client.Predict.DiscardSecurityRuleChange(ctx, in.NetworkID, csID, f.Device, scope, rb, f.RuleID)
		default:
			_, ferr = s.Client.Predict.RemoveSecurityRule(ctx, in.NetworkID, csID, f.Device, scope, rb, f.RuleID)
		}
		if ferr != nil {
			return rollback(fmt.Sprintf("staging the firewall rule change (%s) for %s failed: %v", f.Op, f.Device, ferr), map[string]any{"device": f.Device})
		}
	}
	extra := map[string]any{"change_set_id": csID}
	if len(in.Firewall) > 0 {
		diffs := map[string]any{}
		seen := map[string]bool{}
		for _, f := range in.Firewall {
			if seen[f.Device] {
				continue
			}
			seen[f.Device] = true
			if d, derr := s.SecurityRulesDiff(ctx, in.NetworkID, csID, f.Device); derr == nil && d != nil {
				diffs[f.Device] = d.Entries
			} else if derr != nil {
				limits = append(limits, fmt.Sprintf("the staged rule changes of %s could not be read back: %v", f.Device, derr))
			}
		}
		extra["firewall_rules_diff"] = diffs
	}
	finding := fmt.Sprintf("Created change set %q (id %s) on snapshot %s editing %d device(s); commands validated and staged, nothing reached a device", in.Name, csID, base, len(devices))
	for _, a := range in.BGP {
		if a.Action == "withdraw" {
			finding += "; a withdraw only changes the prediction for a peer outside the snapshot (a peer that is itself a collected device needs none, and a withdraw from it changes nothing)"
			break
		}
	}
	if in.Run {
		run, err := s.RunPrediction(ctx, in.NetworkID, csID, "fwdctl")
		p := run.Done
		if err != nil || p == nil {
			why := "no predicted snapshot was returned"
			if err != nil {
				why = err.Error()
			}
			limits = append(limits, "the change set was created and staged but the prediction did not finish: "+why)
			switch {
			case run.ID != "" && strings.EqualFold(run.State, "FAILED"), run.ID != "" && (strings.EqualFold(run.State, "CANCELED") || strings.EqualFold(run.State, "TIMED_OUT")):
				extra["predicted_snapshot_id"] = run.ID
				finding += fmt.Sprintf("; the prediction was started but snapshot %s ended %s (no result): read why with inspect-snapshots, then run the change set again", run.ID, strings.ToUpper(run.State))
			case run.ID != "":
				extra["predicted_snapshot_id"], extra["predicted_snapshot_state"] = run.ID, run.State
				finding += "; processing: " + s.Processing(ctx, run.ID).Line()
				finding += fmt.Sprintf("; NOT predicted yet: snapshot %s was started (state %s) and the wait ended (%s); a long prediction finishes after this call returns, so wait for it with `fwdctl wait snapshot --network %s --snapshot %s` or read verify-change view list before trusting any result (the wait never cancels it: to stop its jobs, edit-snapshot action cancel with snapshot_id %s, a dry run first, needs an ADMINISTER_SYSTEM credential)", run.ID, run.State, why, in.NetworkID, run.ID, run.ID)
			default:
				finding += "; NOT predicted: the prediction could not be started (" + why + ")"
			}
		} else {
			extra["predicted_snapshot_id"] = string(p.ID)
			changes = append(changes, result.Change{Action: "run_prediction", Target: "change set " + csID, After: string(p.ID), Applied: true, Reversible: false})
			finding += fmt.Sprintf("; predicted as snapshot %s", p.ID)
			if run.FirstFailed {
				finding += " (the wait first read FAILED and a re-read found it PROCESSED)"
			}
			limits = append(limits, "the predicted snapshot stays after the draft is removed; edit-snapshot action delete (confirm = the snapshot id) is the way to remove it, and Forward decides whether it allows it (not verified for a predicted snapshot)")
		}
	} else {
		limits = append(limits, "the change was not predicted (run=false), so its effect is not measured")
	}
	return result.Build(draftChangeSetName, result.OK, finding, result.Deterministic, cx,
		result.Options{Mode: result.ModeApplied, Changes: changes, Evidence: ev(result.ModeApplied, extra), Limits: limits, NextActions: []string{"verify-change"}})
}

// neighborShutdown matches a BGP neighbor shut down or removed in CLI text (IOS, EOS, NX-OS style).
var neighborShutdown = regexp.MustCompile(`(?im)^\s*(neighbor\s+\S+\s+shutdown\b|no\s+neighbor\b)`)

func devicesOf(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for d := range set {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// maxDeviceExistenceChecks bounds the per-device lookups of a change set's devices in its base snapshot (each is a keyed lookup by exact name).
const maxDeviceExistenceChecks = 20

// missingSnapshotDevices returns the devices not found in the snapshot, by an exact-name lookup each. checked is false when the lookups could not be made (or there were too many): then nothing is claimed.
func missingSnapshotDevices(ctx context.Context, s *fwd.Session, networkID, snapshotID string, names []string) (missing []string, checked bool) {
	if len(names) == 0 || len(names) > maxDeviceExistenceChecks {
		return nil, false
	}
	for _, n := range names {
		q := fmt.Sprintf("foreach device in network.devices where device.name == %q select {name: device.name}", n)
		rows, _, _, err := s.RunNQEAll(ctx, networkID, snapshotID, q, 2)
		if err != nil {
			return nil, false
		}
		if len(rows) == 0 {
			missing = append(missing, n)
		}
	}
	return missing, true
}
