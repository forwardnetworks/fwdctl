package skills

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// The ip_owner kind of inspect-inventory (the retired find-ip-owner skill).
const findIPOwnerName = inventoryName

type findIPOwnerInput struct {
	NetworkID  string   `json:"network_id"`
	IPs        []string `json:"ips"`
	SnapshotID string   `json:"snapshot_id"`
}

const maxOwnerIPs = 100

// findIPOwner (inspect-inventory kind ip_owner) says which modelled device interface owns each IPv4 address: the device, interface, VRF and prefix length when an interface carries the address, the
// connected subnet it falls inside (and whose interfaces those are) when none does, and "no modelled owner" otherwise. A modelled owner is Forward's collected model;
// an address with no owner may be outside the network, or on a device that is not collected.
func findIPOwner(ctx context.Context, s *fwd.Session, in findIPOwnerInput) (result.Result, error) {
	if in.NetworkID == "" || len(in.IPs) == 0 || len(in.IPs) > maxOwnerIPs {
		return result.Result{}, fmt.Errorf("%w: network_id and 1 to %d ips are required", ErrInvalidInput, maxOwnerIPs)
	}
	var ips []netip.Addr
	for _, x := range in.IPs {
		a, err := netip.ParseAddr(strings.TrimSpace(x))
		if err != nil || !a.Is4() {
			return result.Result{}, fmt.Errorf("%w: %q is not an IPv4 address", ErrInvalidInput, x)
		}
		ips = append(ips, a)
	}
	snap, err := resolveSnapshot(ctx, s, in.NetworkID, in.SnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	cx := fwd.Context(in.NetworkID, snap)
	if !fwd.IsReady(snap) {
		return result.NewUnknown(findIPOwnerName, "No processed snapshot is available to answer from", cx, []string{"no processed snapshot; nothing was measured"},
			result.Options{NextActions: []string{"investigate-collection-failure"}})
	}
	addrs, truncated, notes, err := loadIfaceAddrs(ctx, s, in.NetworkID, fwd.SnapshotID(cx))
	if err != nil {
		return result.Result{}, err
	}
	owners := ownersOf(addrs)
	var rows []map[string]any
	owned := 0
	for _, ip := range ips {
		row := map[string]any{"ip": ip.String()}
		if os := owners[ip]; len(os) > 0 {
			owned++
			list := make([]map[string]any, 0, len(os))
			for _, o := range os {
				list = append(list, o.row())
			}
			row["owner"] = list
			if len(os) > 1 {
				row["note"] = "more than one interface carries this address (a shared or per-VRF address, or an FHRP virtual address)"
			}
		} else {
			// inside a connected subnet: the longest prefix that contains it
			var best []ifaceAddr
			bestBits := -1
			for _, a := range addrs {
				if a.Prefix.Contains(ip) && a.Prefix.Bits() > bestBits {
					best, bestBits = []ifaceAddr{a}, a.Prefix.Bits()
				} else if a.Prefix.Contains(ip) && a.Prefix.Bits() == bestBits && len(best) < 6 {
					best = append(best, a)
				}
			}
			if len(best) > 0 {
				list := make([]map[string]any, 0, len(best))
				for _, b := range best {
					list = append(list, b.row())
				}
				row["inside_connected_subnet"] = best[0].Prefix.String()
				row["subnet_interfaces"] = list
				row["note"] = "no modelled interface has this exact address: it is inside a connected subnet, so it is a host or a device that is not collected"
			} else {
				row["owner"] = nil
				row["note"] = "no modelled owner and not inside any connected subnet (interface, SVI and FHRP virtual addresses were searched; see the limits for any class that could not be read)"
			}
		}
		rows = append(rows, row)
	}
	var unowned []netip.Addr
	for i, r := range rows {
		if r["owner"] == nil && r["inside_connected_subnet"] == nil {
			unowned = append(unowned, ips[i])
		}
	}
	var routeNote string
	if cov, n := coveringRoutes(ctx, s, in.NetworkID, fwd.SnapshotID(cx), unowned); n != "" {
		routeNote = n
		for i, r := range rows {
			if c, ok := cov[ips[i]]; ok && r["owner"] == nil && r["inside_connected_subnet"] == nil {
				r["covering_route"] = c
			}
		}
	}
	limits := []string{"owner means an interface of a collected device carries the address in this snapshot, read from interfaces, subinterfaces, routed-VLAN (SVI) interfaces and FHRP virtual addresses (each owner row says which, and its VRF); IPv4 only; an address with no owner may be outside the network, in a class this read does not cover, or on a device Forward does not collect"}
	if routeNote != "" {
		limits = append(limits, routeNote)
	}
	limits = append(limits, notes...)
	if truncated {
		limits = append(limits, fmt.Sprintf("the interface-address read hit its %d-row bound, so an owner may be missing", maxModelRows))
	}
	finding := fmt.Sprintf("%d of %d address(es) have a modelled owner (snapshot %s)", owned, len(ips), snap.ID)
	return result.Build(findIPOwnerName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, NextActions: []string{"investigate-reachability"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvNQE, "runNqeQuery", fwd.SnapshotIDPtr(snap), map[string]any{"snapshot_id": string(snap.ID), "addresses": rows, "owned": owned, "interface_addresses_read": len(addrs)}, finding)}})
}

const (
	maxRouteLookupIPs  = 20
	maxRouteLookupRows = 20000
)

// coveringRoutes finds, for addresses no interface carries and no connected subnet contains, the longest-prefix route that covers each in the network's IPv4 unicast tables (any VRF of any device): the prefix, how many
// device-VRFs hold it and a sample of them with their next hops. Only routes longer than /0 are looked for; an address covered by default routes alone is reported as such. One query serves all the addresses.
func coveringRoutes(ctx context.Context, s *fwd.Session, networkID, snapshotID string, ips []netip.Addr) (map[netip.Addr]map[string]any, string) {
	if len(ips) == 0 {
		return nil, ""
	}
	cappedIPs := len(ips) > maxRouteLookupIPs
	if len(ips) > maxRouteLookupIPs {
		ips, _ = result.Cap(ips, maxRouteLookupIPs, "addresses", "ask for fewer")
	}
	lits := make([]string, len(ips))
	for i, ip := range ips {
		lits[i] = `ipAddress("` + ip.String() + `")`
	}
	q := `foreach d in network.devices
foreach instance in d.networkInstances
where isPresent(instance.afts) && isPresent(instance.afts.ipv4Unicast)
foreach entry in instance.afts.ipv4Unicast.ipEntries
where length(entry.prefix) > 0
foreach ip in [` + strings.Join(lits, ", ") + `]
where ip in entry.prefix
select {ip: ip, device: d.name, vrf: instance.name, prefix: entry.prefix, length: length(entry.prefix), nextHops: foreach hop in entry.nextHops select hop.ipAddress}`
	rows, _, truncated, err := s.RunNQEAll(ctx, networkID, snapshotID, q, maxRouteLookupRows)
	if err != nil {
		return nil, "the covering routes could not be read (" + err.Error() + "), so no route is named for an address with no owner"
	}
	type best struct {
		length int
		prefix string
		rows   []map[string]any
	}
	byIP := map[netip.Addr]*best{}
	for _, r := range rows {
		ip, perr := netip.ParseAddr(fmt.Sprint(r["ip"]))
		if perr != nil {
			continue
		}
		n := 0
		if f, ok := r["length"].(float64); ok {
			n = int(f)
		}
		b := byIP[ip]
		switch {
		case b == nil || n > b.length:
			byIP[ip] = &best{length: n, prefix: fmt.Sprint(r["prefix"]), rows: []map[string]any{r}}
		case n == b.length:
			b.rows = append(b.rows, r)
		}
	}
	out := map[netip.Addr]map[string]any{}
	for ip, b := range byIP {
		sample := []map[string]any{}
		for _, r := range b.rows {
			if len(sample) >= 5 {
				break
			}
			sample = append(sample, map[string]any{"device": r["device"], "vrf": r["vrf"], "next_hops": r["nextHops"]})
		}
		out[ip] = map[string]any{"prefix": b.prefix, "device_vrfs_holding_it": len(b.rows), "sample": sample}
	}
	note := "covering_route is the longest route longer than /0 that contains the address, in any VRF of any device; an address with none is covered by default routes at most. A route covering an address does not mean a host answers there or that the traffic is delivered"
	if cappedIPs {
		note += fmt.Sprintf("; only the first %d addresses with no owner were looked up", maxRouteLookupIPs)
	}
	if truncated {
		note += fmt.Sprintf("; the route read hit its %d-row bound, so the longest route may be missing", maxRouteLookupRows)
	}
	return out, note
}
