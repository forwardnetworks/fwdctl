package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// A view of inspect-edge (the retired skill of that name): it reports under inspect-edge.
const findTraceSourceName = inspectEdgeName

type findTraceSourceInput struct {
	NetworkID  string   `json:"network_id"`
	TargetIP   string   `json:"target_ip"`
	VRF        string   `json:"vrf"`
	Exclude    []string `json:"exclude"`
	Candidates int      `json:"candidates"`
	SnapshotID string   `json:"snapshot_id"`
}

var vrfName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

const (
	defaultTraceCandidates = 12
	maxTraceCandidates     = 30
)

// findTraceSource picks devices to start a trace from. A source that is blackholed at its first hop proves nothing about the destination, so each candidate
// (a device that has a routing instance, in the given VRF when one is named) is first traced to a control address that must be reachable (the edge's own
// interface address, for example); the ones whose control trace is delivered are good sources. It measures the control only: a delivered control says the source
// reaches the edge, not that it reaches the real destination.
func findTraceSource(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in findTraceSourceInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	in.TargetIP, in.VRF = strings.TrimSpace(in.TargetIP), strings.TrimSpace(in.VRF)
	if in.NetworkID == "" || in.TargetIP == "" {
		return result.Result{}, fmt.Errorf("%w: network_id and target_ip (the control address a good source must reach, such as the edge device's own interface address) are required", ErrInvalidInput)
	}
	if in.VRF != "" && !vrfName.MatchString(in.VRF) {
		return result.Result{}, fmt.Errorf("%w: vrf is a routing instance name (letters, digits, . _ -)", ErrInvalidInput)
	}
	n := in.Candidates
	if n <= 0 {
		n = defaultTraceCandidates
	}
	n = min(n, maxTraceCandidates)
	snap, err := resolveSnapshot(ctx, s, in.NetworkID, in.SnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	cx := fwd.Context(in.NetworkID, snap)
	if !fwd.IsReady(snap) {
		return result.NewUnknown(findTraceSourceName, "No processed snapshot is available to answer from", cx, []string{"no processed snapshot; nothing was measured"},
			result.Options{NextActions: []string{"investigate-collection-failure"}})
	}
	sid := fwd.SnapshotIDPtr(snap)
	filter := ""
	if in.VRF != "" {
		filter = fmt.Sprintf(" where instance.name == %q", in.VRF)
	}
	out, err := s.RunNQE(ctx, in.NetworkID, fwd.NQERun{SnapshotID: fwd.SnapshotID(cx), Limit: 2000, Query: "foreach device in network.devices\n" +
		"foreach instance in device.networkInstances\n" +
		"where isPresent(instance.afts) && isPresent(instance.afts.ipv4Unicast)" + strings.ReplaceAll(filter, " where ", " && ") + "\n" +
		"select distinct {device: device.name}"})
	if err != nil {
		return result.Result{}, err
	}
	skip := map[string]bool{}
	for _, e := range in.Exclude {
		skip[strings.TrimSpace(e)] = true
	}
	var names []string
	for _, row := range fwd.Records(out.Items) {
		if d, _ := row["device"].(string); d != "" && !skip[d] {
			names = append(names, d)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return result.NewUnknown(findTraceSourceName, "No device with an IPv4 routing table"+map[bool]string{true: " in VRF " + in.VRF, false: ""}[in.VRF != ""]+" was found to use as a source", cx,
			[]string{"no candidates (check the VRF name with inspect-inventory; an excluded device is not considered)"}, result.Options{NextActions: []string{"inspect-inventory"}})
	}
	limits := []string{"each candidate was traced only to the control address " + in.TargetIP + ": a delivered control means the source reaches that address, not that it reaches the real destination"}
	truncated := false
	if len(names) > n {
		names, truncated = names[:n], true
		limits = append(limits, fmt.Sprintf("only the first %d candidates (by name) were traced; raise candidates (at most %d) or name a VRF to narrow", n, maxTraceCandidates))
	}
	type row struct {
		Device    string `json:"device"`
		Control   string `json:"control"`
		Hops      int    `json:"hops"`
		LastHop   string `json:"last_hop,omitempty"`
		Delivered bool   `json:"delivered"`
	}
	var rows []row
	for _, d := range names {
		doc, err := s.SearchPaths(ctx, in.NetworkID, fwd.PathQuery{DstIP: in.TargetIP, From: d, Intent: "PREFER_DELIVERED", SnapshotID: fwd.SnapshotID(cx), MaxResults: 5, MaxSeconds: 10})
		if err != nil {
			rows = append(rows, row{Device: d, Control: "error: " + err.Error()})
			continue
		}
		if len(doc.Paths) == 0 {
			rows = append(rows, row{Device: d, Control: "no path returned (the device may not be able to originate it)"})
			continue
		}
		best := doc.Paths[0]
		class := fwd.Classify(best)
		for _, p := range doc.Paths {
			if c := fwd.Classify(p); c == "delivered" {
				best, class = p, c
				break
			}
		}
		r := row{Device: d, Control: class, Hops: len(best.Hops), Delivered: class == "delivered"}
		if len(best.Hops) > 0 {
			r.LastHop = hopName(best.Hops[len(best.Hops)-1])
		}
		rows = append(rows, r)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Delivered != rows[j].Delivered {
			return rows[i].Delivered
		}
		return rows[i].Hops > rows[j].Hops // a source several hops away exercises more of the path
	})
	good := 0
	for _, r := range rows {
		if r.Delivered {
			good++
		}
	}
	d := map[string]any{"snapshot_id": string(snap.ID), "target_ip": in.TargetIP, "vrf": nilIfEmpty(in.VRF), "candidates_traced": len(rows), "good": good, "sources": rows, "truncated": truncated}
	if good == 0 {
		return result.Build(findTraceSourceName, result.Failed, fmt.Sprintf("None of the %d candidate source(s) reaches %s, so none is a good place to trace from (snapshot %s)", len(rows), in.TargetIP, snap.ID), result.Deterministic, cx,
			result.Options{Limits: limits, NextActions: []string{"investigate-reachability"}, Evidence: []result.Evidence{result.NewEvidence(result.EvPath, "getPaths", sid, d, "no source reaches the control")}})
	}
	return result.Build(findTraceSourceName, result.OK, fmt.Sprintf("%d of %d candidate source(s) reach %s; start from %s (snapshot %s)", good, len(rows), in.TargetIP, rows[0].Device, snap.ID), result.Deterministic, cx,
		result.Options{Limits: limits, NextActions: []string{"investigate-reachability"}, Evidence: []result.Evidence{result.NewEvidence(result.EvPath, "getPaths", sid, d, fmt.Sprintf("%d good source(s)", good))}})
}
