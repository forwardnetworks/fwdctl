package fwd

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"
)

// Path search is bounded by default: at most DefaultMaxResults paths and DefaultMaxSeconds of server time.
const (
	DefaultMaxResults = 20
	DefaultMaxSeconds = 30
)

var protocols = map[string]int{"tcp": 6, "udp": 17, "icmp": 1}

// PathQuery is one flow to search.
type PathQuery struct {
	DstIP, SrcIP, From string
	Protocol           string // tcp | udp | icmp | a protocol number
	SrcPort, DstPort   string
	Intent             string
	SnapshotID         string
	MaxResults         int
	MaxSeconds         int
	ReturnPaths        int // > 0: also ask Forward for up to this many paths of the return flow (it sends none otherwise)
	// WithNetworkFunctions asks Forward for each hop's ACLs and security zones. It costs Forward more work per path, so only a caller that shows them (investigate-reachability)
	// sets it; the per-device and per-expectation searches of other skills do not.
	WithNetworkFunctions bool
}

// Hop is one hop of a path, in a stable shape independent of the SDK's types.
type Hop struct {
	Device           string   `json:"device"`
	DisplayName      string   `json:"display_name"`
	IngressInterface string   `json:"ingress_interface"`
	EgressInterface  string   `json:"egress_interface"`
	ACLs             []HopACL `json:"acls,omitempty"`
	IngressZone      string   `json:"ingress_zone,omitempty"`
	EgressZone       string   `json:"egress_zone,omitempty"`
}

// HopACL is an ACL (or policy) Forward evaluated on a hop: its name, where it applied (INGRESS or EGRESS) and what it did.
type HopACL struct {
	Name    string `json:"name"`
	Context string `json:"context,omitempty"`
	Action  string `json:"action,omitempty"`
}

// Path is one candidate path.
type Path struct {
	ForwardingOutcome string `json:"forwarding_outcome"`
	SecurityOutcome   string `json:"security_outcome"`
	Hops              []Hop  `json:"hops"`
}

// PathResult is a path search answer.
type PathResult struct {
	TimedOut bool `json:"timed_out"`
	// SrcLocationType and DstLocationType say how Forward located the source and destination address (INTERFACE, HOST, SNAT, CO_LOCATED, INTERFACE_ATTACHED_SUBNET, INTERNET, ROUTE, MULTICAST: most to least precise); ROUTE means located from injected routes.
	SrcLocationType string `json:"src_location_type,omitempty"`
	DstLocationType string `json:"dst_location_type,omitempty"`
	TotalType       string `json:"total_type"` // EXACT | LOWER_BOUND
	Paths           []Path `json:"paths"`
	// ReturnPaths are the paths of the return flow (destination back to source), only when PathQuery.ReturnPaths asked for them.
	ReturnPaths []Path `json:"return_paths,omitempty"`
}

// ParseProtocol maps tcp/udp/icmp or a number to the IP protocol number.
func ParseProtocol(p string) (*int, error) {
	p = strings.ToLower(strings.TrimSpace(p))
	if p == "" {
		return nil, nil
	}
	if n, ok := protocols[p]; ok {
		return &n, nil
	}
	n, err := strconv.Atoi(p)
	if err != nil || n < 0 || n > 255 {
		return nil, fmt.Errorf("protocol %q: use tcp, udp, icmp or a number 0-255", p)
	}
	return &n, nil
}

// SearchPaths runs a bounded path search.
func (s *Session) SearchPaths(ctx context.Context, networkID string, q PathQuery) (PathResult, error) {
	proto, err := ParseProtocol(q.Protocol)
	if err != nil {
		return PathResult{}, err
	}
	maxResults, maxSeconds := q.MaxResults, q.MaxSeconds
	if maxResults <= 0 {
		maxResults = DefaultMaxResults
	}
	if maxSeconds <= 0 {
		maxSeconds = DefaultMaxSeconds
	}
	var returnPaths *int
	if q.ReturnPaths > 0 {
		returnPaths = &q.ReturnPaths
	}
	var withFunctions *bool
	if q.WithNetworkFunctions {
		on := true
		withFunctions = &on
	}
	resp, _, err := s.Client.Networks.Paths(ctx, networkID, forward.PathSearchRequest{
		From: q.From, SrcIP: q.SrcIP, DstIP: q.DstIP, Intent: q.Intent, SnapshotID: q.SnapshotID,
		IPProto: proto, SrcPort: q.SrcPort, DstPort: q.DstPort,
		MaxResults: &maxResults, MaxSeconds: &maxSeconds, IncludeNetworkFunctions: withFunctions,
		MaxReturnPathResults: returnPaths,
	})
	if err != nil {
		return PathResult{}, err
	}
	out := PathResult{TimedOut: resp.TimedOut, TotalType: resp.Info.TotalHits.Type, Paths: convertPaths(resp.Info.Paths), SrcLocationType: resp.SrcIPLocationType, DstLocationType: resp.DstIPLocationType}
	if q.ReturnPaths > 0 {
		out.ReturnPaths = convertPaths(resp.ReturnPathInfo.Paths)
	}
	s.Annotate(intp(len(out.Paths)), out.TotalType == "LOWER_BOUND")
	return out, nil
}

// Classify names what a path means. Security is folded in for DELIVERED.
func Classify(p Path) string {
	switch p.ForwardingOutcome {
	case "DELIVERED":
		if p.SecurityOutcome == "DENIED" {
			return "security_denied"
		}
		return "delivered"
	case "DELIVERED_TO_INCORRECT_LOCATION":
		return "incomplete_model"
	case "BLACKHOLE":
		return "missing_route"
	case "DROPPED":
		return "dropped"
	case "INADMISSIBLE":
		return "not_admitted"
	case "LOOP":
		return "routing_loop"
	case "UNREACHABLE":
		return "unreachable"
	}
	return "unclassified"
}

// Verdict is delivered, blocked or undetermined for one flow. blocked needs a real failure and no
// delivered path from a search that completed; no paths, a timeout or only incomplete outcomes is
// undetermined, because the absence of a path is not evidence of a block.
func Verdict(paths []Path, timedOut bool) string {
	real := 0
	for _, p := range paths {
		switch k := Classify(p); k {
		case "delivered":
			return "delivered"
		case "incomplete_model", "unclassified":
		default:
			real++
		}
	}
	if real > 0 && !timedOut {
		return "blocked"
	}
	return "undetermined"
}

// convertPaths turns the SDK's path results into ours, with each hop's ACLs and security zones when Forward sent them.
func convertPaths(in []forward.NetworkPathResult) []Path {
	var out []Path
	for _, p := range in {
		path := Path{ForwardingOutcome: p.ForwardingOutcome, SecurityOutcome: p.SecurityOutcome}
		for _, h := range p.Hops {
			hop := Hop{Device: h.DeviceName, DisplayName: h.DisplayName,
				IngressInterface: h.IngressInterface, EgressInterface: h.EgressInterface}
			if nf := h.NetworkFunctions; nf != nil {
				for _, a := range nf.ACL {
					hop.ACLs = append(hop.ACLs, HopACL{Name: a.Name, Context: a.Context, Action: a.Action})
				}
				hop.IngressZone, hop.EgressZone = nf.Ingress.SecurityZone, nf.Egress.SecurityZone
			}
			path.Hops = append(path.Hops, hop)
		}
		out = append(out, path)
	}
	return out
}

// BulkCell is one query's answer from a bulk path search.
type BulkCell struct {
	PathResult
	// Failed says Forward answered an error for this query alone (for example a device not in the snapshot); ErrorMessage is its reason. A timed out query is not Failed: it has TimedOut and no paths.
	Failed       bool
	ErrorMessage string
}

// BulkOptions are the settings shared by every query of a bulk path search. Zero means Forward's default, except MaxOverallSeconds, which a large batch must set.
type BulkOptions struct {
	SnapshotID        string
	Intent            string
	MaxResults        int
	MaxSeconds        int // per query
	MaxOverallSeconds int // for the whole request; queries not started when it passes come back TimedOut with no paths
}

// SearchPathsBulk runs many path searches against one snapshot in one request and returns one cell per query, in order (POST /networks/{n}/paths-bulk). Hops carry device names only.
func (s *Session) SearchPathsBulk(ctx context.Context, networkID string, queries []PathQuery, o BulkOptions) ([]BulkCell, error) {
	qs := make([]forward.PathQuery, 0, len(queries))
	for _, q := range queries {
		proto, err := ParseProtocol(q.Protocol)
		if err != nil {
			return nil, err
		}
		qs = append(qs, forward.PathQuery{From: q.From, SrcIP: q.SrcIP, DstIP: q.DstIP, IPProto: proto, SrcPort: q.SrcPort, DstPort: q.DstPort})
	}
	opt := forward.PathBulkOptions{SnapshotID: o.SnapshotID, Intent: o.Intent}
	if o.MaxResults > 0 {
		opt.MaxResults = &o.MaxResults
	}
	if o.MaxSeconds > 0 {
		opt.MaxSeconds = &o.MaxSeconds
	}
	if o.MaxOverallSeconds > 0 {
		opt.MaxOverallSeconds = &o.MaxOverallSeconds
	}
	res, _, err := s.Client.Networks.PathsBulk(ctx, networkID, qs, opt)
	if err != nil {
		return nil, err
	}
	out := make([]BulkCell, 0, len(res))
	for _, r := range res {
		if r.Failed || r.Response == nil {
			out = append(out, BulkCell{Failed: true, ErrorMessage: r.ErrorMessage})
			continue
		}
		out = append(out, BulkCell{PathResult: PathResult{TimedOut: r.Response.TimedOut, TotalType: r.Response.Info.TotalHits.Type, Paths: convertPaths(r.Response.Info.Paths)}})
	}
	s.Annotate(intp(len(out)), false)
	return out, nil
}
