package skills

import (
	"context"
	"fmt"
	"sort"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const (
	defaultOverrideLimit = 50
	maxOverrideLimit     = 200
	topOverrideGroups    = 10
)

// A link override forces a link between two ports to be present (added) or absent (suppressed). Forward's records hold only the two ports; there is no author, time or source on them.
type linkOverride struct{ State, Port1, Port2 string }

// splitPort splits "<device> <interface>" at the first space (Forward's own port notation).
func splitPort(p string) (device, iface string) {
	if i := strings.IndexByte(p, ' '); i >= 0 {
		return p[:i], p[i+1:]
	}
	return p, ""
}

// listOverrides flattens present and absent overrides with each pair in Forward's normal order (port1 <= port2), sorted by device pair, then state, then ports: a stable order for paging.
func listOverrides(o *forward.TopologyOverrides) []linkOverride {
	var out []linkOverride
	add := func(state string, l []forward.TopologyOverrideLink) {
		for _, x := range l {
			a, b := x.Port1, x.Port2
			if b < a {
				a, b = b, a
			}
			out = append(out, linkOverride{state, a, b})
		}
	}
	add("present", o.Present)
	add("absent", o.Absent)
	sort.Slice(out, func(i, j int) bool {
		di1, _ := splitPort(out[i].Port1)
		di2, _ := splitPort(out[i].Port2)
		dj1, _ := splitPort(out[j].Port1)
		dj2, _ := splitPort(out[j].Port2)
		if di1 != dj1 {
			return di1 < dj1
		}
		if di2 != dj2 {
			return di2 < dj2
		}
		if out[i].State != out[j].State {
			return out[i].State < out[j].State
		}
		if out[i].Port1 != out[j].Port1 {
			return out[i].Port1 < out[j].Port1
		}
		return out[i].Port2 < out[j].Port2
	})
	return out
}

func (l linkOverride) on(device string) bool {
	d1, _ := splitPort(l.Port1)
	d2, _ := splitPort(l.Port2)
	return d1 == device || d2 == device
}

func filterOverrides(all []linkOverride, device string) []linkOverride {
	if device == "" {
		return all
	}
	var out []linkOverride
	for _, l := range all {
		if l.on(device) {
			out = append(out, l)
		}
	}
	return out
}

// overrideSummary counts the overrides: present and absent, and the pairs of devices and the devices carrying the most, so a list of hundreds says what it is made of.
func overrideSummary(all []linkOverride) map[string]any {
	type pc struct{ present, absent int }
	pairs, devs := map[string]*pc{}, map[string]*pc{}
	bump := func(m map[string]*pc, k, state string) {
		c := m[k]
		if c == nil {
			c = &pc{}
			m[k] = c
		}
		if state == "present" {
			c.present++
		} else {
			c.absent++
		}
	}
	present := 0
	for _, l := range all {
		d1, _ := splitPort(l.Port1)
		d2, _ := splitPort(l.Port2)
		if d2 < d1 {
			d1, d2 = d2, d1
		}
		bump(pairs, d1+" <-> "+d2, l.State)
		bump(devs, d1, l.State)
		if d2 != d1 {
			bump(devs, d2, l.State)
		}
		if l.State == "present" {
			present++
		}
	}
	top := func(m map[string]*pc, key string) []map[string]any {
		names := make([]string, 0, len(m))
		for k := range m {
			names = append(names, k)
		}
		sort.Slice(names, func(i, j int) bool {
			a, b := m[names[i]], m[names[j]]
			if a.present+a.absent != b.present+b.absent {
				return a.present+a.absent > b.present+b.absent
			}
			return names[i] < names[j]
		})
		out := make([]map[string]any, 0, min(len(names), topOverrideGroups))
		for i, n := range names {
			if i >= topOverrideGroups {
				break
			}
			out = append(out, map[string]any{key: n, "present": m[n].present, "absent": m[n].absent, "overrides": m[n].present + m[n].absent})
		}
		return out
	}
	return map[string]any{"present": present, "absent": len(all) - present, "total": len(all), "device_pairs": len(pairs), "devices": len(devs),
		"top_device_pairs": top(pairs, "devices"), "top_devices": top(devs, "device")}
}

const overrideLimitsProvenance = "an override record holds only its two ports and whether it forces the link present or absent: Forward's API gives no author, creation time or source for an override, so who set one and when is not available here"

const overrideLimitsScope = "this reads the overrides stored with the snapshot (GET /api/snapshots/{id}/topology/overrides, which Forward has deprecated for removal in release 26.11 in favour of the network-level link-override operations); overrides staged since that snapshot for the network's next snapshot are not read by this skill or the SDK"

// overrideRow is one override with the derived flags. ports_exist and link_in_topology are computed for the rows of the page only.
func overrideRow(l linkOverride) map[string]any {
	d1, i1 := splitPort(l.Port1)
	d2, i2 := splitPort(l.Port2)
	return map[string]any{"state": l.State, "port1": l.Port1, "port2": l.Port2, "device1": d1, "interface1": nilIfEmpty(i1), "device2": d2, "interface2": nilIfEmpty(i2)}
}

func nqeList(items []string) string {
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = quote(s)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

// flagOverrideRows adds ports_exist (both devices and interfaces are in the snapshot's model) and link_in_topology (the pair is among the snapshot's links) to the rows of one page. Every
// derivation that fails is said in the returned limits and leaves its flag null: unknown is never a pass.
func flagOverrideRows(ctx context.Context, s *fwd.Session, networkID, snapshotID string, page []linkOverride, rows []map[string]any) []string {
	var limits []string
	devSet := map[string]bool{}
	for _, l := range page {
		d1, _ := splitPort(l.Port1)
		d2, _ := splitPort(l.Port2)
		devSet[d1], devSet[d2] = true, true
	}
	devs := keys(devSet)
	have, haveIface := map[string]bool{}, map[string]bool{}
	modelOK := false
	if len(devs) > 0 {
		drows, _, dtrunc, derr := s.RunNQEAll(ctx, networkID, snapshotID, "foreach device in network.devices\nwhere device.name in "+nqeList(devs)+"\nselect {device: device.name}", maxModelRows)
		irows, _, itrunc, ierr := s.RunNQEAll(ctx, networkID, snapshotID, "foreach device in network.devices\nwhere device.name in "+nqeList(devs)+"\nforeach iface in device.interfaces\nselect {device: device.name, iface: iface.name}", maxModelRows)
		switch {
		case derr != nil || ierr != nil:
			err := derr
			if err == nil {
				err = ierr
			}
			limits = append(limits, "ports_exist could not be derived (the interface model could not be read: "+err.Error()+"), so it is null")
		case dtrunc || itrunc:
			limits = append(limits, "ports_exist could not be derived: the interface read hit its row bound, so it is null")
		default:
			modelOK = true
			for _, r := range drows {
				have[str(r["device"])] = true
			}
			for _, r := range irows {
				haveIface[str(r["device"])+" "+str(r["iface"])] = true
			}
		}
	}
	topoOK := false
	pairs := map[string]bool{}
	if links, err := s.Links(ctx, snapshotID); err != nil {
		limits = append(limits, "link_in_topology could not be derived (the snapshot's links could not be read: "+err.Error()+"), so it is null")
	} else {
		topoOK = true
		for _, l := range links {
			a, b := l.SourcePort, l.TargetPort
			if b < a {
				a, b = b, a
			}
			pairs[a+"\x00"+b] = true
		}
	}
	for i, l := range page {
		r := rows[i]
		r["ports_exist"], r["link_in_topology"] = nil, nil
		if modelOK {
			var missing []string
			for _, p := range []string{l.Port1, l.Port2} {
				d, _ := splitPort(p)
				if !have[d] {
					missing = append(missing, p+" (no such device)")
				} else if !haveIface[p] {
					missing = append(missing, p+" (no such interface)")
				}
			}
			r["ports_exist"] = len(missing) == 0
			if len(missing) > 0 {
				r["missing_ports"] = missing
			}
		}
		if topoOK {
			r["link_in_topology"] = pairs[l.Port1+"\x00"+l.Port2]
		}
	}
	if modelOK || topoOK {
		limits = append(limits, "ports_exist: both devices and both interfaces are in this snapshot's model (an override naming a port Forward cannot find is a likely reason one does not take effect). link_in_topology: the pair appears among the snapshot's topology links; after processing a present override normally appears and an absent one does not, so a mismatch means the snapshot was not reprocessed with it or Forward did not apply it (the cause is not exposed). Both are computed for the rows shown only. Which side wins when an override disagrees with a discovered link is not exposed by the API (Forward's docs state that overrides take precedence over discovered and inferred links)")
	}
	return limits
}

// overridePage is the shared read of a snapshot's overrides for the link_overrides view and the external view: a summary of all of them, and one page of rows with flags.
func overridePage(ctx context.Context, s *fwd.Session, networkID string, snap *forward.Snapshot, device string, limit, offset int) (detail map[string]any, limits []string, err error) {
	sid := string(snap.ID)
	o, err := s.LinkOverrides(ctx, sid)
	if err != nil {
		return nil, nil, err
	}
	all := filterOverrides(listOverrides(o), device)
	sum := overrideSummary(all)
	sum["snapshot_id"] = sid
	if device != "" {
		sum["device"] = device
	}
	limit = min(max(limit, 1), maxOverrideLimit)
	if offset < 0 {
		offset = 0
	}
	offset = min(offset, len(all))
	end := min(offset+limit, len(all))
	page := all[offset:end]
	rows := make([]map[string]any, len(page))
	for i, l := range page {
		rows[i] = overrideRow(l)
	}
	if len(page) > 0 {
		limits = append(limits, flagOverrideRows(ctx, s, networkID, sid, page, rows)...)
	}
	sum["offset"], sum["returned"], sum["rows"] = offset, len(page), rows
	if end < len(all) {
		limits = append(limits, fmt.Sprintf("%d overrides match; rows %d-%d shown in a stable order (device pair, state, ports). Page with offset=%d (limit up to %d) or narrow with device", len(all), offset+1, end, end, maxOverrideLimit))
	}
	limits = append(limits, overrideLimitsProvenance, overrideLimitsScope)
	return sum, limits, nil
}

// linkOverridesView is inspect-topology kind link_overrides: the summary and a page of one snapshot's link overrides.
func linkOverridesView(ctx context.Context, s *fwd.Session, in topologyInput, snap *forward.Snapshot, cx result.Context, limits []string) (result.Result, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = defaultOverrideLimit
	}
	d, more, err := overridePage(ctx, s, in.NetworkID, snap, in.Device, limit, in.Offset)
	if err != nil {
		return result.Result{}, err
	}
	limits = append(limits, more...)
	total, _ := d["total"].(int)
	sid := string(snap.ID)
	if total == 0 {
		reason := "the snapshot has no link overrides"
		if in.Device != "" {
			reason = "none involve device " + in.Device + " (names are matched exactly)"
		}
		return result.NewUnknown(topologyName, "No link overrides were returned (snapshot "+sid+")", cx, append(limits, reason+"; this skill cannot tell an empty set from a snapshot that was reprocessed without them"),
			result.Options{NextActions: []string{"plan-link-overrides", "inspect-snapshots"}})
	}
	finding := fmt.Sprintf("%d link override(s) in snapshot %s: %v present (added), %v absent (suppressed)", total, sid, d["present"], d["absent"])
	if in.Device != "" {
		finding += " involving " + in.Device
	}
	d["kind"] = "link_overrides"
	return result.Build(topologyName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, NextActions: []string{"plan-link-overrides", "edit-link-overrides"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvTopology, "topologyOverrides", fwd.SnapshotIDPtr(snap), d, finding)}})
}
