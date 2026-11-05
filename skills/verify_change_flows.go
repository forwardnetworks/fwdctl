package skills

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// maxFlowDiffFlows bounds the flows one flows view searches (two path searches each); maxFlowDiffPaths the paths listed per side.
const (
	maxFlowDiffFlows = 20
	maxFlowDiffPaths = 16
)

// flowDiff searches each flow on the before and the after snapshot and says, per flow, whether its outcome changed and which paths are the same,
// only before or only after (a path is its classification and the devices and interfaces it crosses). It judges nothing: verify does that.
func flowDiff(ctx context.Context, s *fwd.Session, in verifyChangeInput) (result.Result, error) {
	if len(in.Expectations) == 0 {
		return result.Result{}, fmt.Errorf("%w: view flows takes expectations: the flows to compare (dst_ip, and src_ip or from; protocol and ports optional; expect is ignored)", ErrInvalidInput)
	}
	if len(in.Expectations) > maxFlowDiffFlows {
		return result.Result{}, fmt.Errorf("%w: view flows compares at most %d flows at once", ErrInvalidInput, maxFlowDiffFlows)
	}
	rc, err := resolveChange(ctx, s, in.changeInput)
	if err == errNoSelector {
		return result.NewError(verifyChangeName, err.Error(), fwd.Context(in.NetworkID, nil)), nil
	}
	if err != nil {
		return result.Result{}, err
	}
	cx := fwd.Context(in.NetworkID, rc.After)
	limits := append([]string(nil), rc.Limits...)
	if rc.BeforeID == "" || !fwd.IsReady(rc.After) || !fwd.IsReady(rc.Before) {
		if len(limits) == 0 {
			limits = []string{"the before or after snapshot is missing or not processed; nothing was compared"}
		}
		return result.NewUnknown(verifyChangeName, "There are not two processed snapshots to compare the flows on", cx, limits, result.Options{})
	}
	afterID := fwd.SnapshotID(cx)
	beforePtr, afterPtr := &rc.BeforeID, fwd.SnapshotIDPtr(rc.After)
	side := func(e expectation, snap string, ptr *string) (map[string]any, map[string]map[string]any, string, error) {
		doc, err := s.SearchPaths(ctx, in.NetworkID, fwd.PathQuery{DstIP: e.DstIP, SrcIP: e.SrcIP, From: e.From, Protocol: protocolText(e.Protocol),
			SrcPort: e.SrcPort, DstPort: e.DstPort, Intent: "PREFER_DELIVERED", SnapshotID: snap, WithNetworkFunctions: true})
		if err != nil {
			return nil, nil, "", err
		}
		verdict := fwd.Verdict(doc.Paths, doc.TimedOut)
		bySig := map[string]map[string]any{}
		var order []string
		for _, p := range doc.Paths {
			c := fwd.Classify(p)
			sig := pathSignature(p, c)
			if _, ok := bySig[sig]; !ok {
				bySig[sig] = pathEvidence(p, c, ptr).Detail
				order = append(order, sig)
			}
		}
		var paths []map[string]any
		for i, sig := range order {
			if i == maxFlowDiffPaths {
				break
			}
			paths = append(paths, bySig[sig])
		}
		out := map[string]any{"snapshot_id": snap, "verdict": verdict, "paths": paths, "distinct_paths": len(order), "timed_out": doc.TimedOut}
		if doc.TotalType == "LOWER_BOUND" {
			out["more_paths_exist"] = true
		}
		return out, bySig, verdict, nil
	}
	var evidence []result.Evidence
	changedOutcome, changedPath := 0, 0
	for _, e := range in.Expectations {
		before, bSigs, bv, err := side(e, rc.BeforeID, beforePtr)
		if err != nil {
			return result.Result{}, err
		}
		after, aSigs, av, err := side(e, afterID, afterPtr)
		if err != nil {
			return result.Result{}, err
		}
		same, onlyBefore, onlyAfter := 0, []map[string]any{}, []map[string]any{}
		for sig, d := range bSigs {
			if _, ok := aSigs[sig]; ok {
				same++
			} else {
				onlyBefore = append(onlyBefore, pathBrief(d))
			}
		}
		for sig, d := range aSigs {
			if _, ok := bSigs[sig]; !ok {
				onlyAfter = append(onlyAfter, pathBrief(d))
			}
		}
		sortBriefs(onlyBefore)
		sortBriefs(onlyAfter)
		change := "same"
		switch {
		case bv != av:
			change = "outcome_changed"
			changedOutcome++
		case len(onlyBefore) > 0 || len(onlyAfter) > 0:
			change = "path_changed"
			changedPath++
		}
		flow := map[string]any{"dst_ip": e.DstIP}
		for k, v := range map[string]string{"src_ip": e.SrcIP, "from": e.From, "protocol": protocolText(e.Protocol), "src_port": e.SrcPort, "dst_port": e.DstPort} {
			if v != "" {
				flow[k] = v
			}
		}
		evidence = append(evidence, result.NewEvidence(result.EvPath, "getPaths", afterPtr, map[string]any{
			"flow": flow, "change": change, "verdict_before": bv, "verdict_after": av,
			"diff":   map[string]any{"same": same, "only_before": onlyBefore, "only_after": onlyAfter},
			"before": before, "after": after},
			fmt.Sprintf("%s: %s (%s -> %s)", e.DstIP, change, bv, av)))
	}
	limits = append(limits, "each flow is searched on both snapshots with intent PREFER_DELIVERED; a path is the same on both when its classification and every device and interface match. Forward picks which paths it returns, so a path missing from one side can be one it did not return: compare distinct_paths and more_paths_exist",
		"flows compares and judges nothing: view verify judges a change against expected outcomes")
	finding := fmt.Sprintf("%d flow(s) compared between snapshot %s and %s: %d changed outcome, %d changed path only", len(in.Expectations), rc.BeforeID, afterID, changedOutcome, changedPath)
	return result.Build(verifyChangeName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, Evidence: evidence,
		NextActions: []string{"investigate-reachability"}})
}

// pathBrief is one path in a few fields: its classification, forwarding outcome and the devices with their interfaces.
func pathBrief(d map[string]any) map[string]any {
	var hops []string
	if hs, ok := d["hops"].([]map[string]any); ok {
		for _, h := range hs {
			hops = append(hops, fmt.Sprintf("%v(%v>%v)", h["device"], dashIfNil(h["ingress_interface"]), dashIfNil(h["egress_interface"])))
		}
	}
	return map[string]any{"classification": d["classification"], "forwarding_outcome": d["forwarding_outcome"], "hops": strings.Join(hops, " > ")}
}

func dashIfNil(v any) any {
	if v == nil {
		return "-"
	}
	return v
}

func sortBriefs(l []map[string]any) {
	sort.Slice(l, func(i, j int) bool { return fmt.Sprint(l[i]["hops"]) < fmt.Sprint(l[j]["hops"]) })
}
