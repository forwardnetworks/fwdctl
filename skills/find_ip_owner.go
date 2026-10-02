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
	limits := []string{"owner means an interface of a collected device carries the address in this snapshot, read from interfaces, subinterfaces, routed-VLAN (SVI) interfaces and FHRP virtual addresses (each owner row says which, and its VRF); IPv4 only; an address with no owner may be outside the network, in a class this read does not cover, or on a device Forward does not collect"}
	limits = append(limits, notes...)
	if truncated {
		limits = append(limits, fmt.Sprintf("the interface-address read hit its %d-row bound, so an owner may be missing", maxModelRows))
	}
	finding := fmt.Sprintf("%d of %d address(es) have a modelled owner (snapshot %s)", owned, len(ips), snap.ID)
	return result.Build(findIPOwnerName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, NextActions: []string{"investigate-reachability"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvNQE, "runNqeQuery", fwd.SnapshotIDPtr(snap), map[string]any{"snapshot_id": string(snap.ID), "addresses": rows, "owned": owned, "interface_addresses_read": len(addrs)}, finding)}})
}
