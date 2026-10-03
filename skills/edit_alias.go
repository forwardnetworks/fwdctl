package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const editAliasName = "edit-alias"

func init() { Register(editAliasName, editAlias) }

type editAliasInput struct {
	NetworkID  string          `json:"network_id"`
	SnapshotID string          `json:"snapshot_id"`
	Action     string          `json:"action"`
	Name       string          `json:"name"`
	Definition json.RawMessage `json:"definition"`
	Apply      bool            `json:"apply"`
}

// aliasDefinition is the type-specific part of an alias, in the field names Forward uses; fields another type owns are refused by the SDK.
type aliasDefinition struct {
	Type            string              `json:"type"`
	Values          []string            `json:"values"`
	Locations       []string            `json:"locations"`
	VLANIDs         []string            `json:"vlanIds"`
	VLANIntfTypes   []string            `json:"vlanIntfTypes"`
	IsExposurePoint *bool               `json:"isExposurePoint"`
	HeaderValues    map[string][]string `json:"headerValues"`
	Devices         []string            `json:"devices"`
	EdgeNodes       []string            `json:"edgeNodes"`
}

func (d aliasDefinition) builder(name string) forward.AliasBuilder {
	return forward.AliasBuilder{Name: name, Type: strings.ToUpper(d.Type), Values: d.Values, Locations: d.Locations, VLANIDs: d.VLANIDs, VLANIntfTypes: d.VLANIntfTypes,
		IsExposurePoint: d.IsExposurePoint, HeaderValues: d.HeaderValues, Devices: d.Devices, EdgeNodes: d.EdgeNodes}
}

// aliasBody is the definition Forward would store for a builder, as a generic map (the SDK writes it in Forward's own shape).
func aliasBody(b forward.AliasBuilder) (map[string]any, error) {
	raw, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	return m, json.Unmarshal(raw, &m)
}

// aliasHolds says whether the stored alias already has every field of the wanted body (Forward adds the alias's resolved value and ids, so equality is one-way).
func aliasHolds(stored *forward.Alias, want map[string]any) bool {
	var have map[string]any
	if json.Unmarshal(stored.Definition, &have) != nil {
		return false
	}
	for k, v := range want {
		wj, _ := json.Marshal(v)
		var wv any
		_ = json.Unmarshal(wj, &wv)
		if !reflect.DeepEqual(have[k], wv) {
			return false
		}
	}
	return true
}

// aliasSummary is the stored alias without Forward's resolved value, for before and after.
func aliasSummary(a *forward.Alias) map[string]any {
	if a == nil {
		return nil
	}
	var m map[string]any
	_ = json.Unmarshal(a.Definition, &m)
	out := map[string]any{"name": a.Name, "type": a.Type}
	for _, k := range []string{"values", "locations", "vlanIds", "vlanIntfTypes", "isExposurePoint", "devices", "edgeNodes"} {
		if v, ok := m[k]; ok {
			out[k] = v
		}
	}
	return out
}

// editAlias creates, replaces or deactivates one alias: a named set of hosts, devices, interfaces, traffic headers or logical networks that checks and path searches refer to by name.
// An alias applies to the snapshot it is put on and to every LATER snapshot of the network, so put it on the newest snapshot.
func editAlias(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in editAliasInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return result.Result{}, fmt.Errorf("%w: name is required (the name checks refer to)", ErrInvalidInput)
	}
	var def aliasDefinition
	var wantBody map[string]any
	switch in.Action {
	case "put":
		if len(in.Definition) == 0 {
			return result.Result{}, fmt.Errorf("%w: put needs a definition: {\"type\": HOSTS | DEVICES | INTERFACES | HEADERS | LOGICAL_NETWORK, and that type's fields}", ErrInvalidInput)
		}
		dec := json.NewDecoder(strings.NewReader(string(in.Definition)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&def); err != nil {
			return result.Result{}, fmt.Errorf("%w: definition: %v (fields: type, values, locations, vlanIds, vlanIntfTypes, isExposurePoint, headerValues, devices, edgeNodes)", ErrInvalidInput, err)
		}
		var err error
		if wantBody, err = aliasBody(def.builder(in.Name)); err != nil {
			return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
	case "deactivate":
		if len(in.Definition) != 0 {
			return result.Result{}, fmt.Errorf("%w: deactivate takes name only", ErrInvalidInput)
		}
	default:
		return result.Result{}, fmt.Errorf("%w: action must be put or deactivate", ErrInvalidInput)
	}
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	sn, err := resolveSnapshot(ctx, s, in.NetworkID, in.SnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	if sn == nil {
		return result.NewUnknown(editAliasName, "There is no processed snapshot to put an alias on", cx,
			[]string{"no such snapshot (or none processed); nothing was changed"}, result.Options{NextActions: []string{"inspect-snapshots"}})
	}
	cx = fwd.Context(in.NetworkID, sn)
	sid := string(sn.ID)
	mode := result.ModeDryRun
	if in.Apply {
		mode = result.ModeApplied
	}
	existing, err := s.Alias(ctx, sid, in.Name)
	if err != nil {
		return result.Result{}, err
	}
	before := aliasSummary(existing)
	limits := []string{
		"an alias applies to snapshot " + sid + " and every LATER snapshot of the network, not to earlier ones: put it on the newest snapshot and it carries on to the ones collected afterwards",
		"Forward checks the values themselves when it saves them (a 400 for one it cannot parse); a check that refers to this name by an alias that does not exist, or was deactivated, errors or finds nothing",
		"a snapshot forked for Predict needs the organization property PREDICT_UNSANDBOXED before aliases can change there",
	}
	ev := func(extra map[string]any) []result.Evidence {
		d := map[string]any{"snapshot_id": sid, "name": in.Name, "action": in.Action, "mode": mode, "before": before}
		for k, v := range extra {
			d[k] = v
		}
		return []result.Evidence{result.NewEvidence(result.EvTopology, "aliases", cx.SnapshotID, d, "")}
	}
	next := []string{"inspect-topology", "inspect-checks"}
	if in.Action == "deactivate" {
		if existing == nil {
			return result.Build(editAliasName, result.OK, fmt.Sprintf("No alias %q is active at snapshot %s; nothing to deactivate", in.Name, sid), result.Deterministic, cx,
				result.Options{Mode: mode, Evidence: ev(nil), Limits: limits, NextActions: next})
		}
		ch := result.Change{Action: "deactivate_alias", Target: fmt.Sprintf("alias %s at snapshot %s", in.Name, sid), Before: before, After: nil, Reversible: true,
			Undo: "put the alias again with the definition in before (edit-alias action put): it becomes a NEW alias with the same name, active from this snapshot on"}
		limits = append(limits, "deactivating ends the alias at this snapshot's creation time, so for it and every later snapshot; earlier snapshots still have it, and the name can only be used again by creating a new alias")
		if !in.Apply {
			return result.Build(editAliasName, result.OK, fmt.Sprintf("Dry run: would deactivate alias %q (%s) from snapshot %s on. Nothing was changed; run again with apply=true", in.Name, existing.Type, sid), result.Deterministic, cx,
				result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Evidence: ev(nil), Limits: limits, NextActions: next})
		}
		if _, err := s.DeactivateAlias(ctx, sid, in.Name); err != nil {
			return result.Result{}, fmt.Errorf("the deactivation failed, nothing is known to have changed: %w", err)
		}
		ch.Applied = true
		now, rerr := s.Alias(ctx, sid, in.Name)
		if rerr != nil {
			return result.Result{}, fmt.Errorf("the deactivation was sent but reading the alias back failed, so it is not proven: %w", rerr)
		}
		if now != nil {
			return result.Build(editAliasName, result.Failed, fmt.Sprintf("Forward accepted the deactivation but alias %q is still active at snapshot %s", in.Name, sid), result.Deterministic, cx,
				result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(nil), Limits: limits})
		}
		return result.Build(editAliasName, result.OK, fmt.Sprintf("Deactivated alias %q from snapshot %s on", in.Name, sid), result.Deterministic, cx,
			result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(nil), Limits: limits, NextActions: next})
	}
	// put
	after := map[string]any{"name": in.Name}
	for k, v := range wantBody {
		after[k] = v
	}
	if existing != nil && aliasHolds(existing, wantBody) && existing.Type == strings.ToUpper(def.Type) {
		return result.Build(editAliasName, result.OK, fmt.Sprintf("Alias %q already has that definition at snapshot %s; nothing to change", in.Name, sid), result.Deterministic, cx,
			result.Options{Mode: mode, Evidence: ev(map[string]any{"after": after}), Limits: limits, NextActions: next})
	}
	ch := result.Change{Action: "put_alias", Target: fmt.Sprintf("alias %s at snapshot %s", in.Name, sid), Before: before, After: after, Reversible: true}
	verb := "create"
	if existing != nil {
		verb = "replace the definition of"
		ch.Undo = "put the alias again with the definition in before (edit-alias action put)"
		limits = append(limits, "replacing changes only the name and definition; a check that uses this alias sees the new members from this snapshot on")
	} else {
		ch.Undo = "deactivate the alias (edit-alias action deactivate): it ends from this snapshot on, and the name can then only be reused as a new alias"
	}
	if !in.Apply {
		return result.Build(editAliasName, result.OK, fmt.Sprintf("Dry run: would %s alias %q (%s) at snapshot %s. Nothing was changed; run again with apply=true", verb, in.Name, strings.ToUpper(def.Type), sid), result.Deterministic, cx,
			result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Evidence: ev(map[string]any{"after": after}), Limits: limits, NextActions: next})
	}
	if _, err := s.PutAlias(ctx, sid, def.builder(in.Name)); err != nil {
		return result.Result{}, fmt.Errorf("the alias was not saved, nothing is known to have changed: %w", err)
	}
	ch.Applied = true
	now, rerr := s.Alias(ctx, sid, in.Name)
	if rerr != nil {
		return result.Result{}, fmt.Errorf("the alias was sent but reading it back failed, so it is not proven: %w", rerr)
	}
	if now == nil || !aliasHolds(now, wantBody) {
		return result.Build(editAliasName, result.Failed, fmt.Sprintf("Forward accepted the alias but snapshot %s does not show alias %q with that definition", sid, in.Name), result.Deterministic, cx,
			result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(map[string]any{"after": aliasSummary(now)}), Limits: limits})
	}
	return result.Build(editAliasName, result.OK, fmt.Sprintf("Saved alias %q (%s) at snapshot %s; it applies from that snapshot on", in.Name, now.Type, sid), result.Deterministic, cx,
		result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(map[string]any{"after": aliasSummary(now)}), Limits: limits, NextActions: next})
}
