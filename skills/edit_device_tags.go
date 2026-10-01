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
// dry run lists exactly those, and the undo is the opposite action on exactly those. It never creates or deletes a tag definition.
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
		return result.Build(editDeviceTagsName, result.Failed, fmt.Sprintf("The network has no tag %s; this skill applies existing tags and never creates one", strings.Join(missing, ", ")), result.Deterministic, cx,
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
	for i, p := range plans {
		var err error
		if in.Action == "add" {
			err = s.AddDeviceTags(ctx, in.NetworkID, p.Devices, []string{p.Tag})
		} else {
			err = s.RemoveDeviceTags(ctx, in.NetworkID, p.Devices, []string{p.Tag})
		}
		if err != nil {
			for j := 0; j < i; j++ {
				changes[j].Applied = true
			}
			return result.Build(editDeviceTagsName, result.Failed, fmt.Sprintf("Forward refused the change for tag %s: %v; the tags before it were changed", p.Tag, err), result.Deterministic, cx,
				result.Options{Mode: result.ModeApplied, Changes: changes, Evidence: ev(nil), Limits: append(limits, "partly applied: the changes marked applied were made, the rest were not")})
		}
		changes[i].Applied = true
	}
	after, err := s.DeviceTags(ctx, in.NetworkID)
	if err != nil {
		return result.Result{}, fmt.Errorf("the change was sent but reading the tags back failed, so it is not proven: %w", err)
	}
	now := map[string][]string{}
	for _, t := range after {
		now[t.Name] = t.Devices
	}
	for _, p := range plans {
		for _, d := range p.Devices {
			holds := slices.Contains(now[p.Tag], d)
			if (in.Action == "add") != holds {
				return result.Build(editDeviceTagsName, result.Failed, fmt.Sprintf("Forward accepted the change but tag %s on %s is not in the requested state", p.Tag, d), result.Deterministic, cx,
					result.Options{Mode: result.ModeApplied, Changes: changes, Evidence: ev(map[string]any{"held": false}), Limits: limits})
			}
		}
	}
	return result.Build(editDeviceTagsName, result.OK, fmt.Sprintf("Applied: %s on %d tag(s)", in.Action, len(plans)), result.Deterministic, cx,
		result.Options{Mode: result.ModeApplied, Changes: changes, Evidence: ev(map[string]any{"held": true}), Limits: limits, NextActions: next})
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
