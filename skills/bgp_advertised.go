package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// advertisedInput is inspect-bgp-neighbors' advertised option: list the prefixes one device sends to one peer, optionally only those outside some blocks.
type advertisedInput struct {
	// Outside keeps only prefixes that do not lie inside any of these CIDR blocks (a prefix wider than a block, or overlapping it, is outside).
	Outside []string `json:"outside"`
}

const (
	defaultAdvertisedRows = 100
	maxAdvertisedRows     = 1000
	maxAdvertisedRead     = 200000
)

// advertisedQuery reads one device's Adj-RIB-Out after output policy, one row per route; the peer and address family are filtered here, not in the query, so the peer
// address needs no quoting in NQE. The device name is a string literal, checked by the caller.
func advertisedQuery(device string) string {
	return `foreach device in network.devices
where device.name == "` + device + `"
where isPresent(device.bgpRib)
foreach afi in device.bgpRib.afiSafis
foreach n in afi.neighbors
where isPresent(n.adjRibOutPost)
foreach route in n.adjRibOutPost.routes
foreach attr in route.pathAttributes
select {peer: n.neighborAddress, afi: afi.afiSafiName, vrf: route.vrf, prefix: route.prefix, nextHop: attr.nextHop, origin: attr.origin, asPath: attr.asPath?.members, active: attr.activeRoute}`
}

// bgpAdvertised is inspect-bgp-neighbors with advertised: the prefixes a device advertises to one BGP peer after output policy, for IPv4 unicast in one VRF, as the minimal set
// (a prefix inside another advertised prefix is dropped), paged, with an optional filter to those outside given blocks and a count by containing /16. It is the list behind the
// counts the neighbor rows give, for exclude-set and connection design.
func bgpAdvertised(ctx context.Context, s *fwd.Session, in inspectBGPNeighborsInput, adv advertisedInput, sid string, cx result.Context) (result.Result, error) {
	if in.Device == "" || in.Peer == "" {
		return result.Result{}, fmt.Errorf("%w: advertised needs device and peer (the peer address); add vrf for a non-default VRF", ErrInvalidInput)
	}
	if strings.ContainsAny(in.Device, "\"\\\n") {
		return result.Result{}, fmt.Errorf("%w: device names with a quote or backslash are not supported here", ErrInvalidInput)
	}
	peer, err := netip.ParseAddr(strings.TrimSpace(in.Peer))
	if err != nil {
		return result.Result{}, fmt.Errorf("%w: peer must be an IP address", ErrInvalidInput)
	}
	if in.Unmodelled || in.State != "" || in.PeerAS != 0 || in.Offset < 0 {
		return result.Result{}, fmt.Errorf("%w: with advertised, only device, peer, vrf, limit, offset and snapshot_id apply", ErrInvalidInput)
	}
	var outside []netip.Prefix
	for _, c := range adv.Outside {
		p, perr := netip.ParsePrefix(strings.TrimSpace(c))
		if perr != nil {
			return result.Result{}, fmt.Errorf("%w: outside entry %q is not a CIDR block", ErrInvalidInput, c)
		}
		outside = append(outside, p.Masked())
	}
	wantVRF := in.VRF
	if wantVRF == "" || strings.EqualFold(wantVRF, "default") {
		wantVRF = ""
	}
	rows, _, trunc, err := s.RunNQEAll(ctx, in.NetworkID, sid, advertisedQuery(in.Device), maxAdvertisedRead)
	if err != nil {
		return result.Result{}, err
	}
	type attrs struct {
		nextHop, origin string
		asPath          []string
		active          bool
	}
	var seen = map[netip.Prefix]bool{}
	info := map[netip.Prefix]attrs{}
	var other int
	for _, r := range rows {
		rp, perr := netip.ParseAddr(strings.TrimSpace(str(r["peer"])))
		if perr != nil || rp != peer {
			continue
		}
		if !strings.Contains(strings.ToUpper(str(r["afi"])), "IPV4_UNICAST") || str(r["vrf"]) != wantVRF {
			other++
			continue
		}
		p, perr := netip.ParsePrefix(strings.TrimSpace(str(r["prefix"])))
		if perr != nil {
			continue
		}
		mp := p.Masked()
		seen[mp] = true
		a := attrs{nextHop: strings.TrimSpace(str(r["nextHop"])), origin: str(r["origin"])}
		if l, ok := r["asPath"].([]any); ok {
			for _, m := range l {
				if f, ok := m.(float64); ok { // AS numbers can exceed 2^31: print them whole, not as 4.25e+09
					a.asPath = append(a.asPath, strconv.FormatFloat(f, 'f', -1, 64))
					continue
				}
				a.asPath = append(a.asPath, fmt.Sprint(m))
			}
		}
		a.active, _ = r["active"].(bool)
		// several paths for one prefix (multipath): keep the active one, else the first seen
		if cur, ok := info[mp]; !ok || (a.active && !cur.active) {
			info[mp] = a
		}
	}
	cx.State = "current"
	if len(seen) == 0 {
		lim := []string{fmt.Sprintf("%d row(s) of this device's Adj-RIB-Out were read; none is an IPv4 unicast route to %s in VRF %q", len(rows), peer, firstNonEmpty(in.VRF, "default")),
			"Adj-RIB-Out is only reported by Junos, IOS, IOS-XE, NX-OS and IOS-XR devices; no rows means the device, the peer address or the VRF is not as given, or the platform does not report it: that is not proof the peer is sent nothing (inspect-bgp-neighbors without advertised lists the device's peers and VRFs)"}
		if other > 0 {
			lim = append(lim, fmt.Sprintf("%d route(s) go to this peer in other VRFs or families", other))
		}
		return result.NewUnknown(inspectBGPNeighborsName, fmt.Sprintf("No advertised IPv4 prefixes found for %s to %s", in.Device, peer), cx, lim, result.Options{NextActions: []string{"inspect-bgp-neighbors"}})
	}
	all := make([]netip.Prefix, 0, len(seen))
	for p := range seen {
		all = append(all, p)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Bits() != all[j].Bits() {
			return all[i].Bits() < all[j].Bits()
		}
		return all[i].Addr().Less(all[j].Addr())
	})
	// the minimal covering set: shortest first, drop anything inside one already kept
	var kept []netip.Prefix
	for _, p := range all {
		covered := false
		for _, k := range kept {
			if k.Bits() <= p.Bits() && k.Contains(p.Addr()) {
				covered = true
				break
			}
		}
		if !covered {
			kept = append(kept, p)
		}
	}
	list := kept
	if len(outside) > 0 {
		list = nil
		for _, p := range kept {
			inside := false
			for _, o := range outside {
				if o.Bits() <= p.Bits() && o.Contains(p.Addr()) {
					inside = true
					break
				}
			}
			if !inside {
				list = append(list, p)
			}
		}
	}
	by16 := map[string]int{}
	for _, p := range list {
		key := p.String()
		if p.Addr().Is4() && p.Bits() >= 16 {
			key = netip.PrefixFrom(p.Addr(), 16).Masked().String()
		}
		by16[key]++
	}
	rowsOut := make([]map[string]any, 0, len(list))
	byOrigin, byNextHop, byOriginCode := map[string]int{}, map[string]int{}, map[string]int{}
	for _, p := range list {
		a := info[p]
		row := map[string]any{"prefix": p.String(), "origin_type": originType(a.nextHop), "next_hop": nilIfEmpty(a.nextHop), "origin_code": nilIfEmpty(a.origin), "as_path_length": len(a.asPath)}
		if len(a.asPath) > 0 {
			row["as_path"] = strings.Join(a.asPath, " ")
		}
		rowsOut = append(rowsOut, row)
		byOrigin[originType(a.nextHop)]++
		if a.nextHop != "" && originType(a.nextHop) == "learned" {
			byNextHop[a.nextHop]++
		}
		if a.origin != "" {
			byOriginCode[a.origin]++
		}
	}
	limit := in.Limit
	if limit <= 0 {
		limit = defaultAdvertisedRows
	}
	win, omitted, ok := window(rowsOut, min(limit, maxAdvertisedRows), in.Offset, defaultAdvertisedRows, maxAdvertisedRows, "prefixes")
	if !ok {
		return result.NewUnknown(inspectBGPNeighborsName, fmt.Sprintf("Offset %d is beyond the %d prefixes", in.Offset, len(list)), cx, []string{"offset is past the end of the list"}, result.Options{})
	}
	finding := fmt.Sprintf("%s advertises %d distinct IPv4 prefixes to %s in VRF %s (%d after dropping those inside another advertised prefix)", in.Device, len(seen), peer, firstNonEmpty(in.VRF, "default"), len(kept))
	if len(outside) > 0 {
		finding += fmt.Sprintf("; %d of those lie outside %s", len(list), strings.Join(adv.Outside, ", "))
	}
	var limits []string
	limits = append(limits,
		"read from the device's Adj-RIB-Out after output policy (not the route-map: that is not in Forward's model), IPv4 unicast, for this peer address and VRF; only Junos, IOS, IOS-XE, NX-OS and IOS-XR devices report it",
		"the list is the minimal covering set: a prefix inside another advertised prefix is dropped, so it has fewer entries than the neighbor's advertised_prefixes counter and adj_rib_out_distinct_prefixes; the distinct count above is before dropping",
		"outside means not inside any given block: a prefix wider than a block, or one that overlaps it without being inside, is listed as outside; check those by hand",
		"origin_type is read from the path's next hop: local means 0.0.0.0 (the device itself originates or redistributes the route), learned means a next hop elsewhere (re-advertised from a peer: learned_by_next_hop says which); as_path and origin_code are the path attributes of the active path. Communities are NOT in Forward's model, so which prefixes carry a given community, and the route-map that decides what is sent, are read from the device files (reference/policy.md)",
		"by_containing_16 groups IPv4 prefixes by their /16 (a prefix shorter than /16 is its own group)")
	if other > 0 {
		limits = append(limits, fmt.Sprintf("%d more route(s) go to this peer address in other VRFs or address families and are not counted", other))
	}
	if trunc {
		limits = append(limits, fmt.Sprintf("the Adj-RIB-Out read was cut at %d rows, so the list may be incomplete", maxAdvertisedRead))
	}
	groups := make([]map[string]any, 0, len(by16))
	for k, n := range by16 {
		groups = append(groups, map[string]any{"block": k, "prefixes": n})
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i]["prefixes"].(int) != groups[j]["prefixes"].(int) {
			return groups[i]["prefixes"].(int) > groups[j]["prefixes"].(int)
		}
		return groups[i]["block"].(string) < groups[j]["block"].(string)
	})
	d := map[string]any{"device": in.Device, "peer": peer.String(), "vrf": firstNonEmpty(in.VRF, "default"), "distinct_prefixes": len(seen), "after_dropping_covered": len(kept),
		"listed": len(list), "by_origin_type": byOrigin, "learned_by_next_hop": topDeviceCountRowsNamed(byNextHop, 10, "prefixes"), "by_origin_code": byOriginCode, "outside_filter": adv.Outside, "by_containing_16": groups, "offset": in.Offset, "prefixes": win}
	return result.Build(inspectBGPNeighborsName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, Omitted: omitted,
		NextActions: []string{"edit-internet-exclusions", "inspect-topology"}, Evidence: []result.Evidence{result.NewEvidence(result.EvState, "runNqeQuery", cx.SnapshotID, d, finding)}})
}

// parseAdvertised reads the advertised option; ok is false when it is absent.
func parseAdvertised(raw json.RawMessage) (advertisedInput, bool, error) {
	var probe struct {
		Advertised *advertisedInput `json:"advertised"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return advertisedInput{}, false, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if probe.Advertised == nil {
		return advertisedInput{}, false, nil
	}
	return *probe.Advertised, true, nil
}

// originType says where an advertised route came from, by its next hop: 0.0.0.0 or :: is the device's own (originated or redistributed), anything else was learned from a peer.
func originType(nextHop string) string {
	switch strings.TrimSpace(nextHop) {
	case "":
		return "unknown"
	case "0.0.0.0", "::":
		return "local"
	}
	return "learned"
}
