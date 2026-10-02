package skills

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
)

// Queries over Forward's network model that several skills share. Their field names are Forward's (checked against its schema and run on a real network).
const (
	// every IPv4 address on a modelled interface, with the VRF of its subinterface
	ifaceAddressQuery = `foreach device in network.devices
foreach iface in device.interfaces
foreach sub in iface.subinterfaces
where isPresent(sub.ipv4)
foreach addr in sub.ipv4.addresses
select {device: device.name, iface: iface.name, sub: sub.name, vrf: sub.networkInstanceName, ip: addr.ip, prefixLength: addr.prefixLength}`

	// the IPv4 addresses of routed-VLAN (SVI) interfaces, which Forward models under routedVlan and not under a subinterface, with the VRF the SVI is in
	sviAddressQuery = `foreach device in network.devices
foreach iface in device.interfaces
where isPresent(iface.routedVlan) && isPresent(iface.routedVlan.ipv4)
foreach addr in iface.routedVlan.ipv4.addresses
select {device: device.name, iface: iface.name, sub: "", vrf: iface.routedVlan.networkInstanceName, ip: addr.ip, prefixLength: addr.prefixLength}`

	// the FHRP (HSRP, VRRP) virtual addresses on routed-VLAN interfaces: the gateway address hosts use
	fhrpAddressQuery = `foreach device in network.devices
foreach iface in device.interfaces
where isPresent(iface.routedVlan) && isPresent(iface.routedVlan.ipv4)
foreach addr in iface.routedVlan.ipv4.fhrpAddresses
select {device: device.name, iface: iface.name, sub: "", vrf: iface.routedVlan.networkInstanceName, ip: addr.ip, prefixLength: addr.prefixLength}`

	// every next hop of every IPv4 default route, per device and VRF
	defaultRouteQuery = `foreach device in network.devices
foreach instance in device.networkInstances
where isPresent(instance.afts) && isPresent(instance.afts.ipv4Unicast)
foreach entry in instance.afts.ipv4Unicast.ipEntries
where entry.prefix == ipSubnet("0.0.0.0/0")
foreach hop in entry.nextHops
select {device: device.name, vrf: instance.name, nextHop: hop.ipAddress, egress: hop.interfaceName, sub: hop.subInterfaceName, hopType: hop.nextHopType}`

	// every BGP neighbor of every device and VRF; statistics and the session state are null on platforms that do not report them
	bgpNeighborQuery = `foreach device in network.devices
foreach instance in device.networkInstances
foreach protocol in instance.protocols
where isPresent(protocol.bgp)
foreach n in protocol.bgp.neighbors
select {device: device.name, vrf: instance.name, peer: n.neighborAddress, peerAS: n.peerAS, localAS: n.localAS, state: n.sessionState, peerDevice: n.peerDeviceName, description: n.description, advertised: n.statistics?.advertisedPrefixes, received: n.statistics?.receivedPrefixes}`

	// the routes a device advertises to each BGP neighbor after output policy (Adj-RIB-Out; only some platforms report it), counted per device, peer, address family and
	// route VRF (null for the default VRF). A neighbor is per device, not per VRF, and its Adj-RIB-Out holds the routes of several VRFs and families, so a count per
	// peer alone overstates what one VRF's session sends.
	adjRibOutQuery = `foreach device in network.devices
where isPresent(device.bgpRib)
foreach afi in device.bgpRib.afiSafis
foreach n in afi.neighbors
where isPresent(n.adjRibOutPost)
foreach route in n.adjRibOutPost.routes
group route as routes by {device: device.name, peer: n.neighborAddress, afi: afi.afiSafiName, vrf: route.vrf} as g
select {device: g.device, peer: g.peer, afi: g.afi, vrf: g.vrf, routes: length(routes), distinct: length(foreach r in routes select distinct r.prefix)}`
)

// maxModelRows bounds each model read; hitting it is stated in the result, never hidden.
const maxModelRows = 200000

type ifaceAddr struct {
	Device, Iface, Sub, VRF string
	// Class is "" for an address on an interface or subinterface, "svi" for one on a routed-VLAN interface, "fhrp" for an FHRP virtual address
	Class  string
	Addr   netip.Addr
	Prefix netip.Prefix
}

func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func num(v any) (int64, bool) {
	switch x := v.(type) {
	case float64:
		return int64(x), true
	case int64:
		return x, true
	case int:
		return int64(x), true
	}
	return 0, false
}

// loadIfaceAddrs reads every IPv4 interface address of the snapshot: on interfaces and subinterfaces, on routed-VLAN (SVI) interfaces, and the FHRP virtual addresses on those.
// truncated says a read held more rows than maxModelRows. notes says which address class could not be read (an unknown is a limit, never a silent omission).
func loadIfaceAddrs(ctx context.Context, s *fwd.Session, networkID, snapshotID string) (addrs []ifaceAddr, truncated bool, notes []string, err error) {
	read := func(query, class string) (bool, error) {
		rows, _, trunc, err := s.RunNQEAll(ctx, networkID, snapshotID, query, maxModelRows)
		if err != nil {
			return false, err
		}
		for _, r := range rows {
			a, err := netip.ParseAddr(str(r["ip"]))
			if err != nil {
				continue
			}
			bits, ok := num(r["prefixLength"])
			if !ok {
				bits = int64(a.BitLen())
			}
			p, err := a.Prefix(int(bits))
			if err != nil {
				continue
			}
			addrs = append(addrs, ifaceAddr{Device: str(r["device"]), Iface: str(r["iface"]), Sub: str(r["sub"]), VRF: str(r["vrf"]), Class: class, Addr: a, Prefix: p})
		}
		return trunc, nil
	}
	trunc, err := read(ifaceAddressQuery, "")
	if err != nil {
		return nil, false, nil, err
	}
	truncated = trunc
	for _, c := range []struct{ q, class, what string }{{sviAddressQuery, "svi", "routed-VLAN (SVI) interface addresses"}, {fhrpAddressQuery, "fhrp", "FHRP virtual addresses"}} {
		t, rerr := read(c.q, c.class)
		if rerr != nil {
			notes = append(notes, c.what+" could not be read ("+rerr.Error()+"), so an address of that class may be reported as having no owner")
			continue
		}
		truncated = truncated || t
	}
	return addrs, truncated, notes, nil
}

func (a ifaceAddr) row() map[string]any {
	m := map[string]any{"device": a.Device, "interface": a.Iface, "ip": a.Addr.String(), "prefix_length": a.Prefix.Bits()}
	if a.Sub != "" && a.Sub != a.Iface {
		m["subinterface"] = a.Sub
	}
	if a.VRF != "" {
		m["vrf"] = a.VRF
	}
	switch a.Class {
	case "svi":
		m["kind"] = "routed VLAN (SVI) interface"
	case "fhrp":
		m["kind"] = "FHRP virtual address"
	}
	return m
}

// ownersOf indexes the addresses by IP.
func ownersOf(addrs []ifaceAddr) map[netip.Addr][]ifaceAddr {
	m := map[netip.Addr][]ifaceAddr{}
	for _, a := range addrs {
		m[a.Addr] = append(m[a.Addr], a)
	}
	return m
}

// countsTop turns a count map into rows, largest first (ties by name), at most max rows.
func countsTop(m map[string]int, max int) []map[string]any {
	type kv struct {
		k string
		n int
	}
	var all []kv
	for k, n := range m {
		all = append(all, kv{k, n})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].n != all[j].n {
			return all[i].n > all[j].n
		}
		return all[i].k < all[j].k
	})
	out := make([]map[string]any, 0, min(len(all), max))
	for i, x := range all {
		if i >= max {
			break
		}
		out = append(out, map[string]any{"name": x.k, "count": x.n})
	}
	return out
}

func joinTop(names []string, n int) string {
	if len(names) <= n {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:n], ", ") + fmt.Sprintf(" (+%d more)", len(names)-n)
}
