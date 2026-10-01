package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const advancedReachabilityName = "edit-advanced-reachability"

func init() { Register(advancedReachabilityName, editAdvancedReachability) }

type advancedReachabilityInput struct {
	NetworkID  string `json:"network_id"`
	SnapshotID string `json:"snapshot_id"`
	Apply      bool   `json:"apply"`
}

// editAdvancedReachability starts the advanced reachability computation of one processed snapshot, the analysis that internet exposure (internet_addressable) and some
// checks are read from. It changes no data and no device, but it is asynchronous and compute-heavy, so it is a dry run unless apply is true and it refuses every state
// except UNPROCESSED: Forward ignores the request for a snapshot whose state is final (PROCESSED, FAILED, CANCELED, TIMED_OUT) and would be a request that does nothing.
func editAdvancedReachability(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in advancedReachabilityInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if in.NetworkID == "" || in.SnapshotID == "" {
		return result.Result{}, fmt.Errorf("%w: network_id and snapshot_id are required", ErrInvalidInput)
	}
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	sn, err := s.Snapshot(ctx, in.NetworkID, in.SnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	if sn == nil {
		return result.NewUnknown(advancedReachabilityName, fmt.Sprintf("The network holds no snapshot %s", in.SnapshotID), cx,
			[]string{"no such snapshot in this network (ids are matched exactly); nothing was changed"}, result.Options{NextActions: []string{"inspect-snapshots"}})
	}
	cx = fwd.Context(in.NetworkID, sn)
	mode := result.ModeDryRun
	if in.Apply {
		mode = result.ModeApplied
	}
	before := advancedState(*sn)
	ev := func(extra map[string]any) []result.Evidence {
		d := map[string]any{"snapshot_id": in.SnapshotID, "snapshot_state": sn.State, "advanced_reachability_before": before, "mode": mode, "devices": sn.TotalDevices}
		for k, v := range extra {
			d[k] = v
		}
		return []result.Evidence{result.NewEvidence(result.EvState, "computeAdvancedReachability", cx.SnapshotID, d, "")}
	}
	next := []string{"inspect-snapshots", "inspect-vulnerabilities"}
	refuse := func(finding string, limits ...string) (result.Result, error) {
		return result.Build(advancedReachabilityName, result.Failed, finding, result.Deterministic, cx,
			result.Options{Mode: mode, Evidence: ev(nil), NextActions: next, Limits: append([]string{"nothing was changed"}, limits...)})
	}
	if sn.State != "PROCESSED" {
		return refuse(fmt.Sprintf("Snapshot %s is %s, not PROCESSED: advanced reachability needs the reachability stage finished", in.SnapshotID, sn.State),
			"wait for the snapshot to finish processing (inspect-snapshots), or reprocess it if it failed (edit-snapshot-reprocess), then run this again")
	}
	switch before {
	case forward.AdvancedReachabilityUnprocessed:
	case forward.AdvancedReachabilityProcessing:
		return refuse(fmt.Sprintf("Advanced reachability is already computing for snapshot %s (PROCESSING)", in.SnapshotID),
			"wait for it to finish: watch advanced_reachability in inspect-snapshots until it reads PROCESSED")
	case forward.AdvancedReachabilityProcessed:
		return refuse(fmt.Sprintf("Advanced reachability is already computed for snapshot %s (PROCESSED)", in.SnapshotID),
			"if internet_addressable is still unavailable the reason is not this: read inspect-vulnerabilities (it says why) and inspect-topology kind external for the internet node")
	case forward.AdvancedReachabilityFailed, forward.AdvancedReachabilityCanceled, forward.AdvancedReachabilityTimedOut:
		return refuse(fmt.Sprintf("Advanced reachability for snapshot %s ended %s, a final state", in.SnapshotID, before),
			"Forward ignores a request for a snapshot whose advanced reachability is in a final state (the call is accepted and does nothing); reprocessing the snapshot (edit-snapshot-reprocess) clears the state, after which this can run again. Read the cause first: investigate-collection-failure, and the compute limits in inspect-environment")
	default:
		return result.NewUnknown(advancedReachabilityName, fmt.Sprintf("Forward does not report advanced reachability state for snapshot %s", in.SnapshotID), cx,
			[]string{"the snapshot carries no advancedReachabilityState (an older Forward build, or the field is absent), so this skill cannot tell whether a request would do anything; nothing was changed"},
			result.Options{NextActions: []string{"inspect-environment"}})
	}
	// Forward ignores the request's cause: with flow computation disabled the stages after reachability are not run, so a snapshot stays UNPROCESSED and the request would
	// not produce exposure. The organization property says so when it is the cause; a network-level override and the license are not returned by any API.
	flow := readFlowFacts(ctx, s)
	if flow.Read && flow.Disabled {
		return refuse(fmt.Sprintf("Advanced reachability for snapshot %s is blocked, not just never triggered: flow computation is disabled", in.SnapshotID),
			"the organization property DISABLE_FLOW_COMPUTATION is true: Forward does not run the stages after reachability, so this request would not produce a DAG or internet exposure, and the snapshot would stay UNPROCESSED",
			"enable it first (the property is configurable by an organization administrator of an on-premises Forward, or by Forward support; it takes effect for existing snapshots after they are reprocessed); this skill does not change organization properties",
			"read inspect-environment (features) for the value and where it is set")
	}
	limits := []string{
		"asynchronous: Forward accepts the request and computes in the background, so the state reads PROCESSING and later PROCESSED; this skill does not wait (inspect-snapshots shows advanced_reachability)",
		fmt.Sprintf("compute-heavy: it builds the network's flow DAG for %d devices on Forward's compute workers (the same pool that processes snapshots, bounded per worker by the REACHABILITY_MAX_CONCURRENT_DEVICES_PER_WORKER property and timed out by REACHABILITY_TIMEOUT_MINUTES), so on a large network start it when other processing is idle", sn.TotalDevices),
		"it changes no data and no device; internet exposure (internet_addressable) is computed by Forward itself once the DAG is merged, and only when the snapshot has an internet node",
		"by default Forward runs it only on request (organization property ADVANCED_REACHABILITY_ANALYSIS = ON_DEMAND); a reprocess or a backdate clears it, so it must be run again for the reprocessed snapshot unless that property is ASYNC",
		flowLimit(flow),
		"a request for a snapshot in a final state is ignored by Forward, which is why this skill refuses those; the call needs the same role as a reprocess and view-paths access",
	}
	ch := result.Change{Action: "compute-advanced-reachability", Target: "snapshot " + in.SnapshotID, Before: before, After: "PROCESSING, then PROCESSED when it finishes", Reversible: false,
		Undo: "none: it adds computed analysis and removes nothing; a reprocess of the snapshot would clear it"}
	if !in.Apply {
		return result.Build(advancedReachabilityName, result.OK, fmt.Sprintf("Dry run: would start advanced reachability for snapshot %s (%d devices, now %s). Nothing was changed; run again with apply=true to start it", in.SnapshotID, sn.TotalDevices, before),
			result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Evidence: ev(nil), Limits: limits, NextActions: next})
	}
	if err := s.ComputeAdvancedReachability(ctx, in.SnapshotID); err != nil {
		switch {
		case errors.Is(err, forward.ErrSnapshotNotProcessed):
			return refuse(fmt.Sprintf("Forward says snapshot %s has not finished the reachability stage", in.SnapshotID), "retry once processing has finished: "+err.Error())
		case errors.Is(err, forward.ErrSnapshotProcessingFailed):
			return refuse(fmt.Sprintf("Forward says processing of snapshot %s failed, so advanced reachability cannot run", in.SnapshotID), "reprocess it first (edit-snapshot-reprocess): "+err.Error())
		case forward.IsStatus(err, 404):
			return result.NewUnknown(advancedReachabilityName, fmt.Sprintf("Forward has no snapshot %s", in.SnapshotID), cx, []string{"Forward answered 404; nothing was changed"}, result.Options{NextActions: []string{"inspect-snapshots"}})
		}
		return result.Result{}, fmt.Errorf("starting advanced reachability failed, nothing is known to have changed: %w", err)
	}
	ch.Applied = true
	after, note := "", ""
	if now, rerr := s.Snapshot(ctx, in.NetworkID, in.SnapshotID); rerr != nil || now == nil {
		note = "Forward accepted the request but the snapshot could not be read back, so the state after is not known"
	} else {
		after = advancedState(*now)
		if after == forward.AdvancedReachabilityUnprocessed {
			note = "Forward accepted the request (204) but still reports UNPROCESSED: the work starts asynchronously, so check inspect-snapshots again shortly; a state that stays UNPROCESSED means it did not start, and one cause the API cannot rule out is flow computation disabled for the network by a network-level override or an unlicensed path analysis (UNKNOWN: neither is returned by any API)"
		}
	}
	if after != "" {
		ch.After = after
	}
	limits = append(limits, "the state after is read once, right after Forward accepted the request; it is not the end state")
	if note != "" {
		limits = append(limits, note)
	}
	return result.Build(advancedReachabilityName, result.OK, fmt.Sprintf("Started advanced reachability for snapshot %s (%s -> %s)", in.SnapshotID, before, orNone(after)),
		result.Deterministic, cx, result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(map[string]any{"advanced_reachability_after": after}), Limits: limits, NextActions: next})
}

// flowLimit is the line a start request carries about flow computation: what the organization property says, and the two causes no API shows.
func flowLimit(f flowFacts) string {
	switch {
	case !f.Read:
		return "whether flow computation is enabled is UNKNOWN: the organization properties could not be read (" + f.Err + "); if it is disabled this request produces nothing"
	case f.Mode == "ASYNC":
		return "the organization property ADVANCED_REACHABILITY_ANALYSIS is ASYNC, so Forward should have started this after processing; that it did not has a cause the API cannot tell (a predicted snapshot, or flow computation disabled by a network-level override or an unlicensed path analysis, neither returned by any API): this request starts it by hand, and if flow computation is disabled it will not produce exposure"
	}
	return "the organization property DISABLE_FLOW_COMPUTATION is false; a network-level override of it or a license without path analysis would still disable flow computation and are returned by no API, so a request that leaves the state UNPROCESSED may mean that (UNKNOWN)"
}
