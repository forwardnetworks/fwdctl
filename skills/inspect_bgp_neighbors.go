package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const inspectBGPNeighborsName = "inspect-bgp-neighbors"

func init() { Register(inspectBGPNeighborsName, inspectBGPNeighbors) }

type inspectBGPNeighborsInput struct {
	NetworkID   string `json:"network_id"`
	Device      string `json:"device"`
	VRF         string `json:"vrf"`
	Peer        string `json:"peer"`
	PeerAS      int64  `json:"peer_as"`
	Unmodelled  bool   `json:"unmodelled_only"`
	State       string `json:"state"`
	Limit       int    `json:"limit"`
	Offset      int    `json:"offset"`
	NoAdjRibOut bool   `json:"skip_adj_rib_out"`
	SnapshotID  string `json:"snapshot_id"`
}

const (
	defaultBGPLimit = 50
	maxBGPLimit     = 200
)

// inspectBGPNeighbors lists BGP neighbors with the facts needed to see what lies beyond the edge: the peer address and AS, the session state, the prefix counts, the
// description, and whether the peer is a modelled device (peerDevice empty means it is not collected: the unmodelled upstream). For each neighbor it also reports how many
// routes the device advertises to it after output policy (Adj-RIB-Out), where the platform reports it.
func inspectBGPNeighbors(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in inspectBGPNeighborsInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if in.NetworkID == "" {
		return result.Result{}, fmt.Errorf("%w: network_id is required", ErrInvalidInput)
	}
	adv, wantAdv, aerr := parseAdvertised(raw)
	if aerr != nil {
		return result.Result{}, aerr
	}
	if !wantAdv {
		if in.Limit <= 0 {
			in.Limit = defaultBGPLimit
		}
		in.Limit = min(in.Limit, maxBGPLimit)
	}
	snap, err := resolveSnapshot(ctx, s, in.NetworkID, in.SnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	cx := fwd.Context(in.NetworkID, snap)
	if !fwd.IsReady(snap) {
		return result.NewUnknown(inspectBGPNeighborsName, "No processed snapshot is available to answer from", cx, []string{"no processed snapshot; nothing was measured"},
			result.Options{NextActions: []string{"investigate-collection-failure"}})
	}
	sid := fwd.SnapshotID(cx)
	if wantAdv {
		return bgpAdvertised(ctx, s, in, adv, sid, cx)
	}
	rows, _, trunc, err := s.RunNQEAll(ctx, in.NetworkID, sid, bgpNeighborQuery, maxModelRows)
	if err != nil {
		return result.Result{}, err
	}
	// Adj-RIB-Out per device|peer|VRF for IPv4 unicast, and the remainder for the peer in other VRFs and families
	type adjCount struct{ routes, distinct, other int64 }
	var adj map[string]*adjCount
	adjNote := ""
	if !in.NoAdjRibOut {
		adj = map[string]*adjCount{}
		ar, _, _, aerr := s.RunNQEAll(ctx, in.NetworkID, sid, adjRibOutQuery, maxModelRows)
		if aerr != nil {
			adj, adjNote = nil, "the Adj-RIB-Out read failed ("+aerr.Error()+"), so advertised-route counts are missing"
		}
		peerTotal := map[string]int64{}
		for _, r := range ar {
			n, ok := num(r["routes"])
			if !ok {
				continue
			}
			dp := str(r["device"]) + "|" + str(r["peer"])
			peerTotal[dp] += n
			if !strings.Contains(strings.ToUpper(str(r["afi"])), "IPV4_UNICAST") {
				continue
			}
			vrf := str(r["vrf"])
			if vrf == "" {
				vrf = "default"
			}
			c := adj[dp+"|"+vrf]
			if c == nil {
				c = &adjCount{}
				adj[dp+"|"+vrf] = c
			}
			c.routes += n
			d, _ := num(r["distinct"])
			c.distinct += d
		}
		for k, c := range adj {
			c.other = peerTotal[k[:strings.LastIndex(k, "|")]] - c.routes
		}
	}
	want := strings.ToUpper(strings.TrimSpace(in.State))
	var sel []map[string]any
	stateCount, asCount := map[string]int{}, map[string]int{}
	unmodelled := 0
	for _, r := range rows {
		if (in.Device != "" && str(r["device"]) != in.Device) || (in.VRF != "" && str(r["vrf"]) != in.VRF) || (in.Peer != "" && str(r["peer"]) != in.Peer) {
			continue
		}
		if as, ok := num(r["peerAS"]); in.PeerAS != 0 && (!ok || as != in.PeerAS) {
			continue
		}
		peerDev := str(r["peerDevice"])
		if in.Unmodelled && peerDev != "" {
			continue
		}
		st := str(r["state"])
		if want != "" && strings.ToUpper(st) != want {
			continue
		}
		row := map[string]any{"device": str(r["device"]), "vrf": str(r["vrf"]), "peer": str(r["peer"])}
		if v, ok := num(r["peerAS"]); ok {
			row["peer_as"] = v
		}
		if v, ok := num(r["localAS"]); ok {
			row["local_as"] = v
		}
		if st != "" {
			row["state"] = st
		}
		if d := str(r["description"]); d != "" {
			row["description"] = d
		}
		if peerDev != "" {
			row["peer_device"] = peerDev
		} else {
			row["peer_device"] = nil // not a modelled device: what lies beyond the edge
			unmodelled++
		}
		if v, ok := num(r["advertised"]); ok {
			row["advertised_prefixes"] = v
		}
		if v, ok := num(r["received"]); ok {
			row["received_prefixes"] = v
		}
		if adj != nil {
			vrf := str(r["vrf"])
			if vrf == "" {
				vrf = "default"
			}
			if c, ok := adj[str(r["device"])+"|"+str(r["peer"])+"|"+vrf]; ok {
				row["adj_rib_out_routes"] = c.routes
				row["adj_rib_out_distinct_prefixes"] = c.distinct
				row["adj_rib_out_other_vrfs_routes"] = c.other
			}
		}
		sel = append(sel, row)
		if st == "" {
			st = "(not reported)"
		}
		stateCount[st]++
		if v, ok := num(r["peerAS"]); ok {
			asCount[fmt.Sprint(v)]++
		}
	}
	if len(sel) == 0 {
		return result.NewUnknown(inspectBGPNeighborsName, "No BGP neighbor matched", cx, []string{"no BGP neighbor matched the filters (or the devices do not report BGP); this skill cannot tell a wrong filter from no BGP"},
			result.Options{NextActions: []string{"inspect-inventory"}})
	}
	sort.SliceStable(sel, func(i, j int) bool {
		if (sel[i]["peer_device"] == nil) != (sel[j]["peer_device"] == nil) {
			return sel[i]["peer_device"] == nil // unmodelled peers first
		}
		a, b := fmt.Sprint(sel[i]["device"], sel[i]["vrf"], sel[i]["peer"]), fmt.Sprint(sel[j]["device"], sel[j]["vrf"], sel[j]["peer"])
		return a < b
	})
	total := len(sel)
	if in.Offset >= total {
		return result.NewUnknown(inspectBGPNeighborsName, fmt.Sprintf("Offset %d is past the %d matching neighbors", in.Offset, total), cx, []string{"nothing to show at that offset"}, result.Options{})
	}
	end := min(in.Offset+in.Limit, total)
	limits := []string{"the route-map, prefix-list or route-policy applied to a neighbor is not in Forward's model, so it is not shown here: author-nqe-query reference/config-patterns.md reads it from the device config", "state and prefix counts are null on platforms that do not report them; peer_device empty means the peer is not a collected device (the unmodelled side), not that the session is down; adj_rib_out_* is only reported by Junos, IOS, IOS-XE, NX-OS and IOS-XR devices, counts routes after output policy, and is read for the neighbor's own VRF and IPv4 unicast (adj_rib_out_other_vrfs_routes is what the same peer address carries in other VRFs and families); advertised_prefixes is the device's own session counter, adj_rib_out_distinct_prefixes the distinct prefixes in the Adj-RIB-Out and the one to use for exclude-set work, and they can differ by a few"}
	var omitted []result.Omission
	if in.Offset > 0 || end < total {
		omitted = append(omitted, result.Omission{What: "neighbors", Total: total, Shown: end - in.Offset, From: in.Offset, Paged: true, Next: joinNext(pageNext(end, total), "unmodelled peers come first")})
	}
	if trunc {
		limits = append(limits, fmt.Sprintf("the neighbor read hit its %d-row bound", maxModelRows))
	}
	if adjNote != "" {
		limits = append(limits, adjNote)
	}
	d := map[string]any{"total": total, "unmodelled_peers": unmodelled, "by_state": countsTop(stateCount, 12), "by_peer_as": countsTop(asCount, 12), "offset": in.Offset, "neighbors": sel[in.Offset:end]}
	d["snapshot_id"] = string(snap.ID)
	finding := fmt.Sprintf("%d BGP neighbor(s), %d with a peer that is not a modelled device (snapshot %s)", total, unmodelled, snap.ID)
	return result.Build(inspectBGPNeighborsName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, Omitted: omitted,
		NextActions: []string{"inspect-edge", "plan-synthetic-device", "investigate-reachability"},
		Evidence:    []result.Evidence{result.NewEvidence(result.EvNQE, "runNqeQuery", fwd.SnapshotIDPtr(snap), d, finding)}})
}
