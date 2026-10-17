package skills

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// routingDelta is the routing area of verify-change view impact: what Forward's difference areas do not count. It runs the same NQE on both snapshots and compares the BGP neighbor sessions (device, VRF, peer: state) and the
// IPv4/IPv6 route and next-hop counts per device and VRF; with device it also lists that device's per-prefix route changes. It reads two snapshots and judges nothing.
const (
	routeCountQuery = `foreach device in network.devices
foreach instance in device.networkInstances
select {device: device.name, vrf: instance.name,
  ipv4: if isPresent(instance.afts) && isPresent(instance.afts.ipv4Unicast) then length(instance.afts.ipv4Unicast.ipEntries) else 0,
  ipv4NextHops: if isPresent(instance.afts) && isPresent(instance.afts.ipv4Unicast) then sum(foreach e in instance.afts.ipv4Unicast.ipEntries select length(e.nextHops)) else 0,
  ipv6: if isPresent(instance.afts) && isPresent(instance.afts.ipv6Unicast) then length(instance.afts.ipv6Unicast.ipEntries) else 0,
  ipv6NextHops: if isPresent(instance.afts) && isPresent(instance.afts.ipv6Unicast) then sum(foreach e in instance.afts.ipv6Unicast.ipEntries select length(e.nextHops)) else 0}`

	neighborStateQuery = `foreach device in network.devices
foreach instance in device.networkInstances
foreach protocol in instance.protocols
where isPresent(protocol.bgp)
foreach n in protocol.bgp.neighbors
select {device: device.name, vrf: instance.name, peer: n.neighborAddress, state: n.sessionState}`
)

const (
	maxRoutingRows   = 200000
	maxRoutingShown  = 25
	maxPrefixesShown = 50
)

func routingDelta(ctx context.Context, s *fwd.Session, networkID, beforeID, afterID, device string, edited map[string]bool) (evidence []result.Evidence, limits []string, changed bool) {
	run := func(snap, q string) ([]map[string]any, error) {
		rows, _, trunc, err := s.RunNQEAll(ctx, networkID, snap, q, maxRoutingRows)
		if err == nil && trunc {
			limits = append(limits, "a routing read hit the row cap, so the routing comparison is incomplete")
		}
		return rows, err
	}
	cb, e1 := run(beforeID, routeCountQuery)
	ca, e2 := run(afterID, routeCountQuery)
	nb, e3 := run(beforeID, neighborStateQuery)
	na, e4 := run(afterID, neighborStateQuery)
	for _, e := range []error{e1, e2, e3, e4} {
		if e != nil {
			return nil, []string{"the routing comparison (BGP neighbor state, route and next-hop counts) could not be read: " + e.Error() + "; routing impact is unmeasured"}, false
		}
	}
	key := func(r map[string]any, f ...string) string {
		p := make([]string, len(f))
		for i, k := range f {
			p[i] = fmt.Sprint(r[k])
		}
		return strings.Join(p, "\x00")
	}
	num := func(v any) int64 {
		switch x := v.(type) {
		case float64:
			return int64(x)
		case int64:
			return x
		case int:
			return int64(x)
		}
		return 0
	}

	// neighbor sessions: appeared, disappeared, state changed
	nbm, nam := map[string]string{}, map[string]string{}
	for _, r := range nb {
		nbm[key(r, "device", "vrf", "peer")] = fmt.Sprint(r["state"])
	}
	for _, r := range na {
		nam[key(r, "device", "vrf", "peer")] = fmt.Sprint(r["state"])
	}
	var nrows []map[string]any
	for k, st := range nbm {
		p := strings.Split(k, "\x00")
		if a, ok := nam[k]; !ok {
			nrows = append(nrows, map[string]any{"device": p[0], "vrf": p[1], "peer": p[2], "change": "session removed", "state_before": st})
		} else if a != st {
			nrows = append(nrows, map[string]any{"device": p[0], "vrf": p[1], "peer": p[2], "change": "state changed", "state_before": st, "state_after": a})
		}
	}
	for k, st := range nam {
		if _, ok := nbm[k]; !ok {
			p := strings.Split(k, "\x00")
			nrows = append(nrows, map[string]any{"device": p[0], "vrf": p[1], "peer": p[2], "change": "session added", "state_after": st})
		}
	}
	sort.Slice(nrows, func(i, j int) bool {
		return key(nrows[i], "device", "vrf", "peer") < key(nrows[j], "device", "vrf", "peer")
	})

	// route and next-hop counts per device and VRF
	type counts struct{ v4, v4h, v6, v6h int64 }
	read := func(rows []map[string]any) map[string]counts {
		m := map[string]counts{}
		for _, r := range rows {
			m[key(r, "device", "vrf")] = counts{num(r["ipv4"]), num(r["ipv4NextHops"]), num(r["ipv6"]), num(r["ipv6NextHops"])}
		}
		return m
	}
	bm, am := read(cb), read(ca)
	var crows []map[string]any
	var totB, totA counts
	devs := map[string]bool{}
	for k, b := range bm {
		a := am[k]
		totB.v4, totB.v4h, totB.v6, totB.v6h = totB.v4+b.v4, totB.v4h+b.v4h, totB.v6+b.v6, totB.v6h+b.v6h
		totA.v4, totA.v4h, totA.v6, totA.v6h = totA.v4+a.v4, totA.v4h+a.v4h, totA.v6+a.v6, totA.v6h+a.v6h
		if a != b {
			p := strings.Split(k, "\x00")
			devs[p[0]] = true
			crows = append(crows, map[string]any{"device": p[0], "vrf": p[1], "ipv4_routes": [2]int64{b.v4, a.v4}, "ipv4_next_hops": [2]int64{b.v4h, a.v4h}, "ipv6_routes": [2]int64{b.v6, a.v6}, "ipv6_next_hops": [2]int64{b.v6h, a.v6h},
				"ipv4_route_delta": a.v4 - b.v4})
		}
	}
	sort.Slice(crows, func(i, j int) bool {
		di, dj := crows[i]["ipv4_route_delta"].(int64), crows[j]["ipv4_route_delta"].(int64)
		if di < 0 {
			di = -di
		}
		if dj < 0 {
			dj = -dj
		}
		if di != dj {
			return di > dj
		}
		return key(crows[i], "device", "vrf") < key(crows[j], "device", "vrf")
	})
	var notEdited []string
	for d := range devs {
		if edited != nil && !edited[d] {
			notEdited = append(notEdited, d)
		}
	}
	sort.Strings(notEdited)
	shownN, totalN := result.CapRow(nrows, maxRoutingShown)
	shownC, totalC := result.CapRow(crows, maxRoutingShown)
	d := map[string]any{
		"bgp_neighbor_changes": shownN, "bgp_neighbor_changes_total": totalN,
		"device_vrfs_with_route_changes": shownC, "device_vrfs_with_route_changes_total": totalC, "devices_with_route_changes": len(devs),
		"totals_before_after":  map[string]any{"ipv4_routes": [2]int64{totB.v4, totA.v4}, "ipv4_next_hops": [2]int64{totB.v4h, totA.v4h}, "ipv6_routes": [2]int64{totB.v6, totA.v6}, "ipv6_next_hops": [2]int64{totB.v6h, totA.v6h}},
		"device_vrfs_compared": len(bm), "bgp_sessions_compared": len(nbm),
	}
	if edited != nil {
		d["devices_with_route_changes_not_edited_by_the_change_set"] = len(notEdited)
		if len(notEdited) > 0 {
			shown, _ := result.CapRow(notEdited, maxRoutingShown)
			d["not_edited_devices"] = shown
		}
	}
	changed = totalN > 0 || totalC > 0
	if device != "" {
		rows, perr := devicePrefixChanges(ctx, s, networkID, beforeID, afterID, device)
		if perr != nil {
			limits = append(limits, "the per-prefix route changes of "+device+" could not be read: "+perr.Error())
		} else {
			d["prefix_changes_"+device] = rows
		}
	}
	limits = append(limits,
		"routing compares BGP neighbor sessions (state) and IPv4/IPv6 route and next-hop counts per device and VRF, read with NQE from both snapshots; it does not compare route attributes, and a route that changed next hops but kept its count shows only in per-prefix changes (give device)",
		"the PREDICTED snapshot can differ from the collected base with no change staged (for example static and connected routes are not modelled while predict_model_config is off): a route delta on devices the change set does not edit may be that baseline drift, not an effect of the change. To tell, predict a no-op change set (edit-change-set with a description-only change on any device) and compare against it; the delta that also appears there is baseline drift",
		"a CLI `neighbor ... shutdown` removes the session from the prediction but the learned routes stay, so no routing delta after one is not proof of no impact: model the loss of an outside peer with edit-change-set bgp_advertisements withdraw",
		"this routing comparison has been tested against a fake Forward only, not a live one yet: treat a result with no neighbors or routes at all as a read problem, not as a quiet network")
	if len(bm) == 0 || len(am) == 0 {
		limits = append(limits, "one of the snapshots returned no device VRFs for the route count query, so the route comparison says nothing")
		changed = false
	}
	evidence = []result.Evidence{result.NewEvidence(result.EvPredict, "routingDelta", &afterID, d,
		fmt.Sprintf("%d BGP session change(s), %d device VRF(s) with route-count changes on %d device(s); IPv4 routes %d -> %d", totalN, totalC, len(devs), totB.v4, totA.v4))}
	return evidence, limits, changed
}

// devicePrefixChanges lists one device's IPv4 prefixes whose next hops differ between the snapshots (added, removed, next hops changed), per VRF.
func devicePrefixChanges(ctx context.Context, s *fwd.Session, networkID, beforeID, afterID, device string) ([]map[string]any, error) {
	q := `foreach device in network.devices
where device.name == ` + strconv.Quote(device) + `
foreach instance in device.networkInstances
where isPresent(instance.afts) && isPresent(instance.afts.ipv4Unicast)
foreach entry in instance.afts.ipv4Unicast.ipEntries
select {vrf: instance.name, prefix: entry.prefix, nextHops: foreach hop in entry.nextHops select hop.ipAddress}`
	read := func(snap string) (map[string]string, error) {
		rows, _, _, err := s.RunNQEAll(ctx, networkID, snap, q, maxRoutingRows)
		m := map[string]string{}
		for _, r := range rows {
			hops := []string{}
			if hs, ok := r["nextHops"].([]any); ok {
				for _, h := range hs {
					hops = append(hops, fmt.Sprint(h))
				}
			}
			sort.Strings(hops)
			m[fmt.Sprint(r["vrf"])+"\x00"+fmt.Sprint(r["prefix"])] = strings.Join(hops, ",")
		}
		return m, err
	}
	b, err := read(beforeID)
	if err != nil {
		return nil, err
	}
	a, err := read(afterID)
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for k, bh := range b {
		p := strings.SplitN(k, "\x00", 2)
		if ah, ok := a[k]; !ok {
			out = append(out, map[string]any{"vrf": p[0], "prefix": p[1], "change": "removed", "next_hops_before": bh})
		} else if ah != bh {
			out = append(out, map[string]any{"vrf": p[0], "prefix": p[1], "change": "next hops changed", "next_hops_before": bh, "next_hops_after": ah})
		}
	}
	for k, ah := range a {
		if _, ok := b[k]; !ok {
			p := strings.SplitN(k, "\x00", 2)
			out = append(out, map[string]any{"vrf": p[0], "prefix": p[1], "change": "added", "next_hops_after": ah})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return fmt.Sprint(out[i]["vrf"], out[i]["prefix"]) < fmt.Sprint(out[j]["vrf"], out[j]["prefix"])
	})
	shown, total := result.CapRow(out, maxPrefixesShown)
	if total > len(shown) {
		shown = append(shown, map[string]any{"note": fmt.Sprintf("%d of %d prefix changes shown", len(shown), total)})
	}
	return shown, nil
}
