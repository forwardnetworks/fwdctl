package skills

import (
	"context"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
)

// flowFacts is what the organization's effective configuration says about the two properties that decide whether advanced reachability can produce anything:
// DISABLE_FLOW_COMPUTATION and ADVANCED_REACHABILITY_ANALYSIS. Forward's source (checked at the pinned primary build) gives the distinction these skills need:
//
//   - REACHABILITY_COMPUTATION_DISABLED is Forward saying flow computation is disabled: the license tier excludes path analysis, or DISABLE_FLOW_COMPUTATION is set
//     for the network. It is reported only once the snapshot's DAG merge stage has finished.
//   - PENDING_ADVANCED_REACHABILITY is reported whenever the DAG merge stage has not finished. When flow computation is disabled the stages after reachability
//     are marked not run, so a disabled network can read PENDING (and the snapshot UNPROCESSED) for ever: PENDING alone does not rule "disabled" out.
//   - ADVANCED_REACHABILITY_ANALYSIS ON_DEMAND (the default) never starts it by itself; ASYNC starts it after processing, except for predicted snapshots and when flow
//     computation is disabled for the network.
//
// The API returns the organization-level property only. A network-level override of either property and the license tier are held by Forward and returned by no
// API, so when the organization property says "not disabled" the cause of a disabled computation can still be one of those two, and the answer is UNKNOWN which.
type flowFacts struct {
	Read     bool   // the effective organization properties were read
	Disabled bool   // disable_flow_computation is true
	Mode     string // advanced_reachability_analysis (ASYNC or ON_DEMAND), "" when not returned
	Err      string // why the read failed
}

func readFlowFacts(ctx context.Context, s *fwd.Session) flowFacts {
	eff, err := s.EffectiveOrgConfig(ctx)
	if err != nil {
		return flowFacts{Err: err.Error()}
	}
	return flowFacts{Read: true, Disabled: strings.EqualFold(eff["disable_flow_computation"], "true"), Mode: strings.ToUpper(strings.TrimSpace(eff["advanced_reachability_analysis"]))}
}

// disabledCause says what is known about why flow computation is disabled, and what is not.
func (f flowFacts) disabledCause() string {
	switch {
	case !f.Read:
		return "the organization properties could not be read (" + f.Err + "), so whether the cause is the organization property DISABLE_FLOW_COMPUTATION, a network-level override of it or a license without path analysis is UNKNOWN"
	case f.Disabled:
		return "the organization property DISABLE_FLOW_COMPUTATION is true"
	}
	return "the organization property DISABLE_FLOW_COMPUTATION is false, so the cause is a network-level override of it or a license without path analysis; Forward returns neither through any API, so which one is UNKNOWN"
}

// unprocessedWhy explains an UNPROCESSED advanced reachability state (or Forward's PENDING_ADVANCED_REACHABILITY) from the properties, as far as they say.
func (f flowFacts) unprocessedWhy() string {
	switch {
	case !f.Read:
		return "advanced reachability was never computed for this snapshot (state UNPROCESSED), and internet exposure is computed from it; the organization properties could not be read (" + f.Err + "), so whether it was never asked for or is blocked by disabled flow computation is UNKNOWN"
	case f.Disabled:
		return "advanced reachability is blocked, not just never triggered: the organization property DISABLE_FLOW_COMPUTATION is true, so Forward does not run the stages after reachability and the snapshot stays UNPROCESSED (exposure then reads PENDING_ADVANCED_REACHABILITY, which looks like never triggered). Starting it would not produce exposure"
	case f.Mode == "ON_DEMAND":
		return "advanced reachability was never computed for this snapshot (state UNPROCESSED), by design: ADVANCED_REACHABILITY_ANALYSIS is ON_DEMAND, so nothing starts it unless someone asks, and internet exposure is computed from it"
	case f.Mode == "ASYNC":
		return "advanced reachability was never computed for this snapshot (state UNPROCESSED) although ADVANCED_REACHABILITY_ANALYSIS is ASYNC, which starts it after processing. Forward skips predicted snapshots and a network with flow computation disabled, and the API cannot tell these apart or say the trigger did not run: which one is UNKNOWN (a network-level DISABLE_FLOW_COMPUTATION override and the license are not returned by any API)"
	}
	return "advanced reachability was never computed for this snapshot (state UNPROCESSED), and internet exposure is computed from it; the organization property ADVANCED_REACHABILITY_ANALYSIS was not returned, so whether anything would have started it is UNKNOWN"
}
