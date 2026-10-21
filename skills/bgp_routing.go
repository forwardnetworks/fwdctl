package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// routingInput is inspect-bgp-neighbors' routing option: Forward's routing app (prefix search, trace, details, per-device prefix info, installed prefixes) as a read. It answers "where does this prefix come from, who accepts or rejects it, and why".
type routingInput struct {
	// View: search (default), trace, details, device or installed.
	View           string       `json:"view"`
	Prefix         string       `json:"prefix"`
	Router         *routingNode `json:"router"`          // the device (and VRF) to trace or explain
	OriginatedFrom *routingNode `json:"originated_from"` // trace and details: the origin; taken from the search when absent
	Outcome        string       `json:"outcome"`         // search filter, and trace: the outcome to follow; taken from the search when absent
	Path           int          `json:"path"`            // details: which trace path (0 is the first)
	Hop            *int         `json:"hop"`             // details: which hop of that path (the last, the router, by default)
	Device         string       `json:"device"`          // device and installed
	Vendors        []string     `json:"vendors"`         // installed: lowercase vendor-OS names such as cisco_ios
	Limit          int          `json:"limit"`           // installed
}

type routingNode struct {
	Device string `json:"device"`
	VRF    string `json:"vrf"`
}

func parseRouting(raw json.RawMessage) (routingInput, bool, error) {
	var probe struct {
		Routing    *routingInput   `json:"routing"`
		Advertised json.RawMessage `json:"advertised"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return routingInput{}, false, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if probe.Routing == nil {
		return routingInput{}, false, nil
	}
	if len(probe.Advertised) > 0 && string(probe.Advertised) != "null" {
		return routingInput{}, false, fmt.Errorf("%w: routing and advertised are two different questions; give one", ErrInvalidInput)
	}
	r := *probe.Routing
	r.View = strings.ToLower(strings.TrimSpace(r.View))
	if r.View == "" {
		r.View = "search"
	}
	switch r.View {
	case "search", "trace", "details", "device", "installed":
	default:
		return routingInput{}, false, fmt.Errorf("%w: routing.view must be search, trace, details, device or installed", ErrInvalidInput)
	}
	if r.View != "installed" {
		if p, err := netip.ParsePrefix(strings.TrimSpace(r.Prefix)); err != nil {
			return routingInput{}, false, fmt.Errorf("%w: routing.prefix must be a CIDR prefix such as 10.9.0.0/24", ErrInvalidInput)
		} else {
			r.Prefix = p.Masked().String()
		}
	}
	switch r.View {
	case "trace", "details":
		if r.Router == nil || strings.TrimSpace(r.Router.Device) == "" {
			return routingInput{}, false, fmt.Errorf("%w: routing view %s needs routing.router (the device to explain, and its vrf if not default)", ErrInvalidInput, r.View)
		}
	case "device":
		if strings.TrimSpace(r.Device) == "" {
			return routingInput{}, false, fmt.Errorf("%w: routing view device needs routing.device", ErrInvalidInput)
		}
	case "installed":
		if (strings.TrimSpace(r.Device) == "") == (len(r.Vendors) == 0) {
			return routingInput{}, false, fmt.Errorf("%w: routing view installed needs routing.device or routing.vendors (one of them, not both)", ErrInvalidInput)
		}
	}
	return r, true, nil
}

func (n *routingNode) dv() forward.DeviceVRF {
	if n == nil {
		return forward.DeviceVRF{}
	}
	return forward.DeviceVRF{Device: strings.TrimSpace(n.Device), VRF: firstNonEmpty(strings.TrimSpace(n.VRF), "default")}
}

const maxRoutingDevices = 50

var routingPreviewLimits = []string{
	"Forward's routing app is a preview route; this reads BGP (the route names Forward serves in this build are bgp-prefix-*), and its answers can change between builds",
	"it needs the network ADMIN role (the operation BGP_PREFIX_SEARCH; an OPERATOR is refused with 403), the organization property PREDICT_MODELING, and a snapshot processed to the creation stage",
}

// bgpRouting is inspect-bgp-neighbors with routing. Everything is a read (the search and trace calls are POSTs that change nothing).
func bgpRouting(ctx context.Context, s *fwd.Session, in inspectBGPNeighborsInput, r routingInput, sid string, cx result.Context) (result.Result, error) {
	limits := append([]string(nil), routingPreviewLimits...)
	fail := func(err error) (result.Result, error) {
		if res, ok := denialResult(inspectBGPNeighborsName, err, cx, "the routing app"); ok {
			return res, nil
		}
		if errors.Is(err, forward.ErrRoutingLookup) {
			return result.NewUnknown(inspectBGPNeighborsName, "Forward could not resolve that in this snapshot: "+err.Error(), cx,
				append(limits, "the lookup matched nothing (a device Forward has no model for, a node from another snapshot, or a stale route set); nothing is known from this answer"), result.Options{NextActions: []string{"inspect-bgp-neighbors"}})
		}
		return result.Result{}, err
	}
	rc := s.Client.Routing
	detail := map[string]any{"view": r.View, "snapshot_id": sid}
	if r.Prefix != "" {
		detail["prefix"] = r.Prefix
	}
	var finding string
	switch r.View {
	case "search":
		res, _, err := rc.PrefixSearch(ctx, in.NetworkID, sid, forward.RoutingPrefixSearch{Prefix: r.Prefix, Router: ptrDV(r.Router), OriginatedFrom: ptrDV(r.OriginatedFrom), Outcome: r.Outcome})
		if err != nil {
			return fail(err)
		}
		detail["origin"] = devicesOut(res.Origin)
		outs := map[string]any{}
		keys := make([]string, 0, len(res.DevicesByOutcome))
		for k := range res.DevicesByOutcome {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			d := res.DevicesByOutcome[k]
			shown, total := result.CapRow(d, maxRoutingDevices)
			outs[k] = map[string]any{"count": total, "devices": devicesOut(shown)}
			parts = append(parts, fmt.Sprintf("%d %s", len(d), k))
			if total > len(shown) {
				limits = append(limits, fmt.Sprintf("outcome %s lists the first %d of %d devices", k, len(shown), total))
			}
		}
		detail["devices_by_outcome"] = outs
		if len(res.Origin) == 0 && len(keys) == 0 {
			return result.NewUnknown(inspectBGPNeighborsName, fmt.Sprintf("Forward's routing app has no BGP data for %s in snapshot %s", r.Prefix, sid), cx,
				append(limits, "no origin and no outcome came back: the prefix is not in the BGP model of this snapshot (or it is not BGP); nothing is known about it from here"), result.Options{NextActions: []string{"inspect-bgp-neighbors"}})
		}
		finding = fmt.Sprintf("Prefix %s: originated at %s; %s (snapshot %s)", r.Prefix, nodeList(res.Origin), strings.Join(parts, ", "), sid)
	case "trace", "details":
		paths, filled, err := routingTrace(ctx, s, in.NetworkID, sid, r)
		if err != nil {
			return fail(err)
		}
		if filled != "" {
			limits = append(limits, filled)
		}
		if len(paths) == 0 {
			return result.NewUnknown(inspectBGPNeighborsName, fmt.Sprintf("No propagation path of %s to %s was returned", r.Prefix, r.Router.Device), cx,
				append(limits, "Forward returned no path: the router did not learn the prefix by BGP in this snapshot, or the origin/outcome given do not match; run view search to see them"), result.Options{NextActions: []string{"inspect-bgp-neighbors"}})
		}
		out := make([][]map[string]any, len(paths))
		for i, p := range paths {
			for _, n := range p {
				out[i] = append(out[i], map[string]any{"device": n.Device, "vrf": n.VRF, "as_number": n.ASNumber, "protocol": n.Protocol, "import_policies": n.ImportPolicyNames, "export_policies": n.ExportPolicyNames})
			}
		}
		detail["paths"] = out
		finding = fmt.Sprintf("%d propagation path(s) of %s to %s (snapshot %s)", len(paths), r.Prefix, r.Router.Device, sid)
		if r.View == "details" {
			if r.Path < 0 || r.Path >= len(paths) {
				return result.Result{}, fmt.Errorf("%w: routing.path %d is outside the %d path(s) found", ErrInvalidInput, r.Path, len(paths))
			}
			p := paths[r.Path]
			hop := len(p) - 1
			if r.Hop != nil {
				hop = *r.Hop
			}
			if hop < 0 || hop >= len(p) {
				return result.Result{}, fmt.Errorf("%w: routing.hop %d is outside the %d hop(s) of path %d", ErrInvalidInput, hop, len(p), r.Path)
			}
			req := forward.RoutingPrefixDetailsRequest{Prefix: r.Prefix, Node: p[hop]}
			if hop > 0 {
				req.PreviousNode = &p[hop-1]
			}
			if hop > 1 {
				req.PreviousPreviousNode = &p[hop-2]
			}
			if hop+1 < len(p) {
				req.NextNode = &p[hop+1]
			}
			d, _, err := rc.PrefixDetails(ctx, in.NetworkID, sid, req)
			if err != nil {
				return fail(err)
			}
			detail["hop"] = map[string]any{"path": r.Path, "index": hop, "device": d.Device, "vrf": d.VRF, "attributes": d.Attributes, "policies": d.Policies}
			finding = fmt.Sprintf("Route details of %s on %s (path %d, hop %d; snapshot %s)", r.Prefix, d.Device, r.Path, hop, sid)
			limits = append(limits, "attributes and policies are Forward's raw answer: BGP attributes (asPath, localPref, med, communities, origin, next hop) and the policy rules that matched, by rule type")
		}
	case "device":
		d, _, err := rc.DevicePrefixInfo(ctx, in.NetworkID, sid, r.Device, r.Prefix)
		if err != nil {
			return fail(err)
		}
		detail["device"] = map[string]any{"name": d.Device, "vendor": d.Vendor, "model": d.Model, "vrfs": d.VRFs}
		finding = fmt.Sprintf("%s for %s: %d VRF(s) with received advertisements and more-specific installed routes (snapshot %s)", d.Device, r.Prefix, len(d.VRFs), sid)
	case "installed":
		limit := r.Limit
		if limit <= 0 {
			limit = 200
		}
		ip, _, err := rc.InstalledPrefixes(ctx, in.NetworkID, sid, forward.RoutingInstalledOptions{Device: strings.TrimSpace(r.Device), Vendors: r.Vendors, Limit: limit})
		if err != nil {
			return fail(err)
		}
		detail["total_prefix_count"] = ip.TotalPrefixCount
		detail["nodes"] = ip.Nodes
		finding = fmt.Sprintf("%d BGP prefix(es) installed, %d node(s) listed (snapshot %s)", ip.TotalPrefixCount, len(ip.Nodes), sid)
		limits = append(limits, fmt.Sprintf("the list is capped at %d prefixes (routing.limit)", limit))
	}
	return result.Build(inspectBGPNeighborsName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits,
		Evidence: []result.Evidence{result.NewEvidence(result.EvState, "routingApp", &sid, detail, "")}, NextActions: []string{"verify-change", "investigate-reachability"}})
}

// routingTrace finds the propagation paths to the router, taking the origin and outcome from a search when they were not given. It returns the note that says so.
func routingTrace(ctx context.Context, s *fwd.Session, networkID, sid string, r routingInput) ([][]forward.RoutingNode, string, error) {
	rc := s.Client.Routing
	origin, outcome, note := r.OriginatedFrom, r.Outcome, ""
	if origin == nil || strings.TrimSpace(outcome) == "" {
		res, _, err := rc.PrefixSearch(ctx, networkID, sid, forward.RoutingPrefixSearch{Prefix: r.Prefix, Router: ptrDV(r.Router)})
		if err != nil {
			return nil, "", err
		}
		if origin == nil && len(res.Origin) > 0 {
			origin = &routingNode{Device: res.Origin[0].Device, VRF: res.Origin[0].VRF}
		}
		if strings.TrimSpace(outcome) == "" {
			for k, devs := range res.DevicesByOutcome {
				for _, d := range devs {
					if d.Device == r.Router.Device {
						outcome = k
					}
				}
			}
		}
		note = fmt.Sprintf("the origin and outcome were taken from the search (origin %s, outcome %s); give routing.originated_from and routing.outcome to trace another", nodeStr(origin), outcome)
	}
	paths, _, err := rc.PrefixTrace(ctx, networkID, sid, forward.RoutingTraceRequest{Prefix: r.Prefix, Router: r.Router.dv(), OriginatedFrom: origin.dv(), Outcome: outcome})
	return paths, note, err
}

func ptrDV(n *routingNode) *forward.DeviceVRF {
	if n == nil {
		return nil
	}
	d := n.dv()
	return &d
}

func devicesOut(d []forward.RoutingDevice) []map[string]any {
	out := make([]map[string]any, 0, len(d))
	for _, x := range d {
		out = append(out, map[string]any{"device": x.Device, "vrf": x.VRF, "protocol": x.Protocol})
	}
	return out
}

func nodeList(d []forward.RoutingDevice) string {
	if len(d) == 0 {
		return "no device"
	}
	names := make([]string, 0, len(d))
	shown, total := result.CapRow(d, 5)
	for _, x := range shown {
		names = append(names, x.Device)
	}
	if total > len(shown) {
		names = append(names, fmt.Sprintf("and %d more", total-len(shown)))
	}
	return strings.Join(names, ", ")
}

func nodeStr(n *routingNode) string {
	if n == nil {
		return "none"
	}
	return n.Device
}
