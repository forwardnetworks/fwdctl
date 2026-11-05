package skills

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// hopRoutesQuery reads, for each named device and each of its VRFs, the interfaces in the VRF and the forwarding entries holding the destination
// with the longest prefix (one row per device and VRF; nextHops is empty when no entry holds it). One query answers every hop of the paths shown.
const hopRoutesQuery = `@query
hopRoutes(deviceNames: List<String>, dst: IpAddress) =
foreach device in network.devices
where device.name in deviceNames
foreach ni in device.networkInstances
let matches = (foreach x in [ni] where isPresent(x.afts) && isPresent(x.afts.ipv4Unicast) foreach e in x.afts.ipv4Unicast.ipEntries where dst in e.prefix select e)
let best = max(foreach e in matches select length(e.prefix))
select {
  device: device.name,
  vrf: ni.name,
  interfaces: (foreach i in ni.interfaces select if isPresent(i.subIfaceName) then i.subIfaceName else i.ifaceName),
  prefix: max(foreach e in matches where length(e.prefix) == best select e.prefix),
  nextHops: (foreach e in matches where length(e.prefix) == best foreach nh in e.nextHops
             select { ip: nh.ipAddress, interface: nh.interfaceName, vrf: nh.vrf, type: nh.nextHopType, protocol: nh.originProtocol })
};`

// hopSVIsQuery reads each routed VLAN interface (SVI) of the named devices with the ports of its VLAN, so a route out an SVI can be checked against a
// path that leaves by a member port (Forward routes to the SVI, then forwards at L2 out the port).
const hopSVIsQuery = `@query
hopSVIs(deviceNames: List<String>) =
foreach device in network.devices
where device.name in deviceNames
foreach iface in device.interfaces
where isPresent(iface.routedVlan) && isPresent(iface.routedVlan.vlan)
foreach ni in device.networkInstances
foreach v in ni.vlans
where v.vlanId == iface.routedVlan.vlan
foreach m in v.memberNames
select { device: device.name, svi: iface.name, member: m };`

// maxExplainDevices bounds the devices one explain reads.
const maxExplainDevices = 60

type hopVRF struct {
	vrf        string
	interfaces map[string]bool
	prefix     any
	nextHops   any
}

// explainHopRoutes adds `route` to every hop of the evidence: the VRF (Forward's ingress_vrf, else the VRF holding the ingress interface, else
// default), the longest prefix in that VRF's forwarding table holding the destination, and its next hops. It returns the limits to add; a failed
// read is a limit, never a failed answer.
func explainHopRoutes(ctx context.Context, s *fwd.Session, networkID, snapshotID, dst string, ev []result.Evidence) []string {
	var hopLists [][]map[string]any
	for _, e := range ev {
		if hops, ok := e.Detail["hops"].([]map[string]any); ok {
			hopLists = append(hopLists, hops)
		}
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(dst))
	if err != nil || !addr.Is4() {
		return []string{"explain routes reads the IPv4 forwarding table and needs an IPv4 dst_ip; no routes were added"}
	}
	var names []string
	seen := map[string]bool{}
	for _, hops := range hopLists {
		for _, h := range hops {
			if d, _ := h["device"].(string); d != "" && !seen[d] && len(names) < maxExplainDevices {
				seen[d] = true
				names = append(names, d)
			}
		}
	}
	if len(names) == 0 {
		return nil
	}
	rows, _, truncated, err := s.RunNQEAllWith(ctx, networkID, fwd.NQERun{Query: hopRoutesQuery, SnapshotID: snapshotID,
		Parameters: map[string]any{"deviceNames": names, "dst": addr.String()}}, 20000)
	if err != nil {
		return []string{"explain routes: the forwarding tables could not be read (" + err.Error() + "); no routes were added"}
	}
	// device -> SVI -> member ports; a failed read only loses the SVI check
	svis := map[string]map[string]map[string]bool{}
	sviRows, _, _, sviErr := s.RunNQEAllWith(ctx, networkID, fwd.NQERun{Query: hopSVIsQuery, SnapshotID: snapshotID, Parameters: map[string]any{"deviceNames": names}}, 20000)
	for _, r := range sviRows {
		d, _ := r["device"].(string)
		v, _ := r["svi"].(string)
		m, _ := r["member"].(string)
		if svis[d] == nil {
			svis[d] = map[string]map[string]bool{}
		}
		if svis[d][v] == nil {
			svis[d][v] = map[string]bool{}
		}
		svis[d][v][m] = true
	}
	byDevice := map[string]map[string]*hopVRF{}
	for _, r := range rows {
		d, _ := r["device"].(string)
		v, _ := r["vrf"].(string)
		hv := &hopVRF{vrf: v, interfaces: map[string]bool{}, prefix: r["prefix"], nextHops: r["nextHops"]}
		if l, ok := r["interfaces"].([]any); ok {
			for _, i := range l {
				if n, ok := i.(string); ok {
					hv.interfaces[n] = true
				}
			}
		}
		if byDevice[d] == nil {
			byDevice[d] = map[string]*hopVRF{}
		}
		byDevice[d][v] = hv
	}
	assumed, disagree := 0, 0
	for _, hops := range hopLists {
		for _, h := range hops {
			d, _ := h["device"].(string)
			vrfs := byDevice[d]
			if len(vrfs) == 0 {
				h["route"] = map[string]any{"note": "no forwarding table for this device in the model (a synthetic or unmodelled device)"}
				continue
			}
			var pick *hopVRF
			from := ""
			if v, _ := h["ingress_vrf"].(string); v != "" && vrfs[v] != nil {
				pick, from = vrfs[v], "forward"
			}
			if in, _ := h["ingress_interface"].(string); pick == nil && strings.HasPrefix(in, "self.") && vrfs[strings.TrimPrefix(in, "self.")] != nil {
				pick, from = vrfs[strings.TrimPrefix(in, "self.")], "self_interface" // traffic the device originates in that VRF
			}
			if pick == nil {
				if in, _ := h["ingress_interface"].(string); in != "" {
					for _, hv := range vrfs {
						if hv.interfaces[in] {
							pick, from = hv, "ingress_interface"
							break
						}
					}
				}
			}
			// Forward names the parent interface where the VRF holds its subinterfaces: a VRF with no entry for the destination that holds only the parent
			// gives way to the one VRF holding the parent's subinterfaces
			if in, _ := h["ingress_interface"].(string); in != "" && !strings.HasPrefix(in, "self") && (pick == nil || pick.prefix == nil) {
				var subs []*hopVRF
				for _, hv := range vrfs {
					for n := range hv.interfaces {
						if strings.HasPrefix(n, in+".") {
							subs = append(subs, hv)
							break
						}
					}
				}
				if len(subs) == 1 && subs[0] != pick {
					pick, from = subs[0], "ingress_subinterfaces"
				}
			}
			if pick == nil && len(vrfs) == 1 {
				for _, hv := range vrfs {
					pick, from = hv, "only_vrf"
				}
			}
			if pick == nil && vrfs["default"] != nil {
				pick, from = vrfs["default"], "assumed_default"
				assumed++
			}
			if pick == nil {
				h["route"] = map[string]any{"note": "the hop's VRF could not be told: Forward sent none and the ingress interface is in no VRF of the device"}
				continue
			}
			route := map[string]any{"vrf": pick.vrf, "vrf_from": from, "prefix": pick.prefix, "next_hops": pick.nextHops}
			if pick.prefix == nil {
				route["note"] = "no entry in this VRF holds the destination"
			}
			// check the entry against the interface Forward's path left by: a disagreement means the VRF (or the route: PBR, a leak) is not the one
			// the packet used, so say so and name the VRFs whose entry does leave by it
			if eg, _ := h["egress_interface"].(string); eg != "" && !strings.HasPrefix(eg, "self") {
				check := pathCheck(pick.nextHops, eg, svis[d])
				route["path_check"] = check
				switch check {
				case "agrees", "svi_member_port":
					route["agrees_with_path"] = true
				case "disagrees":
					route["agrees_with_path"] = false
					disagree++
					var others []string
					for _, hv := range vrfs {
						if c := pathCheck(hv.nextHops, eg, svis[d]); hv != pick && (c == "agrees" || c == "svi_member_port") {
							others = append(others, hv.vrf)
						}
					}
					sort.Strings(others)
					route["vrfs_agreeing_with_path"] = others
				}
			}
			h["route"] = route
		}
	}
	limits := []string{fmt.Sprintf("explain routes: each hop's route is the longest prefix holding %s in its device's forwarding table (Forward's model, IPv4), in the hop's VRF (vrf_from says how it was told). It does not apply PBR, NAT (the destination may differ after a NAT hop) or route leaking between VRFs; the hop's egress_interface is Forward's own answer and wins where they differ (a recursive next hop has no interface in the table)", addr)}
	if assumed > 0 {
		limits = append(limits, fmt.Sprintf("%d hop(s) had no VRF from Forward and an ingress interface in no VRF, so the default VRF was assumed (vrf_from assumed_default): a device with the same prefix in another VRF may answer differently", assumed))
	}
	if disagree > 0 {
		limits = append(limits, fmt.Sprintf("%d hop(s) have a route that does not leave by the interface Forward's path used (agrees_with_path false): the VRF was likely told wrong (Forward names a parent interface where the VRF holds a subinterface) or the packet was policy-routed or leaked; vrfs_agreeing_with_path lists the VRFs whose entry leaves by it", disagree))
	}
	if sviErr != nil {
		limits = append(limits, "explain routes: the VLAN members of routed VLAN interfaces could not be read ("+sviErr.Error()+"), so a route out an SVI whose path leaves by a member port reads as disagreeing")
	}
	if truncated {
		limits = append(limits, "explain routes: the forwarding-table read was cut short; some hops may lack a route")
	}
	if len(seen) == maxExplainDevices {
		limits = append(limits, fmt.Sprintf("explain routes reads at most %d devices; later hops have no route", maxExplainDevices))
	}
	return limits
}

// pathCheck compares a route's next hops with the interface Forward's path left by: "agrees" (a next hop leaves by it, its parent or a subinterface),
// "svi_member_port" (a next hop is a routed VLAN interface whose VLAN holds that port: routed to the SVI, then switched out the port),
// "not_comparable" (no next hop names an interface, as for a recursive BGP or OSPF next hop, and none drops), or "disagrees".
func pathCheck(nextHops any, egress string, svis map[string]map[string]bool) string {
	l, _ := nextHops.([]any)
	parent := func(n string) string { return strings.SplitN(n, ".", 2)[0] }
	named, drops := false, false
	for _, x := range l {
		nh, _ := x.(map[string]any)
		if t, _ := nh["type"].(string); t == "DROP" {
			drops = true
		}
		i, _ := nh["interface"].(string)
		if i == "" {
			continue
		}
		named = true
		if i == egress || parent(i) == parent(egress) {
			return "agrees"
		}
		if m := svis[i]; m[egress] || m[parent(egress)] {
			return "svi_member_port"
		}
	}
	if !named && !drops {
		return "not_comparable"
	}
	return "disagrees"
}
