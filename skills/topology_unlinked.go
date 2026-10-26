package skills

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const maxUnlinkedRows = 5000

const unlinkedQuery = `foreach d in network.devices
foreach i in d.interfaces
where i.operStatus == OperStatus.UP && length(i.links) == 0 && (i.interfaceType == IfaceType.IF_AGGREGATE || isPresent(i.lldp))
select {device: d.name, interface: i.name, type: i.interfaceType}`

const unlinkedNeighborsQuery = `foreach d in network.devices
foreach i in d.interfaces
where i.operStatus == OperStatus.UP && length(i.links) == 0 && (i.interfaceType == IfaceType.IF_AGGREGATE || isPresent(i.lldp)) && isPresent(i.lldp)
foreach n in i.lldp.neighbors
select {device: d.name, interface: i.name, neighbor: n.deviceName, port: n.portName}`

const allDeviceNamesQuery = `foreach d in network.devices
select {device: d.name}`

// unlinkedInterfaces lists the interfaces that are up but have no link in the topology (port-channels, and any interface with an LLDP neighbour), each with the neighbours its LLDP table names and whether each neighbour
// name is the name of a device in the snapshot. A neighbour name that matches no device is the usual reason a link is missing (the neighbour is named differently from the device Forward collected, or is not collected).
func unlinkedInterfaces(ctx context.Context, s *fwd.Session, in topologyInput, sid string) ([]map[string]any, map[string]any, []string, error) {
	rows, _, trunc, err := s.RunNQEAll(ctx, in.NetworkID, sid, unlinkedQuery, maxUnlinkedRows)
	if err != nil {
		return nil, nil, nil, err
	}
	nrows, _, ntrunc, nerr := s.RunNQEAll(ctx, in.NetworkID, sid, unlinkedNeighborsQuery, maxUnlinkedRows*4)
	drows, _, dtrunc, derr := s.RunNQEAll(ctx, in.NetworkID, sid, allDeviceNamesQuery, 200000)
	var limits []string
	devNames := map[string]bool{}
	if derr == nil {
		for _, r := range drows {
			n := strings.ToLower(fmt.Sprint(r["device"]))
			devNames[n] = true
			if i := strings.Index(n, "."); i > 0 {
				devNames[n[:i]] = true // a neighbour often reports its short name
			}
		}
	}
	matches := func(name string) (known bool, ok bool) {
		if derr != nil {
			return false, false
		}
		n := strings.ToLower(strings.TrimSpace(name))
		if devNames[n] {
			return true, true
		}
		if i := strings.Index(n, "."); i > 0 && devNames[n[:i]] {
			return true, true
		}
		return true, false
	}
	type key struct{ d, i string }
	nb := map[key][]map[string]any{}
	unmatched := map[string]int{}
	if nerr == nil {
		for _, r := range nrows {
			name := fmt.Sprint(r["neighbor"])
			known, ok := matches(name)
			e := map[string]any{"name": name, "port": r["port"]}
			if known {
				e["matches_a_device"] = ok
				if !ok {
					unmatched[name]++
				}
			}
			k := key{fmt.Sprint(r["device"]), fmt.Sprint(r["interface"])}
			nb[k] = append(nb[k], e)
		}
	}
	var out []map[string]any
	for _, r := range rows {
		dev, ifc := fmt.Sprint(r["device"]), fmt.Sprint(r["interface"])
		if in.Device != "" && dev != in.Device {
			continue
		}
		row := map[string]any{"device": dev, "interface": ifc, "type": r["type"]}
		if ns := nb[key{dev, ifc}]; len(ns) > 0 {
			row["lldp_neighbors"] = ns
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i]["device"] != out[j]["device"] {
			return fmt.Sprint(out[i]["device"]) < fmt.Sprint(out[j]["device"])
		}
		return fmt.Sprint(out[i]["interface"]) < fmt.Sprint(out[j]["interface"])
	})
	names := make([]string, 0, len(unmatched))
	for n := range unmatched {
		names = append(names, n)
	}
	sort.Strings(names)
	shown, _ := result.CapRow(names, 30)
	extra := map[string]any{"interfaces_up_without_a_link": len(out), "lldp_neighbor_names_matching_no_device": shown, "lldp_neighbor_names_matching_no_device_count": len(names)}
	limits = append(limits, "lists interfaces that are up and have no link in the topology: port-channels (aggregates) and any interface with an LLDP neighbour; a link is missing for a reason that is not stated here",
		"a neighbour name that matches no device name (exact, or its short name before the first dot, ignoring case) is the usual cause of a missing link: the neighbour is named differently from the device Forward collected, or it is not collected; aliases are not consulted")
	if trunc || ntrunc || dtrunc {
		limits = append(limits, "a read hit its row bound, so the list or the matching is incomplete")
	}
	if nerr != nil {
		limits = append(limits, "the LLDP neighbours could not be read ("+nerr.Error()+"), so no neighbour is shown")
	}
	if derr != nil {
		limits = append(limits, "the device names could not be read ("+derr.Error()+"), so neighbours are not matched")
	}
	return out, extra, limits, nil
}
