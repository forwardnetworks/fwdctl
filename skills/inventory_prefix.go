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

const maxPrefixLookups = 20

// inventoryPrefixRoutes answers "which devices hold a route for this prefix, a route inside it, or a route that covers it", network-wide, from the IPv4 unicast forwarding tables of every VRF of every device
// (kind routes with ips). A route is exact when its prefix is the asked prefix, more_specific when it lies inside it, covering when it contains it (a default route is not counted as covering). The forwarding table is
// what a device installed, so this says who carries a route, not who advertises it; the BGP advertised-routes data is not read here.
func inventoryPrefixRoutes(ctx context.Context, s *fwd.Session, in inventoryInput, cx result.Context, limits []string) (result.Result, error) {
	var asked []netip.Prefix
	seen := map[netip.Prefix]bool{}
	for _, raw := range in.IPs {
		raw = strings.TrimSpace(raw)
		p, err := netip.ParsePrefix(raw)
		if err != nil {
			a, aerr := netip.ParseAddr(raw)
			if aerr != nil || !a.Is4() {
				return result.Result{}, fmt.Errorf("%w: ips entry %q is not an IPv4 address or prefix (for example 10.1.0.0/16)", ErrInvalidInput, raw)
			}
			p = netip.PrefixFrom(a, 32)
		}
		if !p.Addr().Is4() {
			return result.Result{}, fmt.Errorf("%w: ips entry %q is not IPv4; only IPv4 unicast tables are read", ErrInvalidInput, raw)
		}
		p = p.Masked()
		if !seen[p] {
			seen[p] = true
			asked = append(asked, p)
		}
	}
	if len(asked) > maxPrefixLookups {
		return result.Result{}, fmt.Errorf("%w: ips lists %d prefixes; at most %d are looked up at once", ErrInvalidInput, len(asked), maxPrefixLookups)
	}
	lits := make([]string, len(asked))
	for i, p := range asked {
		lits[i] = `ipSubnet("` + p.String() + `")`
	}
	q := `foreach d in network.devices
foreach instance in d.networkInstances
where isPresent(instance.afts) && isPresent(instance.afts.ipv4Unicast)
foreach entry in instance.afts.ipv4Unicast.ipEntries
where length(entry.prefix) > 0
foreach p in [` + strings.Join(lits, ", ") + `]
where entry.prefix in p || p in entry.prefix
select {asked: p, device: d.name, vrf: instance.name, prefix: entry.prefix, length: length(entry.prefix), nextHops: foreach hop in entry.nextHops select {ip: hop.ipAddress, protocol: hop.originProtocol}}`
	rows, _, trunc, err := s.RunNQEAll(ctx, in.NetworkID, fwd.SnapshotID(cx), q, maxModelRows)
	if err != nil {
		return result.Result{}, err
	}
	if trunc {
		limits = append(limits, fmt.Sprintf("the route read hit its %d-row bound, so the counts are partial", maxModelRows))
	}
	type group struct {
		devices map[string]bool
		dvrfs   map[string]bool
		sample  []map[string]any
	}
	type per struct {
		p                      netip.Prefix
		exact, narrower, wider *group
	}
	newGroup := func() *group { return &group{devices: map[string]bool{}, dvrfs: map[string]bool{}} }
	byAsked := map[netip.Prefix]*per{}
	for _, p := range asked {
		byAsked[p] = &per{p: p, exact: newGroup(), narrower: newGroup(), wider: newGroup()}
	}
	var table []map[string]any
	for _, r := range rows {
		ap, aerr := netip.ParsePrefix(fmt.Sprint(r["asked"]))
		rp, rerr := netip.ParsePrefix(fmt.Sprint(r["prefix"]))
		if aerr != nil || rerr != nil || byAsked[ap.Masked()] == nil {
			continue
		}
		e := byAsked[ap.Masked()]
		rel := "more_specific"
		g := e.narrower
		switch {
		case rp.Masked() == ap.Masked():
			rel, g = "exact", e.exact
		case rp.Bits() < ap.Bits():
			rel, g = "covering", e.wider
		}
		dev, vrf := str(r["device"]), str(r["vrf"])
		g.devices[dev] = true
		g.dvrfs[dev+" "+vrf] = true
		row := map[string]any{"asked": ap.String(), "relation": rel, "device": dev, "vrf": vrf, "prefix": rp.String(), "next_hops": r["nextHops"]}
		table = append(table, row)
		if len(g.sample) < 5 {
			g.sample = append(g.sample, map[string]any{"device": dev, "vrf": vrf, "prefix": rp.String()})
		}
	}
	rank := map[string]int{"exact": 0, "covering": 1, "more_specific": 2}
	sort.SliceStable(table, func(i, j int) bool {
		a, b := table[i], table[j]
		if a["asked"] != b["asked"] {
			return fmt.Sprint(a["asked"]) < fmt.Sprint(b["asked"])
		}
		if ra, rb := rank[fmt.Sprint(a["relation"])], rank[fmt.Sprint(b["relation"])]; ra != rb {
			return ra < rb
		}
		if a["device"] != b["device"] {
			return fmt.Sprint(a["device"]) < fmt.Sprint(b["device"])
		}
		return fmt.Sprint(a["vrf"]) < fmt.Sprint(b["vrf"])
	})
	summary := make([]map[string]any, 0, len(asked))
	var parts []string
	for _, p := range asked {
		e := byAsked[p]
		count := func(g *group) map[string]any {
			return map[string]any{"devices": len(g.devices), "device_vrfs": len(g.dvrfs), "sample": g.sample}
		}
		summary = append(summary, map[string]any{"prefix": p.String(), "exact": count(e.exact), "more_specific": count(e.narrower), "covering": count(e.wider)})
		parts = append(parts, fmt.Sprintf("%s: %d device(s) hold it exactly, %d hold a more specific route, %d hold a covering route", p, len(e.exact.devices), len(e.narrower.devices), len(e.wider.devices)))
	}
	shownParts, _ := result.CapRow(parts, 3)
	finding := strings.Join(shownParts, "; ")
	if len(parts) > len(shownParts) {
		finding += fmt.Sprintf("; %d prefixes asked, the rest are in the evidence", len(parts))
	}
	shown, total := result.CapRow(table, in.Limit)
	if total > len(shown) {
		limits = append(limits, fmt.Sprintf("%d of %d matching routes are listed (exact first, then covering, then more specific); the per-prefix counts above cover all of them", len(shown), total))
	}
	limits = append(limits,
		"this reads the IPv4 unicast forwarding table of every VRF of every device as Forward modelled it: a device 'holds' a route that it installed, which is not the same as advertising it (BGP advertised routes are not read here; inspect-bgp-neighbors lists the peers)",
		"a covering route is a route that contains the asked prefix; default routes (/0) are not counted as covering, so a device that reaches the prefix only through its default route is not listed",
		"IPv6 and MPLS tables are not read; a device with no IPv4 forwarding table (not modelled) cannot appear")
	detail := map[string]any{"kind": "routes", "prefixes": summary, "routes": shown, "routes_total": total}
	return result.Build(inventoryName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits,
		NextActions: []string{"inspect-bgp-neighbors", "investigate-reachability"},
		Evidence:    []result.Evidence{result.NewEvidence(result.EvState, "runNqeQuery", cx.SnapshotID, detail, finding)}})
}
