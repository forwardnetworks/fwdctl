package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"regexp"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const reachabilityName = "investigate-reachability"

func init() { Register(reachabilityName, investigateReachability) }

type reachabilityInput struct {
	NetworkID  string          `json:"network_id"`
	DstIP      string          `json:"dst_ip"`
	SrcIP      string          `json:"src_ip"`
	From       string          `json:"from"`
	Protocol   json.RawMessage `json:"protocol"`
	SrcPort    string          `json:"src_port"`
	DstPort    string          `json:"dst_port"`
	SnapshotID string          `json:"snapshot_id"`
	MaxResults int             `json:"max_results"`
	MaxSeconds int             `json:"max_seconds"`
	// ReturnPath also asks Forward for the return flow (destination back to source) and summarizes it beside the forward answer.
	ReturnPath bool `json:"return_path"`
	// Intent is how Forward orders the paths it returns: PREFER_DELIVERED (default), PREFER_VIOLATIONS or VIOLATIONS_ONLY. With a result cap, the intent decides which paths exist in the answer at all.
	Intent string `json:"intent"`
}

const maxReachabilityEvidence = 6

var reachabilityExplain = map[string]string{
	"security_denied": "Traffic reaches the destination but is denied by security policy",
	"missing_route":   "Traffic is blackholed: no matching route or rule at the last hop",
	"dropped":         "Traffic is explicitly dropped at the last hop",
	"not_admitted":    "The first hop does not admit the traffic (VLAN, VRF or interface state)",
	"routing_loop":    "Traffic loops in the network",
	"unreachable":     "The destination is unreachable",
}

// protocolText turns a JSON string or number into the text ParseProtocol takes.
func protocolText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return strings.TrimSpace(string(raw))
}

func hopName(h fwd.Hop) string {
	if h.DisplayName != "" {
		return h.DisplayName
	}
	if h.Device != "" {
		return h.Device
	}
	return "?"
}

// addNetworkFunctions adds what Forward says a hop evaluated: the ACLs (name, where, action) and the security zones the packet entered and left by.
// A hop with none gets no keys, so a device without ACLs or zones is shown as before.
func addNetworkFunctions(row map[string]any, h fwd.Hop) {
	if len(h.ACLs) > 0 {
		acls := make([]map[string]any, 0, len(h.ACLs))
		for _, a := range h.ACLs {
			acls = append(acls, map[string]any{"name": a.Name, "context": nilIfEmpty(a.Context), "action": nilIfEmpty(a.Action)})
		}
		row["acls"] = acls
	}
	if h.IngressZone != "" {
		row["ingress_zone"] = h.IngressZone
	}
	if h.EgressZone != "" {
		row["egress_zone"] = h.EgressZone
	}
}

func pathEvidence(p fwd.Path, class string, snapshot *string) result.Evidence {
	devices := make([]string, 0, len(p.Hops))
	for _, h := range p.Hops {
		devices = append(devices, hopName(h))
	}
	last := map[string]any{"device": nil, "ingress_interface": nil, "egress_interface": nil}
	if n := len(p.Hops); n > 0 {
		h := p.Hops[n-1]
		last = map[string]any{"device": hopName(h), "ingress_interface": nilIfEmpty(h.IngressInterface),
			"egress_interface": nilIfEmpty(h.EgressInterface)}
		addNetworkFunctions(last, h)
	}
	// A synthetic device names its ports "<vrf>/<uplink device>/<uplink port>": the VRF the packet arrived in and the collected port it came from. Forward
	// gives no drop reason in its path answer, so say what the hop shows and where to look, never why.
	if n := len(p.Hops); n > 1 && class == "dropped" {
		h := p.Hops[n-1]
		if parts := strings.SplitN(h.IngressInterface, "/", 3); len(parts) == 3 && h.EgressInterface == "" && parts[1] == p.Hops[n-2].Device {
			last["vrf"] = parts[0]
			last["entered_from"] = parts[1] + " " + parts[2]
			last["drop_reason"] = "not given by Forward. The packet entered " + hopName(h) + " on VRF " + parts[0] + " from " + parts[1] + " " + parts[2] +
				" and has no egress. If " + hopName(h) + " is a synthetic device, it forwards only to a connection whose VRF and subnets own the destination: read it with inspect-topology kind external, device " + hopName(h) +
				" (a transit L3 VPN drops what none of its connections owns; an internet node owns unassigned public space once it has a connection)"
		}
	}
	shown, _ := result.CapRow(devices, 8)
	// every hop with the interfaces the packet entered and left by, so two paths (two snapshots or two networks) can be compared without the raw path search
	hops := make([]map[string]any, 0, len(p.Hops))
	for i, h := range p.Hops {
		if i == maxPathHops {
			break
		}
		row := map[string]any{"device": hopName(h), "ingress_interface": nilIfEmpty(h.IngressInterface), "egress_interface": nilIfEmpty(h.EgressInterface)}
		addNetworkFunctions(row, h)
		hops = append(hops, row)
	}
	// the interface the packet entered the first hop by, so a caller can keep only the paths from the ingress it means without a second query
	var src any
	if len(p.Hops) > 0 {
		src = map[string]any{"device": hopName(p.Hops[0]), "ingress_interface": nilIfEmpty(p.Hops[0].IngressInterface)}
	}
	detail := map[string]any{
		"source_hop": src, "classification": class, "forwarding_outcome": p.ForwardingOutcome, "security_outcome": nilIfEmpty(p.SecurityOutcome),
		"hop_count": len(p.Hops), "devices": devices, "hops": hops, "last_hop": last,
	}
	if mp := missingPeerOf(p); mp != nil {
		detail["missing_peer"] = mp
	}
	return result.NewEvidence(result.EvPath, "getPaths", snapshot, detail, class+": "+strings.Join(shown, " > "))
}

// maxPathHops bounds the per-hop list of one path.
const maxPathHops = 40

// pathSignature identifies a path by its classification and the devices and interfaces it crosses, so identical paths can be reported once.
func pathSignature(p fwd.Path, class string) string {
	var b strings.Builder
	b.WriteString(class)
	for _, h := range p.Hops {
		b.WriteString("|" + hopName(h) + ">" + h.IngressInterface + ">" + h.EgressInterface)
	}
	return b.String()
}

// uniquePathEvidence returns the evidence of at most limit DIFFERENT paths; a path that repeats one already listed (Forward returns the same path for several equal-cost or
// duplicate matches) is counted on the first one as identical_paths instead of being listed again.
func uniquePathEvidence[T any](items []T, path func(T) fwd.Path, class func(T) string, snapshot *string, limit int) []result.Evidence {
	var ev []result.Evidence
	index := map[string]int{}
	count := map[string]int{}
	for _, it := range items {
		sig := pathSignature(path(it), class(it))
		count[sig]++
		if _, seen := index[sig]; seen {
			continue
		}
		if len(ev) == limit {
			continue
		}
		index[sig] = len(ev)
		ev = append(ev, pathEvidence(path(it), class(it), snapshot))
	}
	for sig, i := range index {
		if n := count[sig]; n > 1 {
			ev[i].Detail["identical_paths"] = n
		}
	}
	return ev
}

const missingPeerSuffix = "-missing-peer"

// subVLAN reads the VLAN from a subinterface name ("po1000.698" is VLAN 698); ok is false when the name has no numeric suffix after a dot or the number is 0.
func subVLAN(iface string) (int, bool) {
	i := strings.LastIndex(iface, ".")
	if i < 0 || i == len(iface)-1 {
		return 0, false
	}
	n := 0
	for _, c := range iface[i+1:] {
		if c < '0' || c > '9' || n > 99999 {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, n > 0 && n <= 4094
}

// missingPeerOf recognises Forward's Missing Peer marker: a last hop named "<device>-missing-peer" stands for an uplink of <device> that carries traffic (a VLAN or
// subinterface) to a peer no synthetic device claims. Nil when the last hop is an ordinary device. The interface is the hop's ingress interface, which Forward writes
// "<device>/<interface>"; a vlan is given only when the interface name carries one.
func missingPeerOf(p fwd.Path) map[string]any {
	n := len(p.Hops)
	if n == 0 {
		return nil
	}
	h := p.Hops[n-1]
	name := h.Device
	if !strings.HasSuffix(name, missingPeerSuffix) {
		name = h.DisplayName
	}
	dev, ok := strings.CutSuffix(name, missingPeerSuffix)
	if !ok || dev == "" {
		return nil
	}
	iface := strings.TrimPrefix(h.IngressInterface, dev+"/")
	m := map[string]any{"device": dev, "interface": nilIfEmpty(iface), "hop_name": name}
	if v, ok := subVLAN(iface); ok {
		m["vlan"] = v
	}
	return m
}

// missingPeerText says in plain words what a Missing Peer marker means and what to do about it.
func missingPeerText(m map[string]any) string {
	where := fmt.Sprint(m["device"])
	if m["interface"] != nil {
		where += " " + fmt.Sprint(m["interface"])
	}
	return "Traffic leaves " + where + " toward a peer that is not modelled (Forward's Missing Peer marker " + fmt.Sprint(m["hop_name"]) +
		"): no synthetic device claims that uplink, so the path cannot be followed past it. Model the peer with a synthetic device (plan-synthetic-device; inspect-edge finds the uplinks and says which are unclaimed)"
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func investigateReachability(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in reachabilityInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	switch in.Intent {
	case "":
		in.Intent = "PREFER_DELIVERED"
	case "PREFER_DELIVERED", "PREFER_VIOLATIONS", "VIOLATIONS_ONLY":
	default:
		return result.Result{}, fmt.Errorf("%w: intent must be PREFER_DELIVERED, PREFER_VIOLATIONS or VIOLATIONS_ONLY", ErrInvalidInput)
	}
	mx, mgroups, merr := parseMatrix(raw, in) // validated before any request
	if merr != nil {
		return result.Result{}, merr
	}
	if mx == nil && strings.TrimSpace(in.DstIP) == "" {
		return result.Result{}, fmt.Errorf("%w: dst_ip is required for one flow (or give matrix for every pair of several endpoints)", ErrInvalidInput)
	}
	for name, v := range map[string]string{"dst_ip": in.DstIP, "src_ip": in.SrcIP} {
		if looksLikeHostname(v) {
			return result.Result{}, fmt.Errorf("%w: %s %q is a host name; Forward does not resolve DNS and takes an IP address or prefix. Resolve it yourself (the address the host had at the time of the incident) and pass that", ErrInvalidInput, name, v)
		}
	}
	snap, err := resolveSnapshot(ctx, s, in.NetworkID, in.SnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	cx := fwd.Context(in.NetworkID, snap)
	if !fwd.IsReady(snap) {
		return result.NewUnknown(reachabilityName, "No processed snapshot is available to answer from", cx,
			[]string{"no processed snapshot; nothing was measured"},
			result.Options{NextActions: []string{"investigate-collection-failure"}})
	}
	sid := fwd.SnapshotIDPtr(snap)
	if mx != nil {
		return reachabilityMatrix(ctx, s, in, mx, mgroups, fwd.SnapshotID(cx), cx, sid)
	}
	doc, err := s.SearchPaths(ctx, in.NetworkID, fwd.PathQuery{
		DstIP: in.DstIP, SrcIP: in.SrcIP, From: in.From, Protocol: protocolText(in.Protocol),
		SrcPort: in.SrcPort, DstPort: in.DstPort, Intent: in.Intent, SnapshotID: fwd.SnapshotID(cx),
		MaxResults: in.MaxResults, MaxSeconds: in.MaxSeconds, ReturnPaths: returnPathResults(in.ReturnPath), WithNetworkFunctions: true,
	})
	if err != nil {
		return result.Result{}, err
	}
	var limits []string
	if doc.TimedOut {
		limits = append(limits, "path search hit its time budget; results may be incomplete")
	}
	if doc.TotalType == "LOWER_BOUND" {
		limits = append(limits, "more paths exist than were returned")
	}
	if cx.State == "predicted" {
		limits = append(limits, "read from a predicted snapshot, not collected state")
	}
	if len(doc.Paths) == 0 {
		limits = append(limits, "the search returned no paths; the source or destination may not be locatable in the model")
		return result.NewUnknown(reachabilityName, "The path search returned no paths, so reachability is undetermined", cx, limits,
			result.Options{NextActions: []string{"investigate-collection-failure"}})
	}

	type classified struct {
		class string
		path  fwd.Path
	}
	var all, real []classified
	delivered := 0
	for _, p := range doc.Paths {
		c := classified{fwd.Classify(p), p}
		all = append(all, c)
		switch c.class {
		case "delivered":
			delivered++
		case "incomplete_model", "unclassified":
		default:
			real = append(real, c)
		}
	}
	// Every distinct outcome is reported: one path per classification first (delivered, then the failures, then incomplete models), then more distinct paths up to the evidence cap.
	// What was left out is counted in the limits, so two networks can be compared without a path silently missing.
	ordered := orderByClass(all, func(c classified) string { return c.class })
	outcomes, _ := outcomeSummary(all, func(c classified) string { return c.class })
	limits = append(limits, outcomes+" (intent "+in.Intent+"). Forward chooses which paths it returns, so another intent (PREFER_VIOLATIONS, VIOLATIONS_ONLY) or a larger max_results can show paths this answer does not")
	if l := locationLimit("source", doc.SrcLocationType); l != "" {
		limits = append(limits, l)
	}
	if l := locationLimit("destination", doc.DstLocationType); l != "" {
		limits = append(limits, l)
	}
	retFlow, retVerdict, retLimit := returnFlowSummary(doc, in.ReturnPath)
	if retLimit != "" {
		limits = append(limits, retLimit)
	}
	var omitted []result.Omission
	pathEv := func() []result.Evidence {
		ev := uniquePathEvidence(ordered, func(c classified) fwd.Path { return c.path }, func(c classified) string { return c.class }, sid, maxReachabilityEvidence)
		if retFlow != nil && len(ev) > 0 {
			ev[0].Detail["return_flow"] = retFlow
		}
		if len(ev) > 0 {
			if doc.SrcLocationType != "" {
				ev[0].Detail["src_location_type"] = doc.SrcLocationType
			}
			if doc.DstLocationType != "" {
				ev[0].Detail["dst_location_type"] = doc.DstLocationType
			}
		}
		if distinct := countDistinct(all, func(c classified) fwd.Path { return c.path }, func(c classified) string { return c.class }); distinct > len(ev) {
			omitted = append(omitted, result.Omission{What: "distinct paths", Total: distinct, Shown: len(ev), Next: "one path per outcome is shown first; narrow the flow (src_port, dst_port, from) to see the rest"})
		}
		return ev
	}
	if delivered > 0 {
		finding := fmt.Sprintf("Traffic is delivered (%d of %d returned paths)", delivered, len(doc.Paths))
		next := []string{"verify-change"}
		if in.ReturnPath { // a both-ways question: the status is about both directions
			switch retVerdict {
			case "delivered":
				finding += "; the return flow is delivered too"
			case "blocked":
				return result.Build(reachabilityName, result.Failed, finding+", but the return flow is blocked: the connection cannot complete both ways",
					result.Deterministic, cx, result.Options{Limits: limits, Evidence: pathEv(), Omitted: omitted, NextActions: []string{"plan-troubleshoot-connectivity", "inspect-device-files"}})
			default:
				return result.NewUnknown(reachabilityName, finding+", but the return flow could not be determined, so the connection is not established as working both ways",
					cx, limits, result.Options{Evidence: pathEv(), Omitted: omitted, NextActions: []string{"plan-troubleshoot-connectivity", "inspect-topology"}})
			}
		}
		return result.Build(reachabilityName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, Evidence: pathEv(), Omitted: omitted, NextActions: next})
	}
	if len(real) == 0 || doc.TimedOut {
		if len(real) == 0 {
			limits = append(limits, "only incomplete or unclassified outcomes were returned")
		}
		ev := uniquePathEvidence(all, func(c classified) fwd.Path { return c.path }, func(c classified) string { return c.class }, sid, 3)
		if retFlow != nil && len(ev) > 0 {
			ev[0].Detail["return_flow"] = retFlow
		}
		if len(ev) > 0 {
			if doc.SrcLocationType != "" {
				ev[0].Detail["src_location_type"] = doc.SrcLocationType
			}
			if doc.DstLocationType != "" {
				ev[0].Detail["dst_location_type"] = doc.DstLocationType
			}
		}
		finding, next := "The returned paths do not establish whether the flow is delivered", []string{"investigate-collection-failure"}
		for _, c := range all {
			if len(real) == 0 && c.path.ForwardingOutcome == "DELIVERED_TO_INCORRECT_LOCATION" {
				finding = "The flow leaves the modelled network and the model does not say whether it arrives: " + leavesModelNote
				break
			}
		}
		for _, c := range all {
			if mp := missingPeerOf(c.path); mp != nil && len(real) == 0 {
				finding = "The flow is not established as delivered: " + missingPeerText(mp)
				next = []string{"plan-synthetic-device", "inspect-edge", "investigate-collection-failure"}
				limits = append(limits, "a Missing Peer is an incomplete model, not a verdict: the classification stays incomplete_model and says nothing about what happens beyond that uplink")
				break
			}
		}
		return result.NewUnknown(reachabilityName, finding, cx, limits, result.Options{Evidence: ev, Omitted: omitted, NextActions: next})
	}
	ev := pathEv()
	return result.Build(reachabilityName, result.Failed, reachabilityExplain[real[0].class], result.Deterministic, cx,
		result.Options{Limits: limits, Evidence: ev, Omitted: omitted, NextActions: []string{"inspect-topology", "inspect-device-files", "plan-troubleshoot-connectivity"}})
}

// orderByClass puts the first item of each classification first (in order of first appearance), then the rest in their original order.
func orderByClass[T any](items []T, class func(T) string) []T {
	seen := map[string]bool{}
	var firsts, rest []T
	for _, it := range items {
		if c := class(it); !seen[c] {
			seen[c] = true
			firsts = append(firsts, it)
		} else {
			rest = append(rest, it)
		}
	}
	return append(firsts, rest...)
}

// outcomeSummary says how many returned paths fall in each classification ("5 paths returned: 3 security_denied, 2 incomplete_model").
func outcomeSummary[T any](items []T, class func(T) string) (string, []string) {
	counts := map[string]int{}
	var order []string
	for _, it := range items {
		c := class(it)
		if counts[c] == 0 {
			order = append(order, c)
		}
		counts[c]++
	}
	parts := make([]string, 0, len(order))
	for _, c := range order {
		parts = append(parts, fmt.Sprintf("%d %s", counts[c], c))
	}
	return fmt.Sprintf("%d paths returned: %s", len(items), strings.Join(parts, ", ")), order
}

// countDistinct counts the paths that differ in classification, devices or interfaces.
func countDistinct[T any](items []T, path func(T) fwd.Path, class func(T) string) int {
	seen := map[string]bool{}
	for _, it := range items {
		seen[pathSignature(path(it), class(it))] = true
	}
	return len(seen)
}

// returnPathResults is how many return paths to ask Forward for when return_path is set (it sends none unless asked).
func returnPathResults(on bool) int {
	if on {
		return 5
	}
	return 0
}

// returnFlowSummary describes the return flow (destination back to source) beside the forward answer: the summary for the evidence, its verdict (delivered, blocked or
// undetermined) and a limit when it was asked for but Forward returned no return paths. It returns nothing unless return_path was asked for.
func returnFlowSummary(doc fwd.PathResult, asked bool) (map[string]any, string, string) {
	if !asked {
		return nil, "", ""
	}
	if len(doc.ReturnPaths) == 0 {
		return map[string]any{"paths": 0, "verdict": "undetermined"}, "undetermined", "return_path was asked for but Forward returned no return paths; the return flow is undetermined, not delivered"
	}
	delivered, classes := 0, map[string]int{}
	for _, p := range doc.ReturnPaths {
		k := fwd.Classify(p)
		classes[k]++
		if k == "delivered" {
			delivered++
		}
	}
	verdict := fwd.Verdict(doc.ReturnPaths, doc.TimedOut)
	return map[string]any{"paths": len(doc.ReturnPaths), "delivered": delivered, "outcomes": classes, "verdict": verdict}, verdict, ""
}

// hostnameRE matches a dotted name whose last label has a letter, which no IPv4 address or prefix has.
var hostnameRE = regexp.MustCompile(`^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)*\.[A-Za-z][A-Za-z0-9-]*\.?$`)

// looksLikeHostname says v is a DNS name rather than an address: an address or prefix parses, a name does not.
func looksLikeHostname(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" || strings.ContainsAny(v, "/:") {
		return false
	}
	if _, err := netip.ParseAddr(v); err == nil {
		return false
	}
	return hostnameRE.MatchString(v)
}

// leavesModelNote explains the pair forwarding outcome DELIVERED_TO_INCORRECT_LOCATION with an incomplete model: a path that ends where the model does not go on (for example out an overlay tunnel to something that is not modelled) reads like a failure but is neither a drop in the model nor a delivery.
const leavesModelNote = "the path ends where the model does not go on (forwarding outcome DELIVERED_TO_INCORRECT_LOCATION, classification incomplete_model), for example out an overlay or tunnel toward something that is not modelled: it is not a drop in the model and not a delivery; only a real test (such as Forward's connectivity test) shows whether it arrives"

// locationLimit says how Forward located a flow's source or destination address, when that is not an interface or host: ROUTE means from injected routes (the address is in no connected subnet), INTERFACE_ATTACHED_SUBNET that it
// fell inside an attached subnet, INTERNET that it was treated as an internet address. Without it a source placed by route reads like one found on a device.
func locationLimit(end, kind string) string {
	switch kind {
	case "ROUTE":
		return fmt.Sprintf("the %s address was located from injected routes (srcIpLocationType/dstIpLocationType ROUTE), not from an interface, a host or an attached subnet: the path starts or ends where the model's routes put the address, so read it with that in mind", end)
	case "INTERFACE_ATTACHED_SUBNET":
		return fmt.Sprintf("the %s address was located by the attached subnet it falls in, not by an interface that carries it or a host that was seen", end)
	case "INTERNET":
		return fmt.Sprintf("the %s address was treated as an internet address", end)
	case "CO_LOCATED", "SNAT", "MULTICAST":
		return fmt.Sprintf("the %s address was located as %s", end, kind)
	}
	return ""
}
