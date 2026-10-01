package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const blastRadiusName = verifyChangeName

func analyzeBlastRadius(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in changeInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	rc, err := resolveChange(ctx, s, in)
	if err == errNoSelector {
		return result.NewError(blastRadiusName, err.Error(), fwd.Context(in.NetworkID, nil)), nil
	}
	if err != nil {
		return result.Result{}, err
	}
	cx := fwd.Context(in.NetworkID, rc.After)
	limits := append([]string(nil), rc.Limits...)
	if rc.BeforeID == "" || !fwd.IsReady(rc.Before) || !fwd.IsReady(rc.After) {
		if len(limits) == 0 {
			limits = []string{"a snapshot is missing or not processed; nothing was compared"}
		}
		return result.NewUnknown(blastRadiusName, "There is nothing to compare, so the extent of the change is unknown", cx, limits, result.Options{})
	}
	afterID := fwd.SnapshotID(cx)
	sid := fwd.SnapshotIDPtr(rc.After)
	var evidence []result.Evidence
	var touched, incomplete []string
	measured := false

	counts, err := s.AreaCounts(ctx, rc.BeforeID, afterID)
	if err != nil {
		return result.Result{}, err
	}
	var complete []fwd.AreaCount
	for _, c := range counts {
		if c.Unavailable || !c.Complete {
			incomplete = append(incomplete, c.Area)
			continue
		}
		complete = append(complete, c)
	}
	if len(incomplete) > 0 {
		sort.Strings(incomplete)
		limits = append(limits, "difference counts not complete for: "+strings.Join(incomplete, ", "))
	}
	if len(counts) > 0 {
		areas := make([]string, 0, len(counts))
		for _, c := range counts {
			areas = append(areas, c.Area)
		}
		limits = append(limits, "only these difference areas were compared: "+strings.Join(areas, ", ")+"; any other area was not measured, so say nothing about it")
	}
	if len(complete) > 0 {
		measured = true
	}
	sort.SliceStable(complete, func(i, j int) bool { return complete[i].Count > complete[j].Count })
	areas := make([]string, 0, len(complete))
	for _, c := range complete {
		areas = append(areas, c.Area)
		if c.Count > 0 {
			touched = append(touched, fmt.Sprintf("%s (%d)", c.Area, c.Count))
			evidence = append(evidence, result.NewEvidence(result.EvPredict, "getDiffCount:"+c.Area, sid,
				map[string]any{"area": c.Area, "count": c.Count}, fmt.Sprintf("%s: %d differences", c.Area, c.Count)))
		}
	}

	conn, err := s.SubnetConnectivity(ctx, rc.BeforeID, afterID, time.Duration(orDefault(in.ConnectivitySecs, 300))*time.Second)
	if err != nil {
		return result.Result{}, err
	}
	var next []string
	switch {
	case conn.Unavailable != "":
		limits = append(limits, "the connectivity comparison is not available: "+conn.Unavailable+"; connectivity impact is unmeasured")
	case !conn.Settled:
		limits = append(limits, "the connectivity comparison did not settle in time; its counts are not evidence")
	case !conn.Compared:
		limits = append(limits, "no subnet pairs were compared (no locations defined?); connectivity impact is unmeasured")
	default:
		measured = true
		evidence = append(evidence, result.NewEvidence(result.EvPredict, "getSubnetConnectivityDiff", sid, map[string]any{
			"newly_isolated": conn.NewlyIsolated, "newly_connected": conn.NewlyConnected, "modified": conn.Modified, "total_pairs": conn.TotalPairs},
			fmt.Sprintf("%d pairs newly isolated, %d newly connected of %d", conn.NewlyIsolated, conn.NewlyConnected, conn.TotalPairs)))
		if conn.NewlyIsolated > 0 {
			next = append(next, "investigate-reachability")
		}
	}
	next = append(next, "verify-change")

	if !measured {
		return result.NewUnknown(blastRadiusName, "No comparison completed, so the extent of the change is unknown", cx, limits, result.Options{})
	}
	var parts []string
	if len(touched) > 0 {
		parts = append(parts, "differs in "+strings.Join(touched, ", "))
	}
	if conn.Settled && conn.Compared && (conn.NewlyIsolated > 0 || conn.NewlyConnected > 0) {
		parts = append(parts, fmt.Sprintf("%d subnet pair(s) lost connectivity, %d gained", conn.NewlyIsolated, conn.NewlyConnected))
	}
	finding := "No observable difference between the snapshots"
	if len(parts) > 0 {
		finding = strings.Join(parts, "; ")
	}
	if len(evidence) == 0 {
		evidence = append(evidence, result.NewEvidence(result.EvPredict, "getDiffCount", sid,
			map[string]any{"areas_compared": areas, "differences": 0}, "no differences in any complete area"))
	}
	return result.Build(blastRadiusName, result.OK, finding, result.Deterministic, cx,
		result.Options{Limits: limits, Evidence: evidence, NextActions: next})
}
