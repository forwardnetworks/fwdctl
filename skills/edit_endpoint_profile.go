package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const editEndpointProfileName = "edit-endpoint-profile"

func init() { Register(editEndpointProfileName, editEndpointProfile) }

type epOID struct {
	Name string `json:"name"`
	OID  string `json:"oid"`
}

// epCreate makes a new SNMP profile as a copy of an existing one plus extra custom OIDs (an OID may be a subtree root).
type epCreate struct {
	Name     string  `json:"name"`
	CopyFrom string  `json:"copy_from"`
	AddOIDs  []epOID `json:"add_oids"`
}

type epAssign struct {
	Endpoints []string `json:"endpoints"`
	// Profile is an existing profile id, or "new" for the profile create_profile makes in this run.
	Profile string `json:"profile"`
}

type editEndpointProfileInput struct {
	NetworkID     string    `json:"network_id"`
	CreateProfile *epCreate `json:"create_profile"`
	Assign        *epAssign `json:"assign"`
	DeleteProfile string    `json:"delete_profile"`
	Apply         bool      `json:"apply"`
}

const maxEndpointsPerRun = 20

var (
	epNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	epOIDRe  = regexp.MustCompile(`^[0-9]+(\.[0-9]+)+$`)
)

// editEndpointProfile creates an SNMP endpoint profile as a copy of another plus extra OIDs, points endpoints of one network at a profile, and
// deletes a profile. A profile is an ORGANIZATION-wide object (every network sees it, Forward pushes it to the collectors), but it affects only
// the endpoints assigned to it. It returns the exact inverse as the undo: reassign each endpoint to the profile it had, then delete the new one.
func editEndpointProfile(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in editEndpointProfileInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if in.NetworkID == "" || (in.CreateProfile == nil && in.Assign == nil && in.DeleteProfile == "") {
		return result.Result{}, fmt.Errorf("%w: network_id and at least one of create_profile, assign, delete_profile are required", ErrInvalidInput)
	}
	if in.DeleteProfile != "" && (in.CreateProfile != nil || in.Assign != nil) {
		return result.Result{}, fmt.Errorf("%w: delete_profile is its own run: reassign first, then delete (Forward refuses to delete a profile in use)", ErrInvalidInput)
	}
	if c := in.CreateProfile; c != nil {
		if !epNameRe.MatchString(c.Name) || c.CopyFrom == "" || len(c.AddOIDs) == 0 {
			return result.Result{}, fmt.Errorf("%w: create_profile needs name (letters, digits, _ and -), copy_from (an SNMP profile id) and at least one add_oids entry", ErrInvalidInput)
		}
		for _, o := range c.AddOIDs {
			if !epNameRe.MatchString(o.Name) || !epOIDRe.MatchString(o.OID) {
				return result.Result{}, fmt.Errorf("%w: each add_oids entry needs name (letters, digits, _ and -) and a numeric dotted oid such as 1.3.6.1.4.1.10418.26", ErrInvalidInput)
			}
		}
	}
	if a := in.Assign; a != nil {
		if len(a.Endpoints) == 0 || len(a.Endpoints) > maxEndpointsPerRun || a.Profile == "" {
			return result.Result{}, fmt.Errorf("%w: assign needs 1 to %d endpoints and a profile (an id, or \"new\")", ErrInvalidInput, maxEndpointsPerRun)
		}
		if a.Profile == "new" && in.CreateProfile == nil {
			return result.Result{}, fmt.Errorf("%w: assign profile \"new\" needs create_profile in the same run", ErrInvalidInput)
		}
	}
	mode := result.ModeDryRun
	if in.Apply {
		mode = result.ModeApplied
	}
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}

	eps, err := s.Endpoints(ctx, in.NetworkID)
	if err != nil {
		return result.Result{}, err
	}
	profiles, err := s.EndpointProfiles(ctx)
	if err != nil {
		return result.Result{}, err
	}
	byID := map[string]forward.EndpointProfile{}
	names := map[string]bool{}
	for _, p := range profiles {
		byID[string(p.ID)] = p
		names[strings.ToLower(p.Name)] = true
	}
	epByName := map[string]forward.Endpoint{}
	for _, e := range eps {
		epByName[e.Name] = e
	}

	evd := func(extra map[string]any) []result.Evidence {
		d := map[string]any{"endpoints_read": len(eps), "profiles_read": len(profiles), "mode": mode}
		for k, v := range extra {
			d[k] = v
		}
		return []result.Evidence{result.NewEvidence(result.EvCollection, "endpointProfiles", nil, d, "")}
	}
	refuse := func(msg string) (result.Result, error) {
		return result.Build(editEndpointProfileName, result.Failed, "Refused, nothing was changed: "+msg, result.Deterministic, cx,
			result.Options{Mode: mode, Limits: []string{"nothing was sent to Forward"}, Evidence: evd(map[string]any{"refused": msg})})
	}

	// the new profile: a copy of an SNMP profile with the extra OIDs
	var newDef *forward.EndpointProfile
	if c := in.CreateProfile; c != nil {
		src, ok := byID[c.CopyFrom]
		if !ok {
			return refuse(fmt.Sprintf("there is no profile %s to copy (profiles are organization-wide; inspect-collection config lists the ones this network uses)", c.CopyFrom))
		}
		if src.Type != "SNMP" {
			return refuse(fmt.Sprintf("%s is a %s profile; this skill copies SNMP profiles only", c.CopyFrom, src.Type))
		}
		if names[strings.ToLower(c.Name)] {
			return refuse(fmt.Sprintf("a profile named %s already exists (names are unique ignoring case)", c.Name))
		}
		def := src
		def.ID, def.Name, def.Raw = "", c.Name, nil
		def.CustomOIDs = append([]forward.CustomOID{}, src.CustomOIDs...)
		have := map[string]bool{}
		for _, o := range def.CustomOIDs {
			have[strings.ToLower(o.Name)] = true
		}
		for _, o := range c.AddOIDs {
			if have[strings.ToLower(o.Name)] {
				return refuse(fmt.Sprintf("the profile already has an OID named %s (names are unique ignoring case)", o.Name))
			}
			have[strings.ToLower(o.Name)] = true
			def.CustomOIDs = append(def.CustomOIDs, forward.CustomOID{Name: o.Name, OID: o.OID})
		}
		newDef = &def
	}

	// the assignments, with each endpoint's current profile as the undo
	var moves []epMove
	if a := in.Assign; a != nil {
		target := a.Profile
		wantType := "SNMP"
		if target != "new" {
			p, ok := byID[target]
			if !ok {
				return refuse(fmt.Sprintf("there is no profile %s", target))
			}
			wantType = p.Type
		}
		for _, name := range a.Endpoints {
			e, ok := epByName[name]
			if !ok {
				return refuse(fmt.Sprintf("this network has no endpoint %s (names are matched exactly)", name))
			}
			if !strings.EqualFold(e.Type, wantType) {
				return refuse(fmt.Sprintf("endpoint %s is %s and the profile is %s; Forward rejects a mismatch", name, e.Type, wantType))
			}
			if e.ProfileID == "" {
				return refuse(fmt.Sprintf("endpoint %s has no profile now, so this run could not restore its assignment; set one back by hand if you undo", name))
			}
			if e.ProfileID != target {
				moves = append(moves, epMove{name, e.Type, e.ProfileID})
			}
		}
	}

	// a profile to delete: Forward refuses while any endpoint of the organization uses it; this network is checked here
	if id := in.DeleteProfile; id != "" {
		if _, ok := byID[id]; !ok {
			return result.Build(editEndpointProfileName, result.OK, fmt.Sprintf("There is no profile %s; nothing to delete", id), result.Deterministic, cx,
				result.Options{Mode: mode, Limits: []string{"the profile list is organization-wide"}, Evidence: evd(nil)})
		}
		var using []string
		for _, e := range eps {
			if e.ProfileID == id {
				using = append(using, e.Name)
			}
		}
		if len(using) > 0 {
			sort.Strings(using)
			return refuse(fmt.Sprintf("%d endpoint(s) of this network still use %s (%s); reassign them first (Forward also refuses while any network of the organization uses it)", len(using), id, strings.Join(clipList(using, 5), ", ")))
		}
	}

	limits := []string{
		"a profile is ORGANIZATION-wide: every network sees it and Forward pushes it to the collectors; only the endpoints assigned to it use it",
		"nothing here is collected until the next collection: the new OIDs show in NQE network.endpoints snmpOutputs after a new COLLECTION snapshot, and a vendor subtree can be large",
		"a custom OID is a numeric root: whether Forward walks it as a subtree is shown by the collected rows, not asserted here",
	}
	var changes []result.Change
	if newDef != nil {
		added := make([]string, 0, len(in.CreateProfile.AddOIDs))
		for _, o := range in.CreateProfile.AddOIDs {
			added = append(added, o.Name+"="+o.OID)
		}
		changes = append(changes, result.Change{Action: "create_endpoint_profile", Target: "organization endpoint profiles", Before: nil,
			After:      map[string]any{"name": newDef.Name, "type": "SNMP", "copied_from": in.CreateProfile.CopyFrom, "custom_oids_total": len(newDef.CustomOIDs), "added": added},
			Reversible: true, Undo: "delete_profile the new profile id (after reassigning its endpoints)"})
	}
	for _, m := range moves {
		changes = append(changes, result.Change{Action: "assign_endpoint_profile", Target: "endpoint " + m.endpoint, Before: m.from, After: in.Assign.Profile,
			Reversible: true, Undo: fmt.Sprintf("assign endpoint %s to profile %s", m.endpoint, m.from)})
	}
	if id := in.DeleteProfile; id != "" {
		p := byID[id]
		changes = append(changes, result.Change{Action: "delete_endpoint_profile", Target: "profile " + id, Before: map[string]any{"name": p.Name, "type": p.Type, "custom_oids": len(p.CustomOIDs)},
			Reversible: false, Undo: "none: recreate it with create_profile copy_from another profile; its stored connectivity test results are removed with it"})
	}
	undo := undoEndpointProfile(moves2(moves), newDef != nil)
	if len(changes) == 0 {
		return result.Build(editEndpointProfileName, result.OK, "The endpoints already use that profile; nothing to change", result.Deterministic, cx, result.Options{Mode: mode, Limits: limits, Evidence: evd(nil)})
	}
	if !in.Apply {
		return result.Build(editEndpointProfileName, result.OK, fmt.Sprintf("Dry run: would make %d change(s). Nothing was changed; run again with apply=true to make them. Undo: %s", len(changes), undo),
			result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: changes, Limits: limits, Evidence: evd(map[string]any{"undo": undo}), NextActions: []string{"inspect-collection", "investigate-collection-failure"}})
	}

	// apply: create, then assign (stopping at the first failure, reporting what was done and its undo), then delete
	newID := ""
	i := 0
	if newDef != nil {
		got, err := s.CreateEndpointProfile(ctx, *newDef)
		if err != nil {
			return result.Result{}, fmt.Errorf("the profile was not created, nothing is known to have changed: %w", err)
		}
		newID = string(got.ID)
		changes[i].Applied = true
		changes[i].After = map[string]any{"id": newID, "name": got.Name, "custom_oids_total": len(got.CustomOIDs)}
		i++
	}
	target := ""
	if in.Assign != nil {
		target = in.Assign.Profile
		if target == "new" {
			target = newID
		}
	}
	done := []epMove{}
	for _, m := range moves {
		if err := s.AssignEndpointProfile(ctx, in.NetworkID, m.endpoint, m.typ, target); err != nil {
			return result.Build(editEndpointProfileName, result.Failed, fmt.Sprintf("Stopped: assigning %s failed (%v); %d change(s) were made. Undo: %s", m.endpoint, err, len(done)+btoi(newID != ""), undoEndpointProfile(moves2(done), newID != "")),
				result.Deterministic, cx, result.Options{Mode: result.ModeApplied, Changes: changes, Limits: limits, Evidence: evd(map[string]any{"stopped_at": m.endpoint})})
		}
		changes[i].Applied = true
		changes[i].After = target
		i++
		done = append(done, m)
	}
	if id := in.DeleteProfile; id != "" {
		if err := s.DeleteEndpointProfile(ctx, id); err != nil {
			if errors.Is(err, fwd.ErrProfileInUse) {
				return result.Build(editEndpointProfileName, result.Failed, "Forward refused: "+err.Error()+" (an endpoint of another network in the organization still uses it); nothing was deleted",
					result.Deterministic, cx, result.Options{Mode: result.ModeApplied, Changes: changes, Limits: limits, Evidence: evd(nil)})
			}
			return result.Result{}, fmt.Errorf("the delete failed, nothing is known to have changed: %w", err)
		}
		changes[i].Applied = true
	}

	// read back: the profile exists with the OIDs and each endpoint points at it
	held := true
	var why []string
	if newID != "" {
		got, _, gerr := s.Client.Endpoints.GetProfile(ctx, newID)
		if gerr != nil || got == nil || len(got.CustomOIDs) != len(newDef.CustomOIDs) {
			held = false
			why = append(why, "the stored profile does not carry the requested OIDs")
		}
	}
	if len(moves) > 0 {
		now, rerr := s.Endpoints(ctx, in.NetworkID)
		if rerr != nil {
			return result.Result{}, fmt.Errorf("the changes were sent but reading the endpoints back failed, so they are not proven: %w", rerr)
		}
		cur := map[string]string{}
		for _, e := range now {
			cur[e.Name] = e.ProfileID
		}
		for _, m := range moves {
			if cur[m.endpoint] != target {
				held = false
				why = append(why, m.endpoint+" does not show the profile")
			}
		}
	}
	if !held {
		return result.Build(editEndpointProfileName, result.Failed, "Forward accepted the changes but they are not in the requested state: "+strings.Join(why, "; ")+". Undo: "+undo,
			result.Deterministic, cx, result.Options{Mode: result.ModeApplied, Changes: changes, Limits: limits, Evidence: evd(map[string]any{"held": false})})
	}
	return result.Build(editEndpointProfileName, result.OK, fmt.Sprintf("Applied %d change(s). Undo: %s", len(changes), undo), result.Deterministic, cx,
		result.Options{Mode: result.ModeApplied, Changes: changes, Limits: limits, Evidence: evd(map[string]any{"held": true, "new_profile_id": nilIfEmpty(newID)}), NextActions: []string{"edit-collection", "inspect-collection", "inspect-inventory"}})
}

// epMove is one endpoint to repoint, with the profile it has now (the undo).
type epMove struct{ endpoint, typ, from string }

// moves2 groups the endpoints by the profile they have now, for the undo text.
func moves2(ms []epMove) map[string][]string {
	out := map[string][]string{}
	for _, m := range ms {
		out[m.from] = append(out[m.from], m.endpoint)
	}
	return out
}

// undoEndpointProfile says how to restore: reassign each endpoint to the profile it had, then delete the profile that was created.
func undoEndpointProfile(from map[string][]string, created bool) string {
	var parts []string
	ids := make([]string, 0, len(from))
	for id := range from {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		eps := from[id]
		sort.Strings(eps)
		parts = append(parts, fmt.Sprintf("assign {endpoints: %s, profile: %q}", strings.Join(eps, ","), id))
	}
	if created {
		parts = append(parts, "then delete_profile the new profile id")
	}
	if len(parts) == 0 {
		return "nothing to undo"
	}
	return strings.Join(parts, "; ")
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func clipList(l []string, n int) []string {
	shown, total := result.CapRow(l, n)
	if total == len(shown) {
		return l
	}
	return append(append([]string{}, shown...), fmt.Sprintf("and %d more", total-len(shown)))
}
