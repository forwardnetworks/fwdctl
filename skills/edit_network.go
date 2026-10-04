package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const editNetworkName = "edit-network"

func init() { Register(editNetworkName, editNetwork) }

type editNetworkInput struct {
	NetworkID  string          `json:"network_id"`
	Object     string          `json:"object"`
	Action     string          `json:"action"`
	Name       string          `json:"name"`
	Definition json.RawMessage `json:"definition"`
	Confirm    string          `json:"confirm"`
	Apply      bool            `json:"apply"`
}

// networkPlan is one change ready to show and, with apply, to make: how it reads now, how it would read, how to put it back, and the one call.
type networkPlan struct {
	target, action string
	before, after  any
	reversible     bool
	undo           string
	limits         []string
	confirm        string // the exact value apply needs ("" when none)
	do             func(ctx context.Context) error
	verify         func(ctx context.Context) (bool, any, error) // reads back; ok says the change is visible
}

// editNetwork manages the objects around a network: the network itself (create, rename, note, delete), its locations and device clusters, and its device tag definitions. The
// type-specific body is one `definition` object, as edit-alias does, and every change is a dry run unless apply is true with a before, an after and the undo. Deleting a network, a
// tag definition or anything else that cannot be put back needs confirm.
func editNetwork(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in editNetworkInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	cx := result.Context{NetworkID: in.NetworkID, Scope: "account", State: "current"}
	var plan *networkPlan
	var err error
	switch in.Object {
	case "network":
		plan, err = planNetwork(ctx, s, in)
	case "location":
		plan, err = planLocation(ctx, s, in)
	case "cluster":
		plan, err = planCluster(ctx, s, in)
	case "tag":
		plan, err = planTag(ctx, s, in)
	default:
		return result.Result{}, fmt.Errorf("%w: object must be network, location, cluster or tag", ErrInvalidInput)
	}
	if err != nil {
		return result.Result{}, err
	}
	return finishPlan(ctx, editNetworkName, cx, plan, in.Object, in.Action, in.Apply, in.Confirm)
}

// finishPlan shows a plan as a dry run or, with apply, makes the change and reads it back. A nil plan is "nothing matches". It is shared by the edit skills whose body is a
// networkPlan (edit-network, edit-source, edit-platform).
func finishPlan(ctx context.Context, name string, cx result.Context, plan *networkPlan, object, action string, apply bool, confirm string) (result.Result, error) {
	if plan == nil {
		return result.NewUnknown(name, "Nothing matches, so nothing was planned", cx, []string{"the object named was not found; nothing was changed"}, result.Options{NextActions: []string{"inspect-platform"}})
	}
	mode := result.ModeDryRun
	if apply {
		mode = result.ModeApplied
	}
	if apply && plan.confirm != "" && confirm != plan.confirm {
		return result.Result{}, fmt.Errorf("%w: %s cannot be undone; set confirm to the exact value %q to apply", ErrInvalidInput, plan.action, plan.confirm)
	}
	if plan.confirm == "" && confirm != "" {
		return result.Result{}, fmt.Errorf("%w: confirm belongs to the actions that cannot be undone", ErrInvalidInput)
	}
	ch := result.Change{Action: plan.action, Target: plan.target, Before: plan.before, After: plan.after, Reversible: plan.reversible, Undo: plan.undo}
	ev := func(extra map[string]any) []result.Evidence {
		d := map[string]any{"object": object, "action": action, "mode": mode, "before": plan.before, "after": plan.after}
		for k, v := range extra {
			d[k] = v
		}
		return []result.Evidence{result.NewEvidence(result.EvState, name, nil, d, "")}
	}
	next := []string{"inspect-platform", "inspect-collection"}
	if !apply {
		hint := ""
		if plan.confirm != "" {
			hint = fmt.Sprintf(" and confirm=%q", plan.confirm)
		}
		return result.Build(name, result.OK, fmt.Sprintf("Dry run: would %s. Nothing was changed; run again with apply=true%s", plan.target, hint), result.Deterministic, cx,
			result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Evidence: ev(nil), Limits: plan.limits, NextActions: next})
	}
	if err := plan.do(ctx); err != nil {
		return result.Result{}, fmt.Errorf("the change failed, nothing is known to have changed: %w", err)
	}
	ch.Applied = true
	if plan.verify != nil {
		ok, got, verr := plan.verify(ctx)
		if verr != nil {
			return result.Result{}, fmt.Errorf("the change was sent but reading it back failed, so it is not proven: %w", verr)
		}
		if !ok {
			return result.Build(name, result.Failed, fmt.Sprintf("Forward accepted the change but the read-back does not show it (%s)", plan.target), result.Deterministic, cx,
				result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(map[string]any{"read_back": got}), Limits: plan.limits})
		}
	}
	return result.Build(name, result.OK, fmt.Sprintf("Done: %s", plan.target), result.Deterministic, cx,
		result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(nil), Limits: plan.limits, NextActions: next})
}

// decodeDefinition reads the definition refusing fields it does not know.
func decodeDefinition(raw json.RawMessage, into any, fields string) error {
	if len(raw) == 0 {
		return fmt.Errorf("%w: this needs a definition: {%s}", ErrInvalidInput, fields)
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("%w: definition: %v (fields: %s)", ErrInvalidInput, err, fields)
	}
	return nil
}

func planNetwork(ctx context.Context, s *fwd.Session, in editNetworkInput) (*networkPlan, error) {
	switch in.Action {
	case "create":
		if in.NetworkID != "" || strings.TrimSpace(in.Name) == "" || len(in.Definition) > 0 {
			return nil, fmt.Errorf("%w: create takes name only (no network_id, no definition)", ErrInvalidInput)
		}
		name := strings.TrimSpace(in.Name)
		var created *forward.Network
		list, _, err := s.Client.Networks.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, n := range list {
			if strings.EqualFold(n.Name, name) {
				return nil, fmt.Errorf("%w: a network named %q already exists (id %s)", ErrInvalidInput, n.Name, n.ID)
			}
		}
		return &networkPlan{target: "create network " + name, action: "create_network", before: nil, after: map[string]any{"name": name}, reversible: true, undo: "delete the new network (edit-network object network action delete, confirm its id)",
			do: func(ctx context.Context) error {
				var err error
				created, _, err = s.Client.Networks.Create(ctx, name)
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				return created != nil && created.ID != "", map[string]any{"id": idOf(created)}, nil
			}}, nil
	case "update", "delete":
	default:
		return nil, fmt.Errorf("%w: object network takes action create, update or delete", ErrInvalidInput)
	}
	if in.NetworkID == "" {
		return nil, fmt.Errorf("%w: network_id is required", ErrInvalidInput)
	}
	list, _, err := s.Client.Networks.List(ctx)
	if err != nil {
		return nil, err
	}
	var cur *forward.Network
	for i := range list {
		if string(list[i].ID) == in.NetworkID {
			cur = &list[i]
		}
	}
	if cur == nil {
		return nil, nil
	}
	before := map[string]any{"id": string(cur.ID), "name": cur.Name, "note": nilIfEmpty(cur.Note)}
	if in.Action == "delete" {
		if len(in.Definition) > 0 || in.Name != "" {
			return nil, fmt.Errorf("%w: delete takes network_id and confirm only", ErrInvalidInput)
		}
		snaps := 0
		if sl, serr := s.Snapshots(ctx, in.NetworkID); serr == nil {
			snaps = len(sl)
		}
		before["snapshots"] = snaps
		return &networkPlan{target: fmt.Sprintf("delete network %q (%s) with its %d snapshot(s)", cur.Name, cur.ID, snaps), action: "delete_network", before: before, after: nil, reversible: false, confirm: in.NetworkID,
			undo:   "none: the network, its snapshots, checks and settings are gone (a backup restore may bring snapshots back)",
			limits: []string{"deleting a network removes everything in it; a network that is a workspace's parent may be refused by Forward"},
			do:     func(ctx context.Context) error { return s.DeleteNetwork(ctx, in.NetworkID) },
			verify: func(ctx context.Context) (bool, any, error) {
				l, _, e := s.Client.Networks.List(ctx)
				for _, n := range l {
					if string(n.ID) == in.NetworkID {
						return false, map[string]any{"still_listed": true}, e
					}
				}
				return true, nil, e
			}}, nil
	}
	var def struct {
		Name          *string `json:"name"`
		Note          *string `json:"note"`
		RetentionDays *int32  `json:"retention_days"`
	}
	if err := decodeDefinition(in.Definition, &def, "name, note, retention_days"); err != nil {
		return nil, err
	}
	if def.Name == nil && def.Note == nil && def.RetentionDays == nil {
		return nil, fmt.Errorf("%w: update must change name, note or retention_days", ErrInvalidInput)
	}
	after := map[string]any{"id": string(cur.ID), "name": cur.Name, "note": nilIfEmpty(cur.Note)}
	if def.Name != nil {
		after["name"] = *def.Name
	}
	if def.Note != nil {
		after["note"] = nilIfEmpty(*def.Note)
	}
	if def.RetentionDays != nil {
		before["retention_days"], after["retention_days"] = cur.RetentionDays, *def.RetentionDays
	}
	return &networkPlan{target: fmt.Sprintf("update network %q (%s)", cur.Name, cur.ID), action: "update_network", before: before, after: after, reversible: true, undo: "update it again with the before values",
		do: func(ctx context.Context) error {
			_, _, err := s.Client.Networks.Update(ctx, in.NetworkID, forward.NetworkUpdate{Name: def.Name, Note: def.Note, RetentionDays: def.RetentionDays})
			return err
		},
		verify: func(ctx context.Context) (bool, any, error) {
			l, _, e := s.Client.Networks.List(ctx)
			for _, n := range l {
				if string(n.ID) == in.NetworkID {
					ok := (def.Name == nil || n.Name == *def.Name) && (def.Note == nil || n.Note == *def.Note)
					return ok, map[string]any{"name": n.Name, "note": n.Note}, e
				}
			}
			return false, nil, e
		}}, nil
}

func idOf(n *forward.Network) string {
	if n == nil {
		return ""
	}
	return string(n.ID)
}

func planLocation(ctx context.Context, s *fwd.Session, in editNetworkInput) (*networkPlan, error) {
	if in.NetworkID == "" {
		return nil, fmt.Errorf("%w: network_id is required", ErrInvalidInput)
	}
	locs, _, err := s.Client.Locations.List(ctx, in.NetworkID)
	if err != nil {
		return nil, err
	}
	switch in.Action {
	case "create":
		var def forward.LocationCreateRequest
		if err := decodeDefinition(in.Definition, &def, "name, lat, lng, city, adminDivision, country, id"); err != nil {
			return nil, err
		}
		if in.Name != "" && def.Name == "" {
			def.Name = in.Name
		}
		if strings.TrimSpace(def.Name) == "" {
			return nil, fmt.Errorf("%w: a location needs a name", ErrInvalidInput)
		}
		for _, l := range locs {
			if strings.EqualFold(l.Name, def.Name) {
				return nil, fmt.Errorf("%w: a location named %q already exists (id %s)", ErrInvalidInput, l.Name, l.ID)
			}
		}
		return &networkPlan{target: fmt.Sprintf("create location %q on network %s", def.Name, in.NetworkID), action: "create_location", before: nil, after: def, reversible: true,
			undo:   "delete the location (action delete)",
			limits: []string{"a location is placed by lat and lng; devices join it with action assign (device name to location id) or by a device glob in the UI"},
			do: func(ctx context.Context) error {
				_, _, err := s.Client.Locations.Create(ctx, in.NetworkID, def)
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				l, _, e := s.Client.Locations.List(ctx, in.NetworkID)
				for _, x := range l {
					if strings.EqualFold(x.Name, def.Name) {
						return true, nil, e
					}
				}
				return false, nil, e
			}}, nil
	case "assign":
		var mapping map[string]string
		if err := decodeDefinition(in.Definition, &mapping, `"<device name>": "<location id>", ...`); err != nil {
			return nil, err
		}
		if len(mapping) == 0 {
			return nil, fmt.Errorf("%w: assign needs at least one device name mapped to a location id", ErrInvalidInput)
		}
		known := map[string]bool{}
		for _, l := range locs {
			known[string(l.ID)] = true
		}
		var bad []string
		for d, id := range mapping {
			if !known[id] {
				bad = append(bad, fmt.Sprintf("%s -> %s", d, id))
			}
		}
		if len(bad) > 0 {
			sort.Strings(bad)
			return nil, fmt.Errorf("%w: these location ids do not exist on the network: %s (inspect-platform area locations lists them)", ErrInvalidInput, strings.Join(bad, ", "))
		}
		return &networkPlan{target: fmt.Sprintf("assign %d device(s) to locations on network %s", len(mapping), in.NetworkID), action: "assign_locations", before: "not read: the SDK has no read of current device locations", after: mapping, reversible: false,
			undo:   "assign the devices to their earlier locations: the earlier ones are not read here, so note them first",
			limits: []string{"device names are matched by Forward exactly; a name it does not know is accepted without effect"},
			do: func(ctx context.Context) error {
				_, err := s.Client.Locations.Assign(ctx, in.NetworkID, mapping)
				return err
			}}, nil
	case "update", "delete":
		var cur *forward.Location
		for i := range locs {
			if string(locs[i].ID) == in.Name || strings.EqualFold(locs[i].Name, in.Name) {
				cur = &locs[i]
			}
		}
		if cur == nil {
			return nil, nil
		}
		id := string(cur.ID)
		before, _ := fwd.Generic(cur)
		gone := func(ctx context.Context) (bool, any, error) {
			l, _, e := s.Client.Locations.List(ctx, in.NetworkID)
			for _, x := range l {
				if string(x.ID) == id {
					return false, x, e
				}
			}
			return true, nil, e
		}
		if in.Action == "delete" {
			return &networkPlan{target: fmt.Sprintf("delete location %q on network %s", cur.Name, in.NetworkID), action: "delete_location", before: before, reversible: false, confirm: id,
				undo:   "none: create the location again from the before values; devices assigned to it must be assigned again",
				limits: []string{"confirm is the location id"},
				do: func(ctx context.Context) error {
					_, err := s.Client.Locations.Delete(ctx, in.NetworkID, id)
					return err
				},
				verify: gone}, nil
		}
		var def struct {
			Name          *string  `json:"name"`
			Lat           *float64 `json:"lat"`
			Lng           *float64 `json:"lng"`
			City          *string  `json:"city"`
			AdminDivision *string  `json:"adminDivision"`
			Country       *string  `json:"country"`
			DeviceGlobs   []string `json:"deviceGlobs"`
		}
		if err := decodeDefinition(in.Definition, &def, "name, lat, lng, city, adminDivision, country, deviceGlobs"); err != nil {
			return nil, err
		}
		if def.Name == nil && def.Lat == nil && def.Lng == nil && def.City == nil && def.AdminDivision == nil && def.Country == nil && def.DeviceGlobs == nil {
			return nil, fmt.Errorf("%w: update must change at least one field", ErrInvalidInput)
		}
		patch := forward.LocationPatch{Name: def.Name, Lat: def.Lat, Lng: def.Lng, City: def.City, AdminDivision: def.AdminDivision, Country: def.Country, DeviceGlobs: def.DeviceGlobs}
		return &networkPlan{target: fmt.Sprintf("update location %q on network %s", cur.Name, in.NetworkID), action: "update_location", before: before, after: def, reversible: true,
			undo: "update again with the before values",
			do: func(ctx context.Context) error {
				_, _, err := s.Client.Locations.Patch(ctx, in.NetworkID, id, patch)
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				got, _, e := s.Client.Locations.Get(ctx, in.NetworkID, id)
				if e != nil || got == nil {
					return false, nil, e
				}
				return (def.Name == nil || got.Name == *def.Name) && (def.City == nil || got.City == *def.City) && (def.Country == nil || got.Country == *def.Country), got, nil
			}}, nil
	}
	return nil, fmt.Errorf("%w: object location takes action create, update, delete or assign", ErrInvalidInput)
}

func planCluster(ctx context.Context, s *fwd.Session, in editNetworkInput) (*networkPlan, error) {
	if in.NetworkID == "" {
		return nil, fmt.Errorf("%w: network_id is required", ErrInvalidInput)
	}
	var def struct {
		LocationID string   `json:"location_id"`
		Name       string   `json:"name"`
		Devices    []string `json:"devices"`
	}
	if err := decodeDefinition(in.Definition, &def, "location_id, name, devices"); err != nil {
		return nil, err
	}
	if def.LocationID == "" {
		return nil, fmt.Errorf("%w: a cluster belongs to a location: give location_id", ErrInvalidInput)
	}
	clusters, _, err := s.Client.Locations.ListClusters(ctx, in.NetworkID, def.LocationID)
	if err != nil {
		return nil, err
	}
	switch in.Action {
	case "create":
		if strings.TrimSpace(def.Name) == "" || len(def.Devices) == 0 {
			return nil, fmt.Errorf("%w: create needs name and devices", ErrInvalidInput)
		}
		for _, c := range clusters {
			if c.Name == def.Name {
				return nil, fmt.Errorf("%w: a cluster named %q already exists at that location", ErrInvalidInput, def.Name)
			}
		}
		return &networkPlan{target: fmt.Sprintf("create device cluster %q (%d devices) at location %s", def.Name, len(def.Devices), def.LocationID), action: "create_cluster", before: nil,
			after: map[string]any{"name": def.Name, "devices": def.Devices}, reversible: false, undo: "none through the API: the SDK has no cluster delete; rename or empty it with action update",
			do: func(ctx context.Context) error {
				_, err := s.Client.Locations.CreateCluster(ctx, in.NetworkID, def.LocationID, forward.DeviceCluster{Name: def.Name, Devices: def.Devices})
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				cs, _, e := s.Client.Locations.ListClusters(ctx, in.NetworkID, def.LocationID)
				for _, c := range cs {
					if c.Name == def.Name {
						return true, nil, e
					}
				}
				return false, nil, e
			}}, nil
	case "update":
		if in.Name == "" {
			return nil, fmt.Errorf("%w: update needs name (the cluster to change); definition.name is its new name", ErrInvalidInput)
		}
		var cur *forward.DeviceCluster
		for i := range clusters {
			if clusters[i].Name == in.Name {
				cur = &clusters[i]
			}
		}
		if cur == nil {
			return nil, nil
		}
		patch := forward.DeviceClusterPatch{Devices: def.Devices}
		after := map[string]any{"name": cur.Name, "devices": cur.Devices}
		if def.Name != "" {
			patch.Name = &def.Name
			after["name"] = def.Name
		}
		if len(def.Devices) > 0 {
			after["devices"] = def.Devices
		}
		if patch.Name == nil && len(patch.Devices) == 0 {
			return nil, fmt.Errorf("%w: update must change name or devices", ErrInvalidInput)
		}
		return &networkPlan{target: fmt.Sprintf("update device cluster %q at location %s", in.Name, def.LocationID), action: "update_cluster", before: map[string]any{"name": cur.Name, "devices": cur.Devices}, after: after,
			reversible: true, undo: "update it again with the before values",
			do: func(ctx context.Context) error {
				_, err := s.Client.Locations.PatchCluster(ctx, in.NetworkID, def.LocationID, in.Name, patch)
				return err
			}}, nil
	}
	return nil, fmt.Errorf("%w: object cluster takes action create or update", ErrInvalidInput)
}

func planTag(ctx context.Context, s *fwd.Session, in editNetworkInput) (*networkPlan, error) {
	if in.NetworkID == "" || strings.TrimSpace(in.Name) == "" {
		return nil, fmt.Errorf("%w: a tag definition needs network_id and name (the tag)", ErrInvalidInput)
	}
	tags, _, err := s.Client.DeviceTags.List(ctx, in.NetworkID, "")
	if err != nil {
		return nil, err
	}
	var cur *forward.DeviceTag
	for i := range tags {
		if tags[i].Name == in.Name {
			cur = &tags[i]
		}
	}
	if cur == nil {
		return nil, nil
	}
	before, _ := fwd.Generic(cur)
	switch in.Action {
	case "update":
		var def struct {
			Name  *string `json:"name"`
			Color *string `json:"color"`
		}
		if err := decodeDefinition(in.Definition, &def, "name, color"); err != nil {
			return nil, err
		}
		if def.Name == nil && def.Color == nil {
			return nil, fmt.Errorf("%w: update must change name or color", ErrInvalidInput)
		}
		return &networkPlan{target: fmt.Sprintf("update tag %q on network %s", in.Name, in.NetworkID), action: "update_tag", before: before, after: def, reversible: true,
			undo:   "update it again with the before name and color (a rename moves the assignments with it)",
			limits: []string{"Forward treats a tag update as create-or-update; this checked the tag exists first"},
			do: func(ctx context.Context) error {
				_, _, err := s.Client.DeviceTags.UpdateTag(ctx, in.NetworkID, in.Name, forward.DeviceTagPatch{Name: def.Name, Color: def.Color})
				return err
			}}, nil
	case "delete":
		if len(in.Definition) > 0 {
			return nil, fmt.Errorf("%w: delete takes name and confirm only", ErrInvalidInput)
		}
		return &networkPlan{target: fmt.Sprintf("delete tag %q from network %s and from every device, across its whole timeline", in.Name, in.NetworkID), action: "delete_tag", before: before, after: nil, reversible: false, confirm: in.Name,
			undo:   "none: re-creating the tag does not restore which devices had it (edit-device-tags takes a tag off named devices without deleting the definition)",
			limits: []string{"a tag used by a check, a saved query or an access label may break them"},
			do: func(ctx context.Context) error {
				_, err := s.Client.DeviceTags.DeleteTag(ctx, in.NetworkID, in.Name)
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				l, _, e := s.Client.DeviceTags.List(ctx, in.NetworkID, "")
				for _, t := range l {
					if t.Name == in.Name {
						return false, map[string]any{"still_listed": true}, e
					}
				}
				return true, nil, e
			}}, nil
	}
	return nil, fmt.Errorf("%w: object tag takes action update or delete", ErrInvalidInput)
}
