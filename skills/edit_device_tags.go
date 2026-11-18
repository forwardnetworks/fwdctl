package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const editDeviceTagsName = "edit-device-tags"

func init() { Register(editDeviceTagsName, editDeviceTags) }

const maxTagDevices = 200

type editDeviceTagsInput struct {
	NetworkID string   `json:"network_id"`
	Action    string   `json:"action"`
	Devices   []string `json:"devices"`
	Tags      []string `json:"tags"`
	Apply     bool     `json:"apply"`
}

// editDeviceTags puts existing tags on devices or takes them off. It reads the current membership first, so it changes only the
// device and tag pairs that differ (an add of a tag a device already has is not repeated, a removal of one it lacks is not sent), the
// dry run lists exactly those, and the undo is the opposite action on exactly those. It never creates or deletes a tag definition (edit-network does).
func editDeviceTags(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in editDeviceTagsInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if in.Action != "add" && in.Action != "remove" {
		return result.Result{}, fmt.Errorf("%w: action is add or remove", ErrInvalidInput)
	}
	in.Devices, in.Tags = cleanNames(in.Devices), cleanNames(in.Tags)
	if in.NetworkID == "" || len(in.Devices) == 0 || len(in.Tags) == 0 {
		return result.Result{}, fmt.Errorf("%w: network_id, devices and tags are required", ErrInvalidInput)
	}
	if len(in.Devices) > maxTagDevices {
		return result.Result{}, fmt.Errorf("%w: at most %d devices per run", ErrInvalidInput, maxTagDevices)
	}
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	mode := result.ModeDryRun
	if in.Apply {
		mode = result.ModeApplied
	}
	next := []string{"inspect-inventory"}
	defs, err := s.DeviceTags(ctx, in.NetworkID)
	if err != nil {
		return result.Result{}, err
	}
	have := map[string][]string{}
	for _, t := range defs {
		have[t.Name] = t.Devices
	}
	var missing []string
	for _, t := range in.Tags {
		if _, ok := have[t]; !ok {
			missing = append(missing, t)
		}
	}
	if in.Action == "add" && len(missing) > 0 {
		return result.Build(editDeviceTagsName, result.Failed, fmt.Sprintf("The network has no tag %s; this skill applies existing tags and never creates one; create it first with edit-network (object tag, action create, name)", strings.Join(missing, ", ")), result.Deterministic, cx,
			result.Options{Mode: mode, NextActions: next, Evidence: []result.Evidence{result.NewEvidence(result.EvState, "deviceTags", nil, map[string]any{"missing_tags": missing, "existing_tags": len(have)}, "")}, Limits: []string{"nothing was changed; create the tag in Forward first (a tag definition cannot be removed through the API, so this skill does not create them)"}})
	}
	// the pairs that actually change, per tag
	type plan struct {
		Tag     string   `json:"tag"`
		Devices []string `json:"devices"`
	}
	var plans []plan
	var changes []result.Change
	opposite := map[string]string{"add": "remove", "remove": "add"}[in.Action]
	for _, t := range in.Tags {
		var eff []string
		for _, d := range in.Devices {
			holds := slices.Contains(have[t], d)
			if (in.Action == "add" && !holds) || (in.Action == "remove" && holds) {
				eff = append(eff, d)
			}
		}
		if len(eff) == 0 {
			continue
		}
		sort.Strings(eff)
		plans = append(plans, plan{t, eff})
		changes = append(changes, result.Change{Action: in.Action + "_tag", Target: fmt.Sprintf("tag %s on %s", t, strings.Join(eff, ", ")), Before: strings.Join(have[t], ","), Reversible: true,
			Undo: fmt.Sprintf("run edit-device-tags with action %q, tags [%q], devices [%s] and apply=true", opposite, t, quoteJoin(eff))})
	}
	ev := func(extra map[string]any) []result.Evidence {
		d := map[string]any{"action": in.Action, "changes": plans, "mode": mode}
		for k, v := range extra {
			d[k] = v
		}
		return []result.Evidence{result.NewEvidence(result.EvState, "deviceTags", nil, d, "")}
	}
	pairCount := 0
	for _, p := range plans {
		pairCount += len(p.Devices)
	}
	limits := []string{"a tag change takes effect in the network's next snapshot; the devices must be collection sources"}
	if len(plans) == 0 {
		return result.Build(editDeviceTagsName, result.OK, "Every requested device already is in the requested state; nothing to change", result.Deterministic, cx,
			result.Options{Mode: mode, Evidence: ev(nil), NextActions: next})
	}
	if !in.Apply {
		return result.Build(editDeviceTagsName, result.OK, fmt.Sprintf("Dry run: would %s %d tag(s) on devices (%d pair(s) differ). Nothing was changed; run again with apply=true to make it", in.Action, len(plans), pairCount),
			result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: changes, Evidence: ev(nil), Limits: limits, NextActions: next})
	}
	// Apply tag by tag. A batch Forward refuses (for example when one device is not a collection source) is retried one device at a time, so one bad device does not stop the rest and
	// the refusal is reported per device with Forward's own words.
	send := func(devs []string, tag string) error {
		if in.Action == "add" {
			return s.AddDeviceTags(ctx, in.NetworkID, devs, []string{tag})
		}
		return s.RemoveDeviceTags(ctx, in.NetworkID, devs, []string{tag})
	}
	type refusal struct {
		Tag    string `json:"tag"`
		Device string `json:"device"`
		Reason string `json:"reason"`
		// ParentDevice is the physical device a refused virtual device (a firewall virtual system, a wireless access point under its controller) belongs to, read from the newest processed
		// snapshot (device.system.physicalName). Empty when the device is not virtual there or the read failed.
		ParentDevice string `json:"parent_device,omitempty"`
	}
	var refused []refusal
	sent := make([][]string, len(plans)) // the devices Forward accepted, per plan
	for i, p := range plans {
		if err := send(p.Devices, p.Tag); err == nil {
			sent[i] = p.Devices
			continue
		} else if len(p.Devices) == 1 {
			refused = append(refused, refusal{p.Tag, p.Devices[0], err.Error(), ""})
			continue
		}
		for _, d := range p.Devices {
			if err := send([]string{d}, p.Tag); err != nil {
				refused = append(refused, refusal{p.Tag, d, err.Error(), ""})
			} else {
				sent[i] = append(sent[i], d)
			}
		}
	}
	// an applied result lists only the changes that were made: one change per tag, naming the devices Forward accepted
	var made []result.Change
	for i, p := range plans {
		if len(sent[i]) == 0 {
			continue
		}
		made = append(made, result.Change{Action: in.Action + "_tag", Target: fmt.Sprintf("tag %s on %s", p.Tag, strings.Join(sent[i], ", ")), Before: strings.Join(have[p.Tag], ","), Applied: true, Reversible: true,
			Undo: fmt.Sprintf("run edit-device-tags with action %q, tags [%q], devices [%s] and apply=true", opposite, p.Tag, quoteJoin(sent[i]))})
	}
	if len(refused) > 0 {
		parents := physicalParents(ctx, s, in.NetworkID)
		for i := range refused {
			if p := parents[refused[i].Device]; p != "" {
				refused[i].ParentDevice = p
			}
		}
	}
	var notHeld []refusal
	if len(made) > 0 {
		after, err := s.DeviceTags(ctx, in.NetworkID)
		if err != nil {
			return result.Result{}, fmt.Errorf("the change was sent but reading the tags back failed, so it is not proven: %w", err)
		}
		now := map[string][]string{}
		for _, t := range after {
			now[t.Name] = t.Devices
		}
		for i, p := range plans {
			for _, d := range sent[i] {
				if (in.Action == "add") != slices.Contains(now[p.Tag], d) {
					notHeld = append(notHeld, refusal{p.Tag, d, "Forward accepted the request but the tag is not in the requested state afterwards (it may skip a device that is not a collection source)", ""})
				}
			}
		}
	}
	applied := pairCount - len(refused)
	extra := map[string]any{"applied_pairs": applied - len(notHeld), "refused": refused, "not_in_requested_state": notHeld}
	if len(refused) > 0 || len(notHeld) > 0 {
		limits = append(limits, "partly applied: the changes listed were made; the refused or unproven devices are in the evidence with the reason (and, for a virtual device, parent_device: the physical device to tag instead), and the rest of the batch was applied",
			"a tag set here is written at Forward's staging instant (after every existing snapshot), so NQE (device.tagNames) shows it only in a snapshot collected after the change; a snapshot already processed does not")
		return result.Build(editDeviceTagsName, result.Failed, fmt.Sprintf("%s on %d device-tag pair(s) of %d: %d refused by Forward, %d accepted but not in the requested state; see the evidence for each device and reason",
			in.Action, applied-len(notHeld), pairCount, len(refused), len(notHeld)), result.Deterministic, cx,
			result.Options{Mode: result.ModeApplied, Changes: made, Evidence: ev(extra), Limits: limits, NextActions: next})
	}
	return result.Build(editDeviceTagsName, result.OK, fmt.Sprintf("Applied: %s on %d tag(s)", in.Action, len(plans)), result.Deterministic, cx,
		result.Options{Mode: result.ModeApplied, Changes: made, Evidence: ev(map[string]any{"held": true, "applied_pairs": applied}), Limits: limits, NextActions: next})
}

func cleanNames(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func quoteJoin(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(q, ", ")
}

// physicalParents maps a modeled device that is part of a physical one (a firewall virtual system, an access point listed under its controller) to the physical device, from the newest processed
// snapshot's device.system.physicalName. It returns nothing when the snapshot or the read is unavailable: the parent is a help, not part of the result.
func physicalParents(ctx context.Context, s *fwd.Session, networkID string) map[string]string {
	snap, err := resolveSnapshot(ctx, s, networkID, "")
	if err != nil || !fwd.IsReady(snap) {
		return nil
	}
	rows, _, _, err := s.RunNQEAll(ctx, networkID, fwd.SnapshotID(fwd.Context(networkID, snap)), physicalParentsQuery, 200000)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, r := range rows {
		n, _ := r["name"].(string)
		p, _ := r["physical"].(string)
		if n != "" && p != "" && p != n {
			out[n] = p
		}
	}
	return out
}

const physicalParentsQuery = `foreach device in network.devices
where device.system.physicalName != device.name
select { name: device.name, physical: device.system.physicalName }`
