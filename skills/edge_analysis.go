package skills

import (
	"context"
	"fmt"
	"net/netip"
	"sort"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// EdgeQuery selects what AnalyzeEdge reads.
type EdgeQuery struct {
	NetworkID  string
	SnapshotID string // empty: the latest processed
	VRF        string // empty: every VRF
	Device     string // empty: every device
}

// EdgeExit is one handoff (a VRF and a next hop of an IPv4 default route), owned by a modelled device or not.
type EdgeExit = edgeGroup

// EdgeAnalysis is what inspect-edge reads from the model: the handoffs nothing proves owned (exit candidates) and those a modelled device owns, with who claims each exit's
// egress. The inspect-edge skill and `fwdctl nqe synthesize internet` both render it, so they cannot disagree.
type EdgeAnalysis struct {
	Snapshot      *forward.Snapshot
	Context       result.Context
	SnapshotID    string
	RouteRows     int64
	Handoffs      int
	NonForwarding int
	Truncated     bool // a model read hit its row bound
	Exits, Owned  []*EdgeExit
	Claims        *ClaimIndex
}

// ErrNoProcessedSnapshot says the network has no processed snapshot to read the edge from.
var ErrNoProcessedSnapshot = fmt.Errorf("the network has no processed snapshot")

// AnalyzeEdge resolves the snapshot and runs the analysis.
func AnalyzeEdge(ctx context.Context, s *fwd.Session, q EdgeQuery) (*EdgeAnalysis, error) {
	snap, err := resolveSnapshot(ctx, s, q.NetworkID, q.SnapshotID)
	if err != nil {
		return nil, err
	}
	if !fwd.IsReady(snap) {
		return nil, ErrNoProcessedSnapshot
	}
	return analyzeEdge(ctx, s, q, snap)
}

func analyzeEdge(ctx context.Context, s *fwd.Session, q EdgeQuery, snap *forward.Snapshot) (*EdgeAnalysis, error) {
	cx := fwd.Context(q.NetworkID, snap)
	snapID := string(snap.ID)
	sid := fwd.SnapshotID(cx)
	routes, total, rtrunc, err := s.RunNQEAll(ctx, q.NetworkID, sid, defaultRouteQuery, maxModelRows)
	if err != nil {
		return nil, err
	}
	addrs, atrunc, err := loadIfaceAddrs(ctx, s, q.NetworkID, sid)
	if err != nil {
		return nil, err
	}
	nbrs, ntrunc, err := loadNeighbors(ctx, s, q.NetworkID, sid)
	if err != nil {
		return nil, err
	}
	owners := ownersOf(addrs)
	peerOwner := map[netip.Addr]neighborRow{} // a neighbor record whose peer is a modelled device: that address belongs to that device
	for _, n := range nbrs {
		if n.PeerDevice != "" {
			if _, ok := peerOwner[n.PeerAddr]; !ok {
				peerOwner[n.PeerAddr] = n
			}
		}
	}
	nbrsOn := map[string][]neighborRow{}
	for _, n := range nbrs {
		nbrsOn[n.Device] = append(nbrsOn[n.Device], n)
	}
	// the local ASNs of modelled devices: a peer in one of them is, as far as the model shows, one of our own devices whose side of the link is missing
	localASDevices := map[int64]map[string]bool{}
	for _, n := range nbrs {
		if n.LocalAS != 0 {
			if localASDevices[n.LocalAS] == nil {
				localASDevices[n.LocalAS] = map[string]bool{}
			}
			localASDevices[n.LocalAS][n.Device] = true
		}
	}
	groups := map[edgeKey]*edgeGroup{}
	nonForwarding := 0
	for _, r := range routes {
		dev, vrf := str(r["device"]), str(r["vrf"])
		if (q.Device != "" && dev != q.Device) || (q.VRF != "" && vrf != q.VRF) {
			continue
		}
		nh, err := netip.ParseAddr(str(r["nextHop"]))
		if err != nil || str(r["hopType"]) != "REMOTE" {
			nonForwarding++ // drop, receive, VRF-forwarded or recursive next hops are not handoffs
			continue
		}
		k := edgeKey{vrf, nh.String()}
		g := groups[k]
		if g == nil {
			g = &edgeGroup{edgeKey: k, devices: map[string]bool{}, egress: map[string]bool{}, local: map[string]bool{}, bgp: map[string]map[string]any{}}
			if os := owners[nh]; len(os) > 0 {
				g.owner, g.proof = os[0].row(), "a modelled interface carries this address"
			} else if n, ok := peerOwner[nh]; ok {
				g.owner = map[string]any{"device": n.PeerDevice}
				g.proof = fmt.Sprintf("%s's BGP neighbor record for %s names %s as the peer device", n.Device, nh, n.PeerDevice)
			}
			groups[k] = g
		}
		g.devices[dev] = true
		eg := str(r["sub"])
		if eg == "" {
			eg = str(r["egress"])
		}
		// the local address: this device's address whose connected subnet holds the next hop (the route's own interface when it names one); when the route names
		// no interface (a recursive next hop), that address also tells which (sub)interface the traffic leaves by
		var local *ifaceAddr
		for i := range addrs {
			a := &addrs[i]
			if a.Device != dev || !a.Prefix.Contains(nh) {
				continue
			}
			if a.Sub == str(r["sub"]) || a.Iface == str(r["egress"]) {
				local = a
				break
			}
			if local == nil || (a.VRF == vrf && local.VRF != vrf) {
				local = a
			}
		}
		if local == nil {
			g.noIface = true
		} else {
			g.local[dev+" "+local.Addr.String()] = true
			if eg == "" {
				eg = local.Sub
				if eg == "" {
					eg = local.Iface
				}
			}
			// BGP in the same connected subnet: another device's neighbor record that points at this device's address here proves the next hop is that device;
			// this device's own neighbor in the subnet with no modelled peer device is an eBGP session to the unmodelled far side
			if g.owner == nil {
				for _, n := range nbrs {
					if n.PeerDevice == dev && n.Device != dev && n.PeerAddr == local.Addr && n.PeerAddr != nh {
						g.owner = map[string]any{"device": n.Device}
						g.proof = fmt.Sprintf("%s has a BGP neighbor at %s (%s's own address in the subnet %s that holds the next hop), so the next hop is %s's side of the link", n.Device, n.Peer, dev, local.Prefix, n.Device)
						break
					}
				}
			}
			for _, n := range nbrsOn[dev] {
				if n.PeerDevice == "" && local.Prefix.Contains(n.PeerAddr) {
					ownAS := false
					for d := range localASDevices[n.PeerAS] {
						ownAS = ownAS || d != dev
					}
					g.bgp[dev+"|"+n.Peer] = map[string]any{"device": dev, "peer": n.Peer, "peer_as": n.PeerAS, "local_as": n.LocalAS, "state": nilIfEmpty(n.State), "ebgp": n.PeerAS != n.LocalAS, "vrf": n.VRF, "peer_as_class": asClass(n.PeerAS, n.LocalAS, ownAS), "peer_as_is_a_modelled_device_as": ownAS}
					if n.Received != nil {
						g.bgp[dev+"|"+n.Peer]["received_prefixes"] = *n.Received
					}
				}
			}
		}
		if eg != "" {
			g.egress[dev+" "+eg] = true
		}
	}
	var exits, internal []*edgeGroup
	for _, g := range groups {
		if g.owner == nil {
			exits = append(exits, g)
		} else {
			internal = append(internal, g)
		}
	}
	order := func(gs []*edgeGroup) {
		sort.Slice(gs, func(i, j int) bool {
			if li, lj := gs[i].Likely(), gs[j].Likely(); li != lj {
				return li // a next hop with an eBGP session to an unmodelled peer first
			}
			if gs[i].Likely() {
				ci, cj := gs[i].Assess().Confidence, gs[j].Assess().Confidence
				if ci != cj {
					return confRank[ci] < confRank[cj]
				}
				if hi, hj := vrfHintRank[vrfHint(gs[i].VRF)], vrfHintRank[vrfHint(gs[j].VRF)]; hi != hj {
					return hi < hj
				}
			}
			if gs[i].VRF != gs[j].VRF {
				return gs[i].VRF < gs[j].VRF
			}
			if len(gs[i].devices) != len(gs[j].devices) {
				return len(gs[i].devices) > len(gs[j].devices)
			}
			return gs[i].NextHop < gs[j].NextHop
		})
	}
	order(exits)
	order(internal)
	cl, err := loadClaims(ctx, s, q.NetworkID)
	if err != nil {
		return nil, err
	}
	for _, g := range append(append([]*edgeGroup{}, exits...), internal...) {
		g.claims = map[string][]Claimant{}
		for k := range g.egress {
			dev, port := splitEgress(k)
			g.claims[k] = cl.claimsOf(dev, port)
		}
	}
	return &EdgeAnalysis{Snapshot: snap, Context: cx, SnapshotID: snapID, RouteRows: total, Handoffs: len(groups), NonForwarding: nonForwarding,
		Truncated: rtrunc || atrunc || ntrunc, Exits: exits, Owned: internal, Claims: cl}, nil
}

// Likely says the handoff is a likely internet edge: an unowned public next hop with an eBGP session to an unmodelled peer that is not one of our own ASNs.
func (g *edgeGroup) Likely() bool {
	if g.owner != nil || !publicNextHop(g.NextHop) {
		return false
	}
	for _, b := range g.bgp {
		if b["ebgp"] == true && b["peer_as_is_a_modelled_device_as"] == false {
			return true
		}
	}
	return false
}

// EdgePort is one device interface (the subinterface when the route names one) a handoff leaves by.
type EdgePort struct {
	Device, Interface string
	Claims            []Claimant
}

// Ports lists the egress interfaces of the handoff, sorted, each with the synthetic node connections that claim it.
func (g *edgeGroup) Ports() []EdgePort {
	var out []EdgePort
	for _, k := range keys(g.egress) {
		d, p := splitEgress(k)
		out = append(out, EdgePort{Device: d, Interface: p, Claims: g.claims[k]})
	}
	return out
}

// EdgePeer is an eBGP session to a peer the model does not hold, in the connected subnet of the handoff's next hop.
type EdgePeer struct {
	Device, Peer string
	PeerAS       int64
	EBGP         bool
}

// Peers lists the unmodelled BGP peers, sorted by device and address.
func (g *edgeGroup) Peers() []EdgePeer {
	var out []EdgePeer
	for _, k := range keysAny(g.bgp) {
		b := g.bgp[k]
		as, _ := num(b["peer_as"])
		out = append(out, EdgePeer{Device: str(b["device"]), Peer: str(b["peer"]), PeerAS: as, EBGP: b["ebgp"] == true})
	}
	return out
}

func splitEgress(k string) (device, port string) {
	for i := 0; i < len(k); i++ {
		if k[i] == ' ' {
			return k[:i], k[i+1:]
		}
	}
	return k, ""
}

// ClaimedBy summarises who claims the handoff: the first claimant across its egress ports (nil when none claims any), every claimant of the first port that more than one node
// claims (a double claim), and the ports nobody claims. A handoff with no resolved egress has no ports, so nothing can be said about it.
func (g *edgeGroup) ClaimedBy() (first *Claimant, double []Claimant, unclaimed []string) {
	for _, p := range g.Ports() {
		if len(p.Claims) == 0 {
			unclaimed = append(unclaimed, p.Device+" "+p.Interface)
			continue
		}
		if first == nil {
			c := p.Claims[0]
			first = &c
		}
		if len(p.Claims) > 1 && double == nil {
			double = p.Claims
		}
	}
	return first, double, unclaimed
}
