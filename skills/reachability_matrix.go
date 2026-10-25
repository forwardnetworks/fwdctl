package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// matrixInput is investigate-reachability's matrix option: every ordered pair of endpoints searched in one bulk request, each flow paired with its reverse so asymmetric routing shows.
type matrixInput struct {
	Endpoints []struct {
		Label string `json:"label"`
		IP    string `json:"ip"`
	} `json:"endpoints"`
	// TransitGroups name sets of devices by a pattern on the device name; a path is labelled with the groups it crosses, in order.
	TransitGroups []struct {
		Label   string `json:"label"`
		Pattern string `json:"pattern"`
	} `json:"transit_groups"`
	// MaxOverallSeconds bounds the whole bulk request (default 300; Forward runs more than 10 queries as a large job with a one hour limit).
	MaxOverallSeconds int `json:"max_overall_seconds"`
}

const maxMatrixEndpoints = 40

type matrixGroup struct {
	label string
	re    *regexp.Regexp
}

func parseMatrix(raw json.RawMessage, in reachabilityInput) (*matrixInput, []matrixGroup, error) {
	var probe struct {
		Matrix *matrixInput `json:"matrix"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	m := probe.Matrix
	if m == nil {
		return nil, nil, nil
	}
	if in.SrcIP != "" || in.DstIP != "" || in.From != "" || in.ReturnPath {
		return nil, nil, fmt.Errorf("%w: matrix searches every endpoint pair both ways; it does not combine with src_ip, dst_ip, from or return_path", ErrInvalidInput)
	}
	if len(m.Endpoints) < 2 || len(m.Endpoints) > maxMatrixEndpoints {
		return nil, nil, fmt.Errorf("%w: matrix.endpoints takes 2 to %d endpoints", ErrInvalidInput, maxMatrixEndpoints)
	}
	seen := map[string]bool{}
	for i := range m.Endpoints {
		e := &m.Endpoints[i]
		e.Label, e.IP = strings.TrimSpace(e.Label), strings.TrimSpace(e.IP)
		if _, err := netip.ParseAddr(e.IP); err != nil {
			return nil, nil, fmt.Errorf("%w: matrix endpoint %d: %q is not an IP address", ErrInvalidInput, i, e.IP)
		}
		if e.Label == "" {
			e.Label = e.IP
		}
		if seen[e.Label] {
			return nil, nil, fmt.Errorf("%w: matrix endpoint label %q appears twice", ErrInvalidInput, e.Label)
		}
		seen[e.Label] = true
	}
	var groups []matrixGroup
	for _, g := range m.TransitGroups {
		re, err := regexp.Compile(g.Pattern)
		if err != nil || strings.TrimSpace(g.Label) == "" {
			return nil, nil, fmt.Errorf("%w: matrix.transit_groups needs a label and a valid pattern (%q)", ErrInvalidInput, g.Pattern)
		}
		groups = append(groups, matrixGroup{g.Label, re})
	}
	if m.MaxOverallSeconds <= 0 {
		m.MaxOverallSeconds = 300
	}
	m.MaxOverallSeconds = min(m.MaxOverallSeconds, 3000) // under Forward's one hour limit for a large job
	return m, groups, nil
}

// pathGroups labels a path with the transit groups its hops cross, in order, a repeat run of one group counted once. Without groups it is the device sequence.
func pathGroups(p fwd.Path, groups []matrixGroup) []string {
	var out []string
	add := func(s string) {
		if len(out) == 0 || out[len(out)-1] != s {
			out = append(out, s)
		}
	}
	for _, h := range p.Hops {
		if len(groups) == 0 {
			add(h.Device)
			continue
		}
		for _, g := range groups {
			if g.re.MatchString(h.Device) {
				add(g.label)
				break
			}
		}
	}
	return out
}

func reverseStrings(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[len(in)-1-i] = s
	}
	return out
}

func sameSet(a, b []string) bool {
	x, y := map[string]bool{}, map[string]bool{}
	for _, s := range a {
		x[s] = true
	}
	for _, s := range b {
		y[s] = true
	}
	if len(x) != len(y) {
		return false
	}
	for s := range x {
		if !y[s] {
			return false
		}
	}
	return true
}

func joinGroups(g []string) string { return strings.Join(g, " > ") }

// reachabilityMatrix answers matrix: one bulk request with a query for every ordered pair.
func reachabilityMatrix(ctx context.Context, s *fwd.Session, in reachabilityInput, m *matrixInput, groups []matrixGroup, snapID string, cx result.Context, sid *string) (result.Result, error) {
	type pair struct{ from, to int }
	var order []pair
	var queries []fwd.PathQuery
	for i := range m.Endpoints {
		for j := range m.Endpoints {
			if i == j {
				continue
			}
			order = append(order, pair{i, j})
			queries = append(queries, fwd.PathQuery{SrcIP: m.Endpoints[i].IP, DstIP: m.Endpoints[j].IP, Protocol: protocolText(in.Protocol), SrcPort: in.SrcPort, DstPort: in.DstPort})
		}
	}
	cells, err := s.SearchPathsBulk(ctx, in.NetworkID, queries, fwd.BulkOptions{SnapshotID: snapID, Intent: in.Intent, MaxResults: 3, MaxSeconds: in.MaxSeconds, MaxOverallSeconds: m.MaxOverallSeconds})
	if err != nil {
		return result.Result{}, err
	}
	if len(cells) != len(queries) {
		return result.Result{}, fmt.Errorf("Forward answered %d results for %d queries", len(cells), len(queries))
	}
	type cellOut struct {
		verdict string
		groups  []string
		devices []string
		row     map[string]any
	}
	byPair := map[pair]*cellOut{}
	var rows []map[string]any
	counts := map[string]int{}
	var timedOut, failed int
	for k, c := range cells {
		p := order[k]
		from, to := m.Endpoints[p.from].Label, m.Endpoints[p.to].Label
		row := map[string]any{"from": from, "to": to}
		co := &cellOut{}
		switch {
		case c.Failed:
			co.verdict = "undetermined"
			row["error"] = c.ErrorMessage
			failed++
		case c.TimedOut && len(c.Paths) == 0:
			co.verdict = "undetermined"
			row["timed_out"] = true
			timedOut++
		default:
			co.verdict = fwd.Verdict(c.Paths, c.TimedOut)
			if len(c.Paths) > 0 {
				top := c.Paths[0]
				for _, p := range c.Paths {
					if fwd.Classify(p) == "delivered" {
						top = p
						break
					}
				}
				co.groups = pathGroups(top, groups)
				for _, h := range top.Hops {
					co.devices = append(co.devices, h.Device)
				}
				row["outcome"] = fwd.Classify(top)
				if fwd.Classify(top) == "incomplete_model" && top.ForwardingOutcome == "DELIVERED_TO_INCORRECT_LOCATION" {
					row["leaves_the_model"] = leavesModelNote
				}
				row["groups"] = co.groups
				row["hops"] = len(top.Hops)
			}
		}
		row["verdict"] = co.verdict
		co.row = row
		counts[co.verdict]++
		byPair[p] = co
		rows = append(rows, row)
	}
	// asymmetric: both directions delivered, and the groups crossed (or, without groups, the devices) differ once the reverse is turned around
	var asym []map[string]any
	for i := range m.Endpoints {
		for j := i + 1; j < len(m.Endpoints); j++ {
			f, r := byPair[pair{i, j}], byPair[pair{j, i}]
			if f == nil || r == nil || f.verdict != "delivered" || r.verdict != "delivered" {
				continue
			}
			different := false
			if len(groups) > 0 {
				different = joinGroups(f.groups) != joinGroups(reverseStrings(r.groups))
			} else {
				different = !sameSet(f.devices, r.devices)
			}
			if different {
				asym = append(asym, map[string]any{"a": m.Endpoints[i].Label, "b": m.Endpoints[j].Label, "a_to_b": f.groups, "b_to_a": r.groups, "b_to_a_reversed": reverseStrings(r.groups)})
			}
		}
	}
	sort.SliceStable(asym, func(i, j int) bool {
		return fmt.Sprint(asym[i]["a"], asym[i]["b"]) < fmt.Sprint(asym[j]["a"], asym[j]["b"])
	})
	limits := []string{
		fmt.Sprintf("one bulk request of %d queries (every ordered pair of %d endpoints), at most 3 paths per query, %d seconds in all; a path is the best one Forward returned for the flow, and a router loopback only proves the control plane resolves, so use host addresses where you have them", len(queries), len(m.Endpoints), m.MaxOverallSeconds),
	}
	if len(groups) == 0 {
		limits = append(limits, "no transit_groups were given, so a pair is called asymmetric when its two directions cross different sets of devices; name groups (a pattern on device names) to compare roles instead")
	}
	if timedOut+failed > 0 {
		limits = append(limits, fmt.Sprintf("%d flow(s) timed out and %d failed: they are undetermined, neither delivered nor blocked; a query that Forward had not started when the overall time ran out comes back as a timeout with no paths (raise matrix.max_overall_seconds)", timedOut, failed))
	}
	if cx.State == "predicted" {
		limits = append(limits, "read from a predicted snapshot, not collected state")
	}
	finding := fmt.Sprintf("%d flows between %d endpoints: %d delivered, %d blocked, %d undetermined; %d pair(s) asymmetric", len(cells), len(m.Endpoints), counts["delivered"], counts["blocked"], counts["undetermined"], len(asym))
	ep := make([]map[string]any, 0, len(m.Endpoints))
	for _, e := range m.Endpoints {
		ep = append(ep, map[string]any{"label": e.Label, "ip": e.IP})
	}
	detail := map[string]any{"endpoints": ep, "cells": rows, "asymmetric_pairs": asym, "counts": counts, "snapshot_id": snapID}
	return result.Build(reachabilityName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits,
		Evidence:    []result.Evidence{result.NewEvidence(result.EvPath, "pathsBulk", sid, detail, finding)},
		NextActions: []string{"investigate-reachability"}})
}
