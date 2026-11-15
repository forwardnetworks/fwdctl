package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"sort"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// The search form of investigate-reachability: Forward's Search-page path query (Trace.FacetsQuery), used when through or leaving is given.
// It answers which distinct device paths match a query that pins hops (through), where traffic leaves and how it looks there (leaving),
// grouped by outcome, with the total; the per-path behaviour (headers, functions, config lines) is not read. PREVIEW: the trace-facets
// route is not in Forward's published API.

const maxSearchThrough = 8
const maxSearchPaths = 32

// searchTraceLimit is said when the search form traces its paths: the same preview routes as explain trace, with the search's own query.
const searchTraceLimit = "explain trace on the search form is PREVIEW and UNDOCUMENTED: it reads Forward's GUI flow-trace route with the SAME query as the search (through hops and leaving side included), for each path listed, so each trace is of that path as searched. Each path's `trace` is Forward's response body passed through unchanged (deviceHops with ports and headers, networkFunctions by layer with fileLines, 0-based, and actionBuckets, behavior.processingStages); IPv4 and IPv6 as the query gives them"

const searchLimit = "the search form is PREVIEW: it reads Forward's GUI Search-page query (trace-facets), which is not in Forward's published API and can change between Forward builds. It lists the distinct DEVICE paths that match, grouped by outcome, and the total number of flows; it does not read each path's headers, functions or config lines unless explain is trace (then each path, up to 16, carries Forward's trace for the same query). Forward groups flows by device path, so parallel links or different behaviours on one device path are one entry"

// searchLeaving is the leaving input: where the traffic leaves the network (at) and how the packet looks as it leaves (after any NAT).
type searchLeaving struct {
	At       string          `json:"at"`
	SrcIP    string          `json:"src_ip"`
	DstIP    string          `json:"dst_ip"`
	SrcPort  string          `json:"src_port"`
	DstPort  string          `json:"dst_port"`
	Protocol json.RawMessage `json:"protocol"`
}

// parseSearchLocation reads a location string: "DEVICE", "DEVICE INTERFACE" (Forward's own form, a space between), "zone:DEVICE ZONE",
// "vrf:DEVICE VRF", "vrf:VRF", "host:NAME" (not allowed as a through hop), or an address or prefix ("subnet:CIDR" or the bare CIDR or IP).
func parseSearchLocation(s string, hop bool) (forward.TraceLocation, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return forward.TraceLocation{}, errors.New("an empty location")
	}
	if hop && isAddressLocation(s) {
		return forward.TraceLocation{}, fmt.Errorf("%q: an address or prefix is not allowed as a through hop; name a device, \"DEVICE INTERFACE\", a zone or a VRF", s)
	}
	lower := strings.ToLower(s)
	switch {
	case strings.HasPrefix(lower, "host:"):
		if hop {
			return forward.TraceLocation{}, fmt.Errorf("%q: a host is not allowed as a through hop", s)
		}
		return forward.TraceHost(strings.TrimSpace(s[5:])), nil
	case strings.HasPrefix(lower, "zone:"):
		f := strings.Fields(s[5:])
		if len(f) != 2 {
			return forward.TraceLocation{}, fmt.Errorf("%q: a zone is \"zone:DEVICE ZONE\"", s)
		}
		return forward.TraceSecurityZone(f[0], f[1]), nil
	case strings.HasPrefix(lower, "vrf:"):
		f := strings.Fields(s[4:])
		switch len(f) {
		case 1:
			return forward.TraceVRF(f[0]), nil
		case 2:
			return forward.TraceDeviceVRF(f[0], f[1]), nil
		}
		return forward.TraceLocation{}, fmt.Errorf("%q: a VRF is \"vrf:VRF\" or \"vrf:DEVICE VRF\"", s)
	case strings.HasPrefix(lower, "subnet:"):
		return searchSubnet(strings.TrimSpace(s[7:]))
	}
	if _, err := netip.ParsePrefix(s); err == nil {
		return searchSubnet(s)
	}
	if _, err := netip.ParseAddr(s); err == nil {
		return searchSubnet(s)
	}
	f := strings.Fields(s)
	switch len(f) {
	case 1:
		return forward.TraceDevice(f[0]), nil
	case 2:
		return forward.TraceInterface(f[0], f[1]), nil
	}
	return forward.TraceLocation{}, fmt.Errorf("%q is not a location: give DEVICE, \"DEVICE INTERFACE\", zone:DEVICE ZONE, vrf:[DEVICE ]VRF, host:NAME or an address or prefix", s)
}

// isAddressLocation is true for an address or prefix location (bare, or subnet:).
func isAddressLocation(s string) bool {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(strings.ToLower(s), "subnet:") {
		return true
	}
	_, perr := netip.ParsePrefix(s)
	_, aerr := netip.ParseAddr(s)
	return perr == nil || aerr == nil
}

func searchSubnet(s string) (forward.TraceLocation, error) {
	if a, err := netip.ParseAddr(s); err == nil {
		return forward.TraceSubnet(netip.PrefixFrom(a, a.BitLen()).String()), nil
	}
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return forward.TraceLocation{}, fmt.Errorf("%q is not an address or prefix", s)
	}
	return forward.TraceSubnet(p.Masked().String()), nil
}

// searchHeaders builds a header filter from addresses, ports and protocol (nil when none is given).
func searchHeaders(srcIP, dstIP, srcPort, dstPort string, proto json.RawMessage) (forward.TraceHeaders, error) {
	h := forward.TraceHeaders{}
	for _, f := range []struct{ v, v4, v6 string }{{srcIP, "ipv4_src", "ipv6_src"}, {dstIP, "ipv4_dst", "ipv6_dst"}} {
		v := strings.TrimSpace(f.v)
		if v == "" {
			continue
		}
		addr := v
		if p, err := netip.ParsePrefix(v); err == nil {
			addr = p.Addr().String()
		}
		a, err := netip.ParseAddr(addr)
		if err != nil {
			return nil, fmt.Errorf("%q is not an address or prefix", v)
		}
		if a.Is4In6() {
			if p, err := netip.ParsePrefix(v); err == nil && p.Bits() >= 96 {
				v = netip.PrefixFrom(a.Unmap(), p.Bits()-96).String()
			} else if err == nil {
				return nil, fmt.Errorf("%q is an IPv4-mapped prefix shorter than /96", v)
			} else {
				v = a.Unmap().String()
			}
			a = a.Unmap()
		}
		if a.Is4() {
			h[f.v4] = []string{v}
		} else {
			h[f.v6] = []string{v}
		}
	}
	if v := strings.TrimSpace(srcPort); v != "" {
		h["tp_src"] = []string{v}
	}
	if v := strings.TrimSpace(dstPort); v != "" {
		h["tp_dst"] = []string{v}
	}
	if p := protocolText(proto); p != "" {
		n, err := fwd.ParseProtocol(p)
		if err != nil {
			return nil, err
		}
		if n == nil {
			return nil, fmt.Errorf("protocol %q is not one Forward filters on", p)
		}
		h["ip_proto"] = []string{fmt.Sprint(*n)}
	}
	if len(h) == 0 {
		return nil, nil
	}
	return h, nil
}

// buildSearchQuery turns the inputs into the path query, validating everything before any request.
func buildSearchQuery(in reachabilityInput, leaving *searchLeaving) (forward.TracePathQuery, error) {
	var q forward.TracePathQuery
	for name, v := range map[string]string{"dst_ip": in.DstIP, "src_ip": in.SrcIP} {
		if looksLikeHostname(v) {
			return q, fmt.Errorf("%w: %s %q is a host name; Forward does not resolve DNS and takes an IP address or prefix", ErrInvalidInput, name, v)
		}
	}
	fromHeaders, err := searchHeaders("", in.DstIP, in.SrcPort, in.DstPort, in.Protocol)
	if err != nil {
		return q, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	switch {
	case strings.TrimSpace(in.From) != "":
		loc, err := parseSearchLocation(in.From, false)
		if err != nil {
			return q, fmt.Errorf("%w: from: %v", ErrInvalidInput, err)
		}
		if strings.TrimSpace(in.SrcIP) != "" { // the source address rides as a header on the place it enters
			sh, err := searchHeaders(in.SrcIP, "", "", "", nil)
			if err != nil {
				return q, fmt.Errorf("%w: src_ip: %v", ErrInvalidInput, err)
			}
			if fromHeaders == nil {
				fromHeaders = forward.TraceHeaders{}
			}
			for k, v := range sh {
				fromHeaders[k] = v
			}
		}
		q.From = &forward.TraceEndpoint{Location: loc}
	case strings.TrimSpace(in.SrcIP) != "":
		loc, err := searchSubnet(strings.TrimSpace(in.SrcIP))
		if err != nil {
			return q, fmt.Errorf("%w: src_ip: %v", ErrInvalidInput, err)
		}
		q.From = &forward.TraceEndpoint{Location: loc}
	}
	if fromHeaders != nil {
		if q.From == nil {
			return q, fmt.Errorf("%w: dst_ip, ports and protocol filter the traffic where it enters: give from or src_ip with them in the search form", ErrInvalidInput)
		}
		q.From.Headers = []forward.TraceHeaders{fromHeaders}
	}
	if len(in.Through) > maxSearchThrough {
		return q, fmt.Errorf("%w: through takes at most %d hops", ErrInvalidInput, maxSearchThrough)
	}
	for i, h := range in.Through {
		transit, rest := forward.TraceTransitThrough, strings.TrimSpace(h)
		for _, p := range []string{forward.TraceTransitIngress, forward.TraceTransitEgress} {
			if strings.HasPrefix(strings.ToLower(rest), p+":") {
				transit, rest = p, strings.TrimSpace(rest[len(p)+1:])
			}
		}
		loc, err := parseSearchLocation(rest, true)
		if err != nil {
			return q, fmt.Errorf("%w: through[%d]: %v", ErrInvalidInput, i, err)
		}
		q.Chain = append(q.Chain, forward.TraceChainHop{Transit: transit, Location: loc})
	}
	if leaving != nil {
		if strings.TrimSpace(leaving.At) == "" {
			return q, fmt.Errorf("%w: leaving needs at, where the traffic leaves the network (a device, \"DEVICE INTERFACE\", zone:DEVICE ZONE or vrf:DEVICE VRF); a subnet there matched nothing in a live test", ErrInvalidInput)
		}
		if isAddressLocation(leaving.At) {
			return q, fmt.Errorf("%w: leaving.at %q is an address or prefix; Forward matched nothing for a subnet on the leaving side in a live test, so name where the traffic leaves (a device, \"DEVICE INTERFACE\", zone:DEVICE ZONE or vrf:DEVICE VRF) and put the address in leaving.dst_ip", ErrInvalidInput, leaving.At)
		}
		loc, err := parseSearchLocation(leaving.At, false)
		if err != nil {
			return q, fmt.Errorf("%w: leaving.at: %v", ErrInvalidInput, err)
		}
		lh, err := searchHeaders(leaving.SrcIP, leaving.DstIP, leaving.SrcPort, leaving.DstPort, leaving.Protocol)
		if err != nil {
			return q, fmt.Errorf("%w: leaving: %v", ErrInvalidInput, err)
		}
		q.To = &forward.TraceEndpoint{Location: loc}
		if lh != nil {
			q.To.Headers = []forward.TraceHeaders{lh}
		}
	}
	if q.From == nil && q.To == nil && len(q.Chain) == 0 {
		return q, fmt.Errorf("%w: the search form needs from or src_ip, through, or leaving", ErrInvalidInput)
	}
	return q, nil
}

// reachabilitySearch runs the search form. The query is checked before any request.
func reachabilitySearch(ctx context.Context, s *fwd.Session, in reachabilityInput, q forward.TracePathQuery, snapshotID string, cx result.Context, sid *string) (result.Result, error) {
	max := maxSearchPaths
	if in.MaxResults > 0 && in.MaxResults < max {
		max = in.MaxResults
	}
	facets, _, err := s.Client.Trace.FacetsQuery(ctx, in.NetworkID, snapshotID, q, forward.TraceFacetsOptions{})
	if err != nil {
		if errors.Is(err, forward.ErrEndpointNotServed) {
			return result.NewUnknown(reachabilityName, "This Forward does not serve the Search-page query (trace-facets); nothing was searched", cx, []string{searchLimit}, result.Options{})
		}
		return result.Result{}, err
	}
	query := map[string]any{"from": nilIfEmpty(in.From), "src_ip": nilIfEmpty(in.SrcIP), "dst_ip": nilIfEmpty(in.DstIP), "through": in.Through}
	if in.Leaving != nil {
		query["leaving"] = in.Leaving
	}
	limits := []string{searchLimit}
	pl, ambiguous, perr := facets.PossibleLocations()
	if perr != nil {
		return result.NewUnknown(reachabilityName, "Forward's answer could not be read ("+perr.Error()+"); nothing is concluded", cx, limits, result.Options{})
	}
	if ambiguous {
		detail := map[string]any{"query": query, "possible_locations": pl}
		return result.NewUnknown(reachabilityName, "An address in the query is at more than one place, so Forward answered with the places instead of paths: pick one (give from as the device, or leaving.at) and ask again", cx,
			limits, result.Options{Evidence: []result.Evidence{result.NewEvidence(result.EvPath, "traceFacets", sid, detail, "the address is ambiguous")}})
	}
	if facets.TimedOut {
		limits = append(limits, "Forward's search timed out, so the paths below are only those found before it stopped")
	}
	total, haveTotal, terr := facets.Total()
	if terr != nil {
		limits = append(limits, "Forward's total could not be read ("+terr.Error()+")")
	}
	complete := haveTotal && total.Type == "EXACT" && !facets.TimedOut
	if !complete {
		limits = append(limits, "Forward did not report an exact total (it stopped early, capped the flows it examined, or left the total out), so the paths listed may not be all the paths that match")
	}
	type pathRow struct {
		outcome string
		path    []string
	}
	var all []pathRow
	byOutcome := map[string]int{}
	for _, g := range facets.PathGroups {
		for _, p := range g.Paths {
			all = append(all, pathRow{g.TraceType, p.Path})
			byOutcome[g.TraceType]++
		}
	}
	shown, omitted := result.Cap(all, max, "distinct device paths", "narrow the query (through, leaving); max_results lists at most 32")
	rows := make([]map[string]any, 0, len(shown))
	for _, p := range shown {
		rows = append(rows, map[string]any{"outcome": p.outcome, "devices": p.path})
	}
	outcomes := make([]string, 0, len(byOutcome))
	for o := range byOutcome {
		outcomes = append(outcomes, o)
	}
	sort.Strings(outcomes)
	var parts []string
	for _, o := range outcomes {
		parts = append(parts, fmt.Sprintf("%d %s", byOutcome[o], o))
	}
	detail := map[string]any{"query": query, "paths": rows, "distinct_paths": len(all), "paths_by_outcome": byOutcome}
	if haveTotal {
		detail["total_flows"] = total.Value
		detail["total_type"] = total.Type
	}
	var finding string
	status := result.OK
	switch {
	case len(all) == 0 && !complete:
		status = result.Unknown
		finding = "No matching path was found, but Forward did not report a complete search (a timeout, a capped count or no total); that is not proof there is none"
	case len(all) == 0:
		finding = "No path matches this query in the snapshot (from, through and leaving as given)"
		limits = append(limits, "no match means no modelled flow takes this route in this snapshot; a hop or location named in another form (an interface name, a VRF) matches nothing, so check the names with inspect-inventory before reading it as unreachable")
	default:
		finding = fmt.Sprintf("%d distinct device path(s) match: %s", len(all), strings.Join(parts, ", "))
		if haveTotal {
			bound := ""
			if !complete {
				bound = "at least "
			}
			finding += fmt.Sprintf(" (%s%d flows)", bound, total.Value)
		}
	}
	evidence := []result.Evidence{result.NewEvidence(result.EvPath, "traceFacets", sid, detail, finding)}
	if in.Explain == "trace" && len(all) > 0 {
		ev, tl := searchTraces(ctx, s, in.NetworkID, snapshotID, q, facets, sid, min(max, maxTracePaths))
		limits = append(limits, tl...)
		if ev != nil {
			evidence = append(evidence, *ev)
		}
	}
	opts := result.Options{Limits: limits, Omitted: omitted, NextActions: []string{"investigate-reachability"}, Evidence: evidence}
	if status == result.Unknown {
		return result.NewUnknown(reachabilityName, finding, cx, limits, opts)
	}
	return result.Build(reachabilityName, status, finding, result.Deterministic, cx, opts)
}

// searchTraces traces each path the search found (up to maxTracePaths) with the same query (Trace.TraceFor), so a path with through hops or a
// leaving side is traced as searched. A trace that cannot be read is a row error or a limit, never a change to the search answer.
func searchTraces(ctx context.Context, s *fwd.Session, networkID, snapshotID string, q forward.TracePathQuery, facets *forward.TraceFacets, sid *string, bound int) (*result.Evidence, []string) {
	limits := []string{searchTraceLimit}
	var paths []map[string]any
	total, failed := 0, 0
	for gi, g := range facets.PathGroups {
		for pi := range g.Paths {
			total++
			if len(paths) >= bound {
				continue
			}
			sel, _ := g.Selection(pi)
			row := map[string]any{"id": fmt.Sprintf("g%d.p%d", gi, pi), "group": gi, "index": pi, "trace_type": g.TraceType, "path": sel.Path}
			tr, _, err := s.Client.Trace.TraceFor(ctx, networkID, snapshotID, q, sel)
			switch {
			case err == nil:
				row["descriptor"] = nilIfEmpty(tr.PathDescriptor)
				row["trace"] = json.RawMessage(tr.Raw)
			case ctx.Err() != nil:
				return nil, append(limits, "explain trace: stopped: "+ctx.Err().Error())
			case forward.IsStatus(err, http.StatusUnauthorized) || forward.IsStatus(err, http.StatusForbidden) || forward.IsStatus(err, http.StatusNotFound) ||
				forward.IsStatus(err, http.StatusTooManyRequests) || errors.Is(err, forward.ErrEndpointNotServed):
				return nil, append(limits, "explain trace: the trace was refused ("+err.Error()+"); no trace was read")
			default:
				row["error"] = err.Error()
				failed++
			}
			paths = append(paths, row)
		}
	}
	if total > len(paths) {
		limits = append(limits, fmt.Sprintf("explain trace: traces were asked for the first %d of %d paths found (the same paths listed above)", len(paths), total))
	}
	if failed > 0 {
		limits = append(limits, fmt.Sprintf("explain trace: %d path trace(s) could not be read (each row says why)", failed))
	}
	ev := result.NewEvidence(result.EvPath, "trace", sid, map[string]any{"preview": true, "paths": paths, "traced": len(paths) - failed, "distinct_paths": total, "truncated": total > len(paths),
		"timed_out": facets.TimedOut, "file_lines_base": 0}, fmt.Sprintf("%d path(s) traced", len(paths)-failed))
	return &ev, limits
}
