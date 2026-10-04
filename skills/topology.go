package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
		for _, l := range locs {
			rows = append(rows, map[string]any{"name": l.Name, "city": l.City, "country": l.Country, "device_globs": l.DeviceGlobs})
		}
		limits = append(limits, "locations are defined per network, not per snapshot")
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
	if end < total {
		limits = append(limits, fmt.Sprintf("%d %s match; rows %d-%d shown. Page with offset=%d.", total, in.Kind, in.Offset+1, end, end))
	}
	detail := map[string]any{"kind": in.Kind, "device": in.Device, "total": total, "offset": in.Offset, "returned": end - in.Offset, "rows": rows[in.Offset:end]}
	return result.Build(topologyName, result.OK, fmt.Sprintf("%d %s returned (%d match)", end-in.Offset, in.Kind, total), result.Deterministic, cx,
		result.Options{Limits: limits, NextActions: []string{"investigate-reachability", "inspect-inventory"},
			Evidence: []result.Evidence{result.NewEvidence(result.EvTopology, "topology", fwd.SnapshotIDPtr(snap), detail, fmt.Sprintf("%s: %d of %d", in.Kind, end-in.Offset, total))}})
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
