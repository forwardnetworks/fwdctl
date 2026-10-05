package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const inspectEdgeName = "inspect-edge"

func init() { Register(inspectEdgeName, inspectEdge) }

type inspectEdgeInput struct {
	NetworkID  string `json:"network_id"`
	VRF        string `json:"vrf"`
	Device     string `json:"device"`
	Owned      bool   `json:"include_owned"`
	Limit      int    `json:"limit"`
	SnapshotID string `json:"snapshot_id"`
	// View is "exits" (the default), "public_addresses" (find-public-addresses) or "trace_sources" (find-trace-source). The other
	// inputs of those views (role, offset, target_ip, exclude, candidates) are read by their own runners.
	View string `json:"view"`
}

// Inputs by view. A view rejects an input only another view takes, by name, so a mixed call is an error and not silently ignored.
var inspectEdgeViewInputs = map[string][]string{
	"exits":            {"vrf", "device", "include_owned", "limit", "snapshot_id"},
	"public_addresses": {"vrf", "device", "role", "limit", "offset", "snapshot_id"},
	"trace_sources":    {"vrf", "target_ip", "exclude", "candidates", "snapshot_id"},
}

const (
	defaultEdgeLimit = 40
	maxEdgeLimit     = 200
)

type edgeKey struct{ VRF, NextHop string }

type edgeGroup struct {
	edgeKey
	devices map[string]bool
	egress  map[string]bool
	local   map[string]bool
	owner   map[string]any            // who owns the next hop, when something proves it
	proof   string                    // what proved it
	bgp     map[string]map[string]any // eBGP sessions to unmodelled peers in the connected subnet of the next hop, by device|peer
	claims  map[string][]Claimant     // who claims each egress ("device interface"), from the synthetic nodes
	noIface bool                      // some device has no connected subnet holding the next hop (a recursive or unmodelled interface)
}

// neighborRow is one BGP neighbor as read from the model.
type neighborRow struct {
	Device, VRF, Peer, State, PeerDevice string
	PeerAS, LocalAS                      int64
	Received                             *int64
	Advertised                           *int64
	PeerAddr                             netip.Addr
}

func loadNeighbors(ctx context.Context, s *fwd.Session, networkID, snapshotID string) ([]neighborRow, bool, error) {
	rows, _, trunc, err := s.RunNQEAll(ctx, networkID, snapshotID, bgpNeighborQuery, maxModelRows)
	if err != nil {
		return nil, false, err
	}
	var out []neighborRow
	for _, r := range rows {
		a, err := netip.ParseAddr(str(r["peer"]))
		if err != nil {
			continue
		}
		n := neighborRow{Device: str(r["device"]), VRF: str(r["vrf"]), Peer: a.String(), PeerAddr: a, State: str(r["state"]), PeerDevice: str(r["peerDevice"])}
		n.PeerAS, _ = num(r["peerAS"])
		n.LocalAS, _ = num(r["localAS"])
		if v, ok := num(r["received"]); ok {
			n.Received = &v
		}
		if v, ok := num(r["advertised"]); ok {
			n.Advertised = &v
		}
		out = append(out, n)
	}
	return out, trunc, nil
}

// inspectEdge finds where the modelled network hands traffic off: for every IPv4 default route, the next hop, the interface it leaves by and the local address on that
// interface; then whether a modelled device owns the next hop. Ownership is proved three ways and the proof is reported: a modelled interface carries the address; or a
// BGP neighbor record on another device names this device as its peer at an address in the same connected subnet as the next hop (so the next hop is that device's side of the
// link); or a neighbor record has the next hop itself as its peer with a modelled peer device. A next hop nothing proves is an exit candidate; one that also has an eBGP session
// to an unmodelled peer in its subnet is a likely internet edge. Grouped by VRF.
func inspectEdge(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in inspectEdgeInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if in.NetworkID == "" {
		return result.Result{}, fmt.Errorf("%w: network_id is required", ErrInvalidInput)
	}
	view := in.View
	if view == "" {
		view = "exits"
	}
	if _, ok := inspectEdgeViewInputs[view]; !ok {
		return result.Result{}, fmt.Errorf("%w: view must be exits, public_addresses or trace_sources", ErrInvalidInput)
	}
	if err := rejectForeignInputs(raw, inspectEdgeName, "view", view, inspectEdgeViewInputs); err != nil {
		return result.Result{}, err
	}
	switch view {
	case "public_addresses":
		return findPublicAddresses(ctx, s, raw)
	case "trace_sources":
		return findTraceSource(ctx, s, raw)
	}
	if in.Limit <= 0 {
		in.Limit = defaultEdgeLimit
	}
	in.Limit = min(in.Limit, maxEdgeLimit)
	snap, err := resolveSnapshot(ctx, s, in.NetworkID, in.SnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	cx := fwd.Context(in.NetworkID, snap)
	// The synthetic nodes are read as configured NOW, but a snapshot used the node configuration valid when it was created, and Forward does not
	// expose which version that was (no version or valid-from field on a node). Only the newest processed snapshot can be paired with the current nodes.
	claimsCurrent := false
	if fwd.IsReady(snap) {
		if latest, lerr := s.LatestProcessed(ctx, in.NetworkID); lerr == nil && latest != nil && snap != nil && latest.ID == snap.ID {
			claimsCurrent = true
		}
	}
	if !fwd.IsReady(snap) {
		return result.NewUnknown(inspectEdgeName, "No processed snapshot is available to answer from", cx, []string{"no processed snapshot; nothing was measured"},
			result.Options{NextActions: []string{"investigate-collection-failure"}})
	}
	an, err := analyzeEdge(ctx, s, EdgeQuery{NetworkID: in.NetworkID, VRF: in.VRF, Device: in.Device}, snap)
	if err != nil {
		return result.Result{}, err
	}
	snapID, groups, exits, internal, nonForwarding := an.SnapshotID, an.Handoffs, an.Exits, an.Owned, an.NonForwarding
	total, rtrunc, atrunc, ntrunc := an.RouteRows, an.Truncated, false, false
	render := func(g *edgeGroup) map[string]any {
		names := keys(g.devices)
		m := map[string]any{"vrf": g.VRF, "next_hop": g.NextHop, "devices": len(names), "device_names": joinTop(names, 6), "egress": joinTop(keys(g.egress), 6)}
		if len(g.local) > 0 {
			m["local_address"] = joinTop(keys(g.local), 6)
		}
		if g.egress != nil && len(g.egress) == 0 && g.noIface {
			m["egress_unresolved"] = "no modelled interface subnet on the device holds the next hop (a static or recursive route, or the interface is not in the model); the egress cannot be read from here"
		}
		if g.owner != nil {
			m["next_hop_owner"] = g.owner
			m["owner_proof"] = g.proof
		}
		if g.Likely() {
			m["likely_internet_edge"] = true
			if a := g.Assess(); a != nil {
				m["confidence"], m["confidence_reasons"] = a.Confidence, a.Reasons
			}
		}
		if len(g.egress) > 0 {
			// claimed_by is the key for the newest snapshot; for an older one the nodes-now data goes under claimed_by_now and the claim in the snapshot is unknown.
			suffix := ""
			m["claimed_by_basis"] = "nodes as configured now"
			if !claimsCurrent {
				suffix = "_now"
				m["claimed_in_snapshot"] = "unknown"
			}
			first, double, unclaimed := g.ClaimedBy()
			if first == nil {
				m["claimed_by"+suffix] = nil
			} else {
				m["claimed_by"+suffix] = first.Map()
				if len(unclaimed) > 0 {
					m["egress_not_claimed"+suffix] = unclaimed
				}
			}
			if double != nil {
				all := make([]map[string]any, 0, len(double))
				for _, c := range double {
					all = append(all, c.Map())
				}
				m["double_claim"+suffix] = all
				if !claimsCurrent {
					m["double_claim_in_snapshot"] = "unknown"
				}
			}
		}
		if len(g.bgp) > 0 {
			peers := make([]map[string]any, 0, len(g.bgp))
			for _, k := range keysAny(g.bgp) {
				peers = append(peers, g.bgp[k])
			}
			m["bgp_peers_unmodelled"] = peers
		}
		return m
	}
	var omitted []result.Omission
	show := func(gs []*edgeGroup, what, next string) []map[string]any {
		kept, om := result.Cap(gs, in.Limit, what, next)
		omitted = append(omitted, om...)
		out := make([]map[string]any, 0, len(kept))
		for _, g := range kept {
			out = append(out, render(g))
		}
		return out
	}
	exitRows := show(exits, "exit candidates", fmt.Sprintf("likely internet edges first, then by VRF and the most devices; narrow with vrf or device, or raise limit (at most %d)", maxEdgeLimit))
	byVRF := map[string]int{}
	likelyN := 0
	for _, g := range exits {
		byVRF[g.VRF]++
		if g.Likely() {
			likelyN++
		}
	}
	lgRows, byConf, nullRecv, _ := likelyEdgeGroups(exits)
	claimed, unclaimedN, noPort, unclaimedLikely, doubles := 0, 0, 0, 0, 0
	for _, g := range exits {
		first, double, _ := g.ClaimedBy()
		switch {
		case len(g.egress) == 0:
			noPort++
		case first != nil:
			claimed++
		default:
			unclaimedN++
			if g.Likely() {
				unclaimedLikely++
			}
		}
		if double != nil {
			doubles++
		}
	}
	d := map[string]any{"snapshot_id": snapID, "default_route_rows": total, "handoffs_total": groups, "exit_candidates": len(exits), "likely_internet_edges": likelyN,
		"exit_candidates_by_vrf": countsTop(byVRF, 40), "exits": exitRows}
	if likelyN > 0 {
		d["likely_internet_edges_by_confidence"] = byConf
		shown, capped := result.Cap(lgRows, 40, "likely edge groups", "the first 40 are listed")
		omitted = append(omitted, capped...)
		d["likely_edge_groups"] = shown
	}
	if claimsCurrent {
		d["exits_claimed"], d["exits_unclaimed"] = claimed, unclaimedN
	} else {
		d["exits_claimed_now"], d["exits_unclaimed_now"] = claimed, unclaimedN
		d["claimed_in_snapshot"] = "unknown"
	}
	d["claimed_by_basis"] = "nodes as configured now"
	limits := []string{
		"a handoff is a next hop of an IPv4 default route; static or learned routes to specific prefixes are not read, and a network that reaches its exit by BGP-learned specific routes only (no default) shows none",
		"owned is proved by an interface address, or by a BGP neighbor record on the other side (owner_proof says which); a next hop neither proves may be a device Forward does not collect (an exit candidate) or a modelled device whose interface and BGP are both absent from the model",
		"likely_internet_edge means an eBGP session to a peer that is not a modelled device, sits in the next hop's connected subnet and the next hop itself is a public address (not private or shared 100.64/10 space); it does not prove the far side is the internet, and an internal link on public addresses whose far-side device is missing from the model would look the same",
		"the protocol that installed the route is not in this model, so it is not reported",
	}
	if likelyN > 0 {
		limits = append(limits, "likely_internet_edge is unreliable on a network whose internal WAN uses public address space: a public next hop with an eBGP session to an unmodelled peer is also what a carrier, partner or inter-region WAN looks like. confidence (high, medium, low) ranks the likely edges and is a hint, not proof; read likely_edge_groups (one row per VRF and peer AS) before the single rows. Peer AS class: private is 64512-65534 and 4200000000-4294967294; own is iBGP or the AS of a modelled device")
		if nullRecv > 0 {
			limits = append(limits, fmt.Sprintf("received_prefixes is null on %d of %d likely edge(s): Forward's model carries no received-prefix statistic for those sessions (the platform does not report it or the neighbor is not Established), so the signal that the peer sends only a default is missing and cannot raise their confidence; a private peer AS with a null received count is always LOW", nullRecv, likelyN))
		}
	}
	limits = append(limits, fmt.Sprintf("claimed_by is read from the %d synthetic node(s) of the network (internet node, intranet nodes, L3 VPNs, L2 VPNs, adjacent networks; stored connections and the connections their NQE queries generated): a connection claims an uplink when its uplink or gateway port is the exit's egress, or the egress is a subinterface of its uplink port with the connection's VLAN; claimed_by names the first claimant and says nothing about whether the node forwards the traffic correctly", an.Claims.Nodes))
	limits = append(limits, "claimed_by_basis is \"nodes as configured now\": a snapshot used the synthetic node configuration valid when it was CREATED, and Forward's API does not expose which version a snapshot used (a node has no version or valid-from field), so a node changed after the snapshot was created makes the claim state differ from what that snapshot used")
	if !claimsCurrent {
		limits = append(limits, "this snapshot is not the newest processed one, so who claimed each exit IN IT is unknown (claimed_in_snapshot: unknown): claimed_by_now and double_claim_now describe the synthetic nodes as they are now, not this snapshot; read the newest snapshot, or trace a flow on this snapshot to see what it did")
	}
	if len(an.Claims.Unread) > 0 {
		limits = append(limits, "some synthetic node kinds could not be read ("+strings.Join(an.Claims.Unread, "; ")+"), so an exit shown as unclaimed may be claimed by a node of those kinds")
	}
	if doubles > 0 {
		limits = append(limits, fmt.Sprintf("%d exit(s) are claimed by more than one synthetic node (double_claim): Forward's behaviour for a double claim is not documented, so treat it as a likely mistake and remove all but one", doubles))
	}
	if noPort > 0 {
		d["exits_claim_unknown"] = noPort
		limits = append(limits, fmt.Sprintf("%d exit(s) have no resolved egress interface, so who claims them cannot be told", noPort))
	}
	if nonForwarding > 0 {
		limits = append(limits, fmt.Sprintf("%d default-route next hop(s) were not forwarding handoffs (drop, receive, VRF-forwarded or recursive) and are left out", nonForwarding))
	}
	if rtrunc || atrunc || ntrunc {
		limits = append(limits, fmt.Sprintf("a model read hit its %d-row bound, so the result may be incomplete", maxModelRows))
	}
	if in.Owned {
		d["owned_handoffs"] = show(internal, "owned handoffs", "raise limit or narrow with vrf or device")
	} else {
		d["owned_handoffs_count"] = len(internal)
	}
	if groups == 0 {
		return result.NewUnknown(inspectEdgeName, "No IPv4 default route with a forwarding next hop was found"+map[bool]string{true: " for that device or VRF", false: ""}[in.Device != "" || in.VRF != ""]+" (snapshot "+snapID+")", cx,
			append(limits, "no default route may mean the edge is reached by specific routes, or the filter matched nothing"), result.Options{NextActions: []string{"inspect-inventory"}})
	}
	finding := fmt.Sprintf("%d exit candidate(s) (%d likely internet edge: an unowned public next hop with an eBGP session to an unmodelled peer) across %d VRF(s), and %d handoff(s) to modelled devices (snapshot %s)", len(exits), likelyN, len(byVRF), len(internal), snapID)
	if likelyN > 0 {
		finding += fmt.Sprintf("; confidence of the likely edges: %d high, %d medium, %d low in %d VRF/peer-AS group(s); likely_internet_edge is unreliable on public-addressed internal WANs, read the groups and the confidence before treating one as the internet", byConf["high"], byConf["medium"], byConf["low"], len(lgRows))
	}
	if len(exits) > 0 {
		if claimsCurrent {
			finding += fmt.Sprintf("; %d claimed by a synthetic node, %d unclaimed (nodes as configured now)", claimed, unclaimedN)
		} else {
			finding += fmt.Sprintf("; who claimed each exit in this snapshot is unknown (it is not the newest processed one): the synthetic nodes as configured now claim %d and leave %d unclaimed, which describes the nodes now, not this snapshot", claimed, unclaimedN)
		}
		if unclaimedLikely > 0 {
			if claimsCurrent {
				finding += fmt.Sprintf(" (%d unclaimed likely internet edge(s): a Missing Peer, to be modelled with a synthetic device)", unclaimedLikely)
			} else {
				finding += fmt.Sprintf(" (%d likely internet edge(s) unclaimed by the nodes now)", unclaimedLikely)
			}
		}
	}
	if len(exits) == 0 {
		finding = fmt.Sprintf("Every default-route next hop (%d) is owned by a modelled device: no exit candidate (snapshot %s)", groups, snapID)
	}
	return result.Build(inspectEdgeName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, Omitted: omitted,
		NextActions: []string{"plan-synthetic-device", "inspect-bgp-neighbors", "investigate-reachability"},
		Evidence:    []result.Evidence{result.NewEvidence(result.EvNQE, "runNqeQuery", fwd.SnapshotIDPtr(snap), d, finding)}})
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func keysAny(m map[string]map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// publicNextHop says the next hop is in public address space (not private, shared CGNAT, loopback or link-local): a link to an upstream usually is, an internal link usually is not.
func publicNextHop(s string) bool {
	a, err := netip.ParseAddr(s)
	if err != nil {
		return false
	}
	for _, r := range reservedBlocks {
		if r.Contains(a) {
			return false
		}
	}
	return true
}
