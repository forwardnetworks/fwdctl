package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const externalConnName = topologyName

type externalConnInput struct {
	NetworkID  string `json:"network_id"`
	SnapshotID string `json:"snapshot_id"`
	// Device names one synthetic node (the internet node is "internet"): only that node is shown, with its query-generated connections paged by
	// Limit and Offset (default 25, at most 200) and the source of its query when the library holds it. Without it every node is shown with a summary.
	Device string `json:"device"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}

// inspectExternalConnectivity shows how the network is modelled at its edge: the internet node, intranet nodes, L3 VPNs and the
// manual topology links. A path that ends at the boundary is explained, or not, by what is here.
func inspectExternalConnectivity(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in externalConnInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	detail := map[string]any{}
	var limits []string
	read := 0
	if strings.TrimSpace(in.Device) != "" {
		return externalOneNode(ctx, s, in, cx)
	}

	if n, err := s.InternetNode(ctx, in.NetworkID); err != nil {
		limits = append(limits, "the internet node could not be read: "+err.Error())
	} else {
		read++
		if n == nil {
			detail["internet_node"] = nil
			limits = append(limits, "no internet node is modelled: traffic to the internet has nowhere to go, so a path to a public address ends at the edge")
		} else {
			detail["internet_node"] = nodeRow(*n)
		}
	}
	if circuits, err := s.WanCircuits(ctx, in.NetworkID); err != nil {
		limits = append(limits, "the WAN circuits could not be read: "+err.Error())
	} else {
		read++
		rows := make([]map[string]any, 0, len(circuits))
		for _, c := range circuits {
			rows = append(rows, map[string]any{"name": c.Name, "connection1": fmt.Sprintf("%s %s vlan %s", c.Connection1.Device, c.Connection1.Port, vlanText(c.Connection1.VLAN)),
				"connection2": fmt.Sprintf("%s %s vlan %s", c.Connection2.Device, c.Connection2.Port, vlanText(c.Connection2.VLAN)), "source": c.Source})
		}
		detail["wan_circuits"] = rows
	}
	if sug, err := s.InternetSuggestions(ctx, in.NetworkID); err != nil {
		limits = append(limits, "Forward's suggested internet connections could not be read: "+err.Error())
	} else if len(sug) == 0 {
		detail["internet_connection_suggestion_count"] = 0
		limits = append(limits, "Forward has no connections to suggest for the internet node on the latest processed snapshot (an empty answer), so candidate uplinks have to be derived from the network (for example the interfaces that carry a default route to a next hop outside it)")
	} else if len(sug) > 0 {
		rows := make([]map[string]any, 0, min(len(sug), maxDynamicRows))
		for i, x := range sug {
			if i >= maxDynamicRows {
				break
			}
			rows = append(rows, map[string]any{"uplink_interface": x.UplinkInterface, "uplink_description": x.UplinkInterfaceDescription, "vlan": x.VLAN, "gateway_interface": x.GatewayInterface, "gateway_description": x.GatewayInterfaceDescription})
		}
		detail["internet_connection_suggestions"] = rows
		detail["internet_connection_suggestion_count"] = len(sug)
		limits = append(limits, fmt.Sprintf("Forward suggests %d connection(s) for the internet node (interfaces that appear to face the internet, computed from the latest processed snapshot); they are suggestions, not part of the model until added", len(sug)))
	}
	for _, k := range []struct {
		key  string
		kind forward.SyntheticNodeKind
	}{{"intranet_nodes", forward.SyntheticIntranet}, {"l3_vpns", forward.SyntheticL3VPN}, {"l2_vpns", forward.SyntheticL2VPN}, {"adjacent_networks", forward.SyntheticAdjacentNetwork}} {
		nodes, err := s.SyntheticNodes(ctx, in.NetworkID, k.kind)
		if err != nil {
			limits = append(limits, k.key+" could not be read: "+err.Error())
			continue
		}
		read++
		rows := make([]map[string]any, 0, len(nodes))
		for _, n := range nodes {
			rows = append(rows, nodeRow(n))
		}
		detail[k.key] = rows
	}

	sn, err := resolveSnapshot(ctx, s, in.NetworkID, in.SnapshotID)
	if err != nil {
		limits = append(limits, "the snapshot could not be resolved: "+err.Error())
	} else if sn == nil {
		limits = append(limits, "the network has no processed snapshot, so its manual link overrides were not read")
	} else {
		cx = fwd.Context(in.NetworkID, sn)
		if d, more, err := overridePage(ctx, s, in.NetworkID, sn, "", maxDynamicRows, 0); err != nil {
			limits = append(limits, "the link overrides could not be read: "+err.Error())
		} else {
			read++
			if total, _ := d["total"].(int); total > maxDynamicRows {
				d["more"] = fmt.Sprintf("%d overrides in all; the summary counts them all and the first %d are listed: read them all, paged, with kind link_overrides (and device to narrow)", total, maxDynamicRows)
			}
			detail["link_overrides"] = d
			limits = append(limits, more...)
		}
	}
	if read == 0 {
		return result.NewUnknown(externalConnName, "The network's edge model could not be read", cx, limits, result.Options{})
	}
	queried, broken := 0, 0
	note := func(rows any) {
		list, _ := rows.([]map[string]any)
		for _, r := range list {
			if q, ok := r["nqe_query"].(map[string]any); ok {
				queried++
				if _, bad := q["error"]; bad {
					broken++
				}
			}
		}
	}
	note(detail["intranet_nodes"])
	note(detail["l3_vpns"])
	note(detail["l2_vpns"])
	note(detail["adjacent_networks"])
	if in, ok := detail["internet_node"].(map[string]any); ok {
		note([]map[string]any{in})
	}
	if queried > 0 {
		limits = append(limits, fmt.Sprintf("%d node(s) get connections from an NQE query (Forward's dynamic synthetic connections, a preview feature); their static connections can be none, so read nqe_query for what they carry. The query runs on the latest processed snapshot", queried))
	}
	if broken > 0 {
		limits = append(limits, fmt.Sprintf("%d node(s) have a query that failed to produce connections (see nqe_query.error): the model has none of its dynamic connections, so paths through them end at the node", broken))
	}
	finding := fmt.Sprintf("Edge model: %d intranet node(s), %d L3 VPN(s), %d L2 VPN(s), %d adjacent network(s), %d WAN circuit(s), internet node %s", lenOf(detail["intranet_nodes"]), lenOf(detail["l3_vpns"]),
		lenOf(detail["l2_vpns"]), lenOf(detail["adjacent_networks"]), lenOf(detail["wan_circuits"]), map[bool]string{true: "modelled", false: "not modelled"}[detail["internet_node"] != nil])
	if queried > 0 {
		finding += fmt.Sprintf("; %d driven by an NQE query", queried)
	}
	return result.Build(externalConnName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits,
		NextActions: []string{"investigate-reachability", "inspect-topology"},
		Evidence:    []result.Evidence{result.NewEvidence(result.EvTopology, "inspectExternalConnectivity", cx.SnapshotID, detail, finding)}})
}

func lenOf(v any) int {
	if r, ok := v.([]map[string]any); ok {
		return len(r)
	}
	return 0
}

const (
	maxDynamicRows  = 25
	maxDynamicPage  = 200
	maxQuerySourceB = 6000
)

func connRows(cs []forward.SyntheticNodeConn, limit int) []map[string]any {
	rows := make([]map[string]any, 0, min(len(cs), limit))
	for i, c := range cs {
		if i >= limit {
			break
		}
		rows = append(rows, connRow(c))
	}
	return rows
}

// connRow is one connection with every field Forward returned: the uplink and the gateway (a different interface on an access or trunk port), the
// VLAN, the VRF, how its subnets are found (listed, or discovered from the gateway's routes or BGP) and where the connection came from. An empty name,
// site or subnet list on a query-generated connection is normal: the query leaves them to Forward.
func connRow(c forward.SyntheticNodeConn) map[string]any {
	row := map[string]any{"uplink": fmt.Sprintf("%s %s", c.UplinkPort.Device, c.UplinkPort.Port)}
	if c.GatewayPort != nil {
		row["gateway"] = fmt.Sprintf("%s %s", c.GatewayPort.Device, c.GatewayPort.Port)
	}
	if c.Vlan != nil {
		row["vlan"] = *c.Vlan
	}
	for k, v := range map[string]string{"name": c.Name, "site": c.Site, "vrf": c.VRF, "subnet_discovery": c.SubnetAutoDiscovery} {
		if v != "" {
			row[k] = v
		}
	}
	if len(c.Subnets) > 0 {
		row["subnets"] = c.Subnets
	}
	if len(c.PeerIPs) > 0 {
		row["peer_ips"] = c.PeerIPs
	}
	if len(c.BackdoorLinkPorts) > 0 {
		row["backdoor_links"] = len(c.BackdoorLinkPorts)
	}
	if c.AdvertisesDefaultRoute != nil {
		row["advertises_default_route"] = *c.AdvertisesDefaultRoute
	}
	return row
}

// connSummary says what a long list of connections is made of, so no one has to page through hundreds to find out: how many per uplink port, per VRF
// and per subnet discovery method, and how many distinct uplinks and VRFs there are.
func connSummary(cs []forward.SyntheticNodeConn) map[string]any {
	byUplink, byVRF, byDiscovery := map[string]int{}, map[string]int{}, map[string]int{}
	for _, c := range cs {
		byUplink[c.UplinkPort.Device+" "+c.UplinkPort.Port]++
		v := c.VRF
		if v == "" {
			v = "(default)"
		}
		byVRF[v]++
		d := c.SubnetAutoDiscovery
		if d == "" {
			d = "(listed subnets)"
		}
		byDiscovery[d]++
	}
	return map[string]any{"distinct_uplinks": len(byUplink), "distinct_vrfs": len(byVRF), "per_uplink": topCounts(byUplink, 40), "per_vrf": topCounts(byVRF, 40), "per_subnet_discovery": topCounts(byDiscovery, 10)}
}

func topCounts(m map[string]int, max int) []map[string]any {
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
		out = append(out, map[string]any{"name": x.k, "connections": x.n})
	}
	return out
}

// sortConns orders connections by VRF, uplink device and port, VLAN so a page is stable.
func sortConns(cs []forward.SyntheticNodeConn) []forward.SyntheticNodeConn {
	out := append([]forward.SyntheticNodeConn{}, cs...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.VRF != b.VRF {
			return a.VRF < b.VRF
		}
		if a.UplinkPort.Device != b.UplinkPort.Device {
			return a.UplinkPort.Device < b.UplinkPort.Device
		}
		if a.UplinkPort.Port != b.UplinkPort.Port {
			return a.UplinkPort.Port < b.UplinkPort.Port
		}
		av, bv := 0, 0
		if a.Vlan != nil {
			av = *a.Vlan
		}
		if b.Vlan != nil {
			bv = *b.Vlan
		}
		return av < bv
	})
	return out
}

// nodeRow describes one synthetic node: the connections written on it, and, when it is driven by an NQE query, the query's id, a summary of what it
// generated and the first connections (or, with page set, one page of them). A node whose connections come only from its query has none of its own,
// so reading just its static connections would call it empty. libraryHas says whether the query is in the organization's committed library.
func nodeRow(n forward.SyntheticNode) map[string]any { return nodeRowPaged(n, 0, 0, false) }

func nodeRowPaged(n forward.SyntheticNode, offset, limit int, paged bool) map[string]any {
	row := map[string]any{"name": n.Name, "connections": connRows(n.Connections, len(n.Connections)), "static_connection_count": len(n.Connections)}
	if n.Name == "internet" || len(n.SubnetsToExclude) > 0 {
		// public prefixes kept off the internet node (internet node only); a write replaces the whole list. Shown even when empty: absent would
		// read as "not readable".
		ex := n.SubnetsToExclude
		if ex == nil {
			ex = []string{}
		}
		row["excluded_subnets"] = ex
	}
	if len(n.Translations) > 0 {
		row["translations"] = len(n.Translations)
	}
	if n.QueryID != "" {
		dyn := map[string]any{"query_id": n.QueryID}
		if r := n.QueryResult; r != nil {
			dyn["connection_count"] = len(r.Connections)
			if len(r.Connections) > 0 {
				dyn["summary"] = connSummary(r.Connections)
			}
			sorted := sortConns(r.Connections)
			if paged {
				end := min(offset+limit, len(sorted))
				if offset > len(sorted) {
					offset = len(sorted)
				}
				dyn["offset"], dyn["returned"] = offset, end-offset
				dyn["connections"] = connRows(sorted[offset:end], limit)
			} else {
				dyn["connections_shown"] = connRows(sorted, maxDynamicRows)
				if len(sorted) > maxDynamicRows {
					dyn["more"] = fmt.Sprintf("%d more; read one node with device=<name> and page with offset and limit", len(sorted)-maxDynamicRows)
				}
			}
			if r.Error != nil {
				dyn["error"] = map[string]string{"status": r.Error.Status, "message": r.Error.Message}
			}
		}
		row["nqe_query"] = dyn
	}
	return row
}

func vlanText(v *int) string {
	if v == nil {
		return "untagged"
	}
	return fmt.Sprint(*v)
}

// externalOneNode shows one synthetic node in full: its static connections, a summary of the connections its NQE query generated, a page of them, and
// whether the query is in the organization's committed library (with its source when it is). The name is matched across the internet node ("internet"),
// intranet nodes, L3 VPNs, L2 VPNs and adjacent networks.
func externalOneNode(ctx context.Context, s *fwd.Session, in externalConnInput, cx result.Context) (result.Result, error) {
	name := strings.TrimSpace(in.Device)
	limit := in.Limit
	if limit <= 0 {
		limit = maxDynamicRows
	}
	limit = min(limit, maxDynamicPage)
	var found *forward.SyntheticNode
	kind := ""
	if name == "internet" {
		n, err := s.InternetNode(ctx, in.NetworkID)
		if err != nil {
			return result.Result{}, err
		}
		found, kind = n, "internet"
	} else {
		for _, k := range []struct {
			name string
			kind forward.SyntheticNodeKind
		}{{"intranet", forward.SyntheticIntranet}, {"l3vpn", forward.SyntheticL3VPN}, {"l2vpn", forward.SyntheticL2VPN}, {"adjacent-network", forward.SyntheticAdjacentNetwork}} {
			n, err := s.SyntheticNode(ctx, in.NetworkID, k.kind, name)
			if err != nil {
				continue // a kind the network lacks or this Forward does not serve
			}
			if n != nil {
				found, kind = n, k.name
				break
			}
		}
	}
	if found == nil {
		return result.NewUnknown(externalConnName, fmt.Sprintf("The network has no synthetic node %q", name), cx,
			[]string{"names are matched exactly across the internet node, intranet nodes, L3 VPNs, L2 VPNs and adjacent networks; read without device to list them"}, result.Options{NextActions: []string{"inspect-topology"}})
	}
	row := nodeRowPaged(*found, in.Offset, limit, true)
	row["kind"] = kind
	var limits []string
	if found.QueryID != "" {
		lib := map[string]any{"query_id": found.QueryID}
		if q, err := s.OrgQueryByID(ctx, found.QueryID); err != nil {
			limits = append(limits, "the query could not be looked up in the library: "+err.Error())
		} else if q == nil {
			lib["in_library"] = false
			limits = append(limits, "the node's query is NOT in the organization's committed library (it was deleted, was never committed, or lives elsewhere), so its source cannot be read; the connections shown are the result Forward stored when the query last ran")
		} else {
			lib["in_library"] = true
			lib["intent"] = q.Intent
			src := q.SourceCode
			if len(src) > maxQuerySourceB {
				src = src[:maxQuerySourceB]
				lib["source_truncated"] = true
			}
			lib["source"] = src
		}
		row["nqe_query_library"] = lib
		limits = append(limits, "dynamic connections are Forward's computed result for this query; name, site and subnets are empty when the query leaves them to subnet discovery (see subnet_discovery)")
	}
	finding := fmt.Sprintf("%s node %q", kind, found.Name)
	if q, ok := row["nqe_query"].(map[string]any); ok {
		finding += fmt.Sprintf(": %v connection(s) from its NQE query, %v static", q["connection_count"], row["static_connection_count"])
	}
	return result.Build(externalConnName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, NextActions: []string{"investigate-reachability", "inspect-topology"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvTopology, "inspectSyntheticNode", nil, row, finding)}})
}

// nodeQueryCount is how many connections the node's query generated (0 when it has none or has not run).
// inertInternetNodeLimit is the sentence that stops "0 devices exposed" being read as "safe": it is returned when the network has no internet node, or one with no connection and no
// query rows, since Forward then computes no internet exposure at all. "" when the node is live or could not be read (a failed read is not evidence either way).
func inertInternetNodeLimit(ctx context.Context, s *fwd.Session, networkID string) string {
	if networkID == "" {
		return ""
	}
	n, err := s.InternetNode(ctx, networkID)
	if err != nil {
		return ""
	}
	if n == nil {
		return "this network has no internet node, so Forward computes no internet exposure for it: zero devices internet addressable here is the absence of the analysis, not evidence that nothing is exposed (inspect-topology kind external, plan-synthetic-device)"
	}
	if len(n.Connections)+nodeQueryCount(n) == 0 {
		return "the internet node has NO connection, so it owns no addresses and Forward computes no internet exposure: zero devices internet addressable here is the absence of the analysis, not evidence that nothing is exposed (give the node a connection first: plan-synthetic-device)"
	}
	return ""
}

func nodeQueryCount(n *forward.SyntheticNode) int {
	if n == nil || n.QueryResult == nil {
		return 0
	}
	return len(n.QueryResult.Connections)
}
