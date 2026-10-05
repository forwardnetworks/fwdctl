package skills

import (
	"context"
	"encoding/json"
	"fmt"
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
	shown := devices
	if len(shown) > 8 {
		shown = shown[:8]
	}
	// every hop with the interfaces the packet entered and left by, so two paths (two snapshots or two networks) can be compared without the raw path search
	hops := make([]map[string]any, 0, len(p.Hops))
	for i, h := range p.Hops {
		if i == maxPathHops {
			break
		}
		hops = append(hops, map[string]any{"device": hopName(h), "ingress_interface": nilIfEmpty(h.IngressInterface), "egress_interface": nilIfEmpty(h.EgressInterface)})
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
	doc, err := s.SearchPaths(ctx, in.NetworkID, fwd.PathQuery{
		DstIP: in.DstIP, SrcIP: in.SrcIP, From: in.From, Protocol: protocolText(in.Protocol),
		SrcPort: in.SrcPort, DstPort: in.DstPort, Intent: in.Intent, SnapshotID: fwd.SnapshotID(cx),
		MaxResults: in.MaxResults, MaxSeconds: in.MaxSeconds,
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
	pathEv := func() []result.Evidence {
		ev := uniquePathEvidence(ordered, func(c classified) fwd.Path { return c.path }, func(c classified) string { return c.class }, sid, maxReachabilityEvidence)
		if distinct := countDistinct(all, func(c classified) fwd.Path { return c.path }, func(c classified) string { return c.class }); distinct > len(ev) {
			limits = append(limits, fmt.Sprintf("%d distinct paths exist in this answer; %d are shown (one per outcome first); narrow the flow (src_port, dst_port, from) to see the rest", distinct, len(ev)))
		}
		return ev
	}
	if delivered > 0 {
		return result.Build(reachabilityName, result.OK,
			fmt.Sprintf("Traffic is delivered (%d of %d returned paths)", delivered, len(doc.Paths)),
			result.Deterministic, cx, result.Options{Limits: limits, Evidence: pathEv(), NextActions: []string{"verify-change"}})
	}
	if len(real) == 0 || doc.TimedOut {
		if len(real) == 0 {
			limits = append(limits, "only incomplete or unclassified outcomes were returned")
		}
		ev := uniquePathEvidence(all, func(c classified) fwd.Path { return c.path }, func(c classified) string { return c.class }, sid, 3)
		finding, next := "The returned paths do not establish whether the flow is delivered", []string{"investigate-collection-failure"}
		for _, c := range all {
			if mp := missingPeerOf(c.path); mp != nil && len(real) == 0 {
				finding = "The flow is not established as delivered: " + missingPeerText(mp)
				next = []string{"plan-synthetic-device", "inspect-edge", "investigate-collection-failure"}
				limits = append(limits, "a Missing Peer is an incomplete model, not a verdict: the classification stays incomplete_model and says nothing about what happens beyond that uplink")
				break
			}
		}
		return result.NewUnknown(reachabilityName, finding, cx, limits, result.Options{Evidence: ev, NextActions: next})
	}
	ev := pathEv()
	return result.Build(reachabilityName, result.Failed, reachabilityExplain[real[0].class], result.Deterministic, cx,
		result.Options{Limits: limits, Evidence: ev, NextActions: []string{"inspect-topology", "inspect-device-files", "plan-troubleshoot-connectivity"}})
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
