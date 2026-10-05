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

const topologyName = "inspect-topology"

func init() { Register(topologyName, inspectTopology) }

type topologyInput struct {
	NetworkID  string `json:"network_id"`
	SnapshotID string `json:"snapshot_id"`
	Kind       string `json:"kind"`
	Device     string `json:"device"`
	Limit      int    `json:"limit"`
	Offset     int    `json:"offset"`
	// CompareToSnapshotID belongs to kind link_overrides: the earlier snapshot to compare with, which turns the summary into the added, removed
	// and changed diff against snapshot_id (default: the newest processed snapshot).
	CompareToSnapshotID string `json:"compare_to_snapshot_id"`
}

const (
	defaultTopologyLimit = 40
	maxTopologyLimit     = 200
)

func inspectTopology(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in topologyInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if in.Kind == "external" {
		return inspectExternalConnectivity(ctx, s, raw)
	}
	if in.CompareToSnapshotID != "" {
		if in.Kind != "link_overrides" {
			return result.Result{}, fmt.Errorf("%w: compare_to_snapshot_id is an input of kind link_overrides, not of kind %s", ErrInvalidInput, in.Kind)
		}
		if in.Offset > 0 {
			return result.Result{}, fmt.Errorf("%w: offset pages the summary view; the diff view (compare_to_snapshot_id) is bounded by limit", ErrInvalidInput)
		}
		after := in.SnapshotID
		if after == "" {
			snap, err := resolveSnapshot(ctx, s, in.NetworkID, "")
			if err != nil {
				return result.Result{}, err
			}
			if snap == nil {
				return result.NewUnknown(topologyName, "No processed snapshot is available to compare against", fwd.Context(in.NetworkID, nil),
					[]string{"no processed snapshot; nothing was compared; name the after snapshot with snapshot_id"}, result.Options{NextActions: []string{"inspect-snapshots"}})
			}
			after = string(snap.ID)
		}
		return compareLinkOverridesView(ctx, s, compareLinkOverridesInput{NetworkID: in.NetworkID, BeforeSnapshotID: in.CompareToSnapshotID, AfterSnapshotID: after, Device: in.Device, Limit: in.Limit})
	}
	if in.Limit <= 0 {
		in.Limit = defaultTopologyLimit
	}
	in.Limit = min(in.Limit, maxTopologyLimit)
	snap, err := resolveSnapshot(ctx, s, in.NetworkID, in.SnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	cx := fwd.Context(in.NetworkID, snap)
	if snap != nil && !fwd.IsReady(snap) {
		return notReadySnapshot(topologyName, cx, snap, "")
	}
	if !fwd.IsReady(snap) {
		return result.NewUnknown(topologyName, "No processed snapshot is available to read", cx,
			[]string{"no processed snapshot; nothing was read"}, result.Options{NextActions: []string{"investigate-collection-failure"}})
	}
	sid := fwd.SnapshotID(cx)
	var limits []string
	if cx.State == "predicted" {
		limits = append(limits, "read from a predicted snapshot: these rows describe a prediction, not collected state")
	}
	var rows []map[string]any
	extra := map[string]any{}
	switch in.Kind {
	case "links":
		links, err := s.Links(ctx, sid)
		if err != nil {
			return result.Result{}, err
		}
		for _, l := range links {
			if in.Device != "" && !portOn(l.SourcePort, in.Device) && !portOn(l.TargetPort, in.Device) {
				continue
			}
			rows = append(rows, map[string]any{"source": l.SourcePort, "target": l.TargetPort})
		}
	case "link_overrides":
		return linkOverridesView(ctx, s, in, snap, cx, limits)
	case "locations":
		locs, err := s.Locations(ctx, in.NetworkID)
		if err != nil {
			return result.Result{}, err
		}
		atlas, _, aerr := s.Client.Locations.AtlasByLocation(ctx, in.NetworkID)
		placed := map[string]forward.AtlasLocation{}
		located := map[string]bool{}
		for _, a := range atlas {
			placed[string(a.LocationID)] = a
			for _, group := range [][]string{a.Devices, a.AnchoredDevices, a.DynamicMatchDevices} {
				for _, d := range group {
					located[d] = true
				}
			}
		}
		for _, l := range locs {
			row := map[string]any{"id": string(l.ID), "name": l.Name, "city": l.City, "admin_division": l.AdminDivision, "country": l.Country, "lat": l.Lat, "lng": l.Lng, "device_globs": l.DeviceGlobs}
			if aerr == nil {
				a := placed[string(l.ID)]
				row["devices_assigned"] = capNames(a.Devices)
				row["devices_anchored"] = capNames(a.AnchoredDevices)
				row["devices_matched_by_glob"] = capNames(a.DynamicMatchDevices)
				row["device_count"] = len(a.Devices) + len(a.AnchoredDevices) + len(a.DynamicMatchDevices)
			}
			rows = append(rows, row)
		}
		if aerr != nil {
			limits = append(limits, "device placement was not read ("+aerr.Error()+"); the rows have no device lists")
		} else {
			extra["devices_with_a_location"] = len(located)
			if st, serr := s.DeviceCollectionStatuses(ctx, in.NetworkID); serr == nil && len(st) == 0 {
				limits = append(limits, "devices with no location were not counted: Forward listed no devices for this network's collection, so a count of 0 would prove nothing")
			} else if serr == nil {
				none := 0
				for _, d := range st {
					if !located[d.DeviceName] {
						none++
					}
				}
				extra["devices_without_a_location"] = none
				extra["devices_counted"] = len(st)
			} else {
				limits = append(limits, "devices with no location were not counted: the device list could not be read ("+serr.Error()+")")
			}
			limits = append(limits, "placement comes from an unpublished Forward view; cloud locations and devices with no fixed location are not in it; a device is counted once, as assigned, anchored (a virtual context or access point) or matched by a location's device glob")
		}
		limits = append(limits, "locations are defined per network, not per snapshot; lat and lng are 0 when the location has no coordinates")
	case "tags":
		tags, err := s.Tags(ctx, in.NetworkID)
		if err != nil {
			return result.Result{}, err
		}
		for _, t := range tags {
			if in.Device != "" && !contains(t.Devices, in.Device) {
				continue
			}
			rows = append(rows, map[string]any{"name": t.Name, "devices": t.Devices})
		}
		limits = append(limits, "tags are defined per network, not per snapshot")
	case "aliases":
		as, err := s.Aliases(ctx, sid)
		if err != nil {
			return result.Result{}, err
		}
		for _, a := range as {
			rows = append(rows, map[string]any{"name": a.Name, "type": a.Type, "definition": json.RawMessage(a.Definition)})
		}
	case "zones":
		zm, _, err := s.Client.Networks.SecurityZones(ctx, in.NetworkID, sid)
		if err != nil {
			return result.Result{}, err
		}
		names := make([]string, 0, len(zm))
		for d := range zm {
			names = append(names, d)
		}
		sort.Strings(names)
		for _, d := range names {
			if in.Device != "" && d != in.Device {
				continue
			}
			rows = append(rows, map[string]any{"device": d, "zones": zm[d], "zone_count": len(zm[d])})
		}
		limits = append(limits, "zones are the security zones Forward derived for each device (firewall zones and similar); a device with no zones has an empty list, which is not the same as not analysed; it needs the security analysis permission and a snapshot that has reached the creation stage (unverified against a live Forward: read from Forward's source)")
	default:
		return result.NewError(topologyName, "unknown kind "+in.Kind, cx), nil
	}
	total := len(rows)
	// a list of things an administrator DEFINES (tags, aliases, link overrides, locations) that Forward returned empty is an answer, not a doubt: the read succeeded and none exist
	if total == 0 && in.Device == "" && (in.Kind == "tags" || in.Kind == "aliases" || in.Kind == "link_overrides" || in.Kind == "locations") {
		finding := fmt.Sprintf("No %s are defined (the read succeeded and Forward returned an empty list)", in.Kind)
		d := map[string]any{"kind": in.Kind, "total": 0, "read": "succeeded", "rows": []map[string]any{}}
		return result.Build(topologyName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: append(limits, "an empty list from a successful read is 'none defined'; a failed read is an error, never an empty list"),
			NextActions: []string{"inspect-inventory"}, Evidence: []result.Evidence{result.NewEvidence(result.EvTopology, "topology_"+in.Kind, cx.SnapshotID, d, finding)}})
	}
	if total == 0 {
		reason := "none are defined or collected"
		if in.Device != "" {
			reason = "none involve device " + in.Device + " (names are matched exactly)"
		}
		return result.NewUnknown(topologyName, fmt.Sprintf("No %s were returned", in.Kind), cx,
			append(limits, fmt.Sprintf("no %s (%s); this skill cannot tell an empty network from a wrong name or an uncollected feature", in.Kind, reason)),
			result.Options{NextActions: []string{"inspect-inventory"}})
	}
	if in.Offset >= total {
		return result.NewUnknown(topologyName, fmt.Sprintf("Offset %d is beyond the %d %s", in.Offset, total, in.Kind), cx,
			append(limits, fmt.Sprintf("offset %d is past the end of the %d %s", in.Offset, total, in.Kind)), result.Options{})
	}
	end := min(in.Offset+in.Limit, total)
	var omitted []result.Omission
	if in.Offset > 0 || end < total {
		omitted = append(omitted, result.Omission{What: in.Kind, Total: total, Shown: end - in.Offset, From: in.Offset, Paged: true, Next: pageNext(end, total)})
	}
	detail := map[string]any{"kind": in.Kind, "device": in.Device, "total": total, "offset": in.Offset, "returned": end - in.Offset, "rows": rows[in.Offset:end]}
	for k, v := range extra {
		detail[k] = v
	}
	return result.Build(topologyName, result.OK, fmt.Sprintf("%d %s returned (%d match)", end-in.Offset, in.Kind, total), result.Deterministic, cx,
		result.Options{Limits: limits, Omitted: omitted, NextActions: []string{"investigate-reachability", "inspect-inventory"},
			Evidence: []result.Evidence{result.NewEvidence(result.EvTopology, "topology", fwd.SnapshotIDPtr(snap), detail, fmt.Sprintf("%s: %d of %d", in.Kind, end-in.Offset, total))}})
}

// capNames bounds a device list in one row; the count stays exact in device_count.
func capNames(names []string) any {
	shown, total := result.CapRow(names, 25)
	if total == len(shown) {
		return shown
	}
	return append(append([]string{}, shown...), fmt.Sprintf("... and %d more", total-len(shown)))
}

// portOn reports whether a port name ("<device> <interface>", as Forward writes it) is on the device.
func portOn(port, device string) bool { return port == device || strings.HasPrefix(port, device+" ") }

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
