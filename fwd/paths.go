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
}

// Hop is one hop of a path, in a stable shape independent of the SDK's types.
type Hop struct {
	Device           string `json:"device"`
	DisplayName      string `json:"display_name"`
	IngressInterface string `json:"ingress_interface"`
	EgressInterface  string `json:"egress_interface"`
}

// Path is one candidate path.
type Path struct {
	ForwardingOutcome string `json:"forwarding_outcome"`
	SecurityOutcome   string `json:"security_outcome"`
	Hops              []Hop  `json:"hops"`
}

// PathResult is a path search answer.
type PathResult struct {
	TimedOut  bool   `json:"timed_out"`
	TotalType string `json:"total_type"` // EXACT | LOWER_BOUND
	Paths     []Path `json:"paths"`
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
	resp, _, err := s.Client.Networks.Paths(ctx, networkID, forward.PathSearchRequest{
		From: q.From, SrcIP: q.SrcIP, DstIP: q.DstIP, Intent: q.Intent, SnapshotID: q.SnapshotID,
		IPProto: proto, SrcPort: q.SrcPort, DstPort: q.DstPort,
		MaxResults: &maxResults, MaxSeconds: &maxSeconds,
	})
	if err != nil {
		return PathResult{}, err
	}
	out := PathResult{TimedOut: resp.TimedOut, TotalType: resp.Info.TotalHits.Type}
	for _, p := range resp.Info.Paths {
		path := Path{ForwardingOutcome: p.ForwardingOutcome, SecurityOutcome: p.SecurityOutcome}
		for _, h := range p.Hops {
			path.Hops = append(path.Hops, Hop{Device: h.DeviceName, DisplayName: h.DisplayName,
				IngressInterface: h.IngressInterface, EgressInterface: h.EgressInterface})
		}
		out.Paths = append(out.Paths, path)
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
