package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// The describe view of verify-change (the retired review-change-set skill): it reports under the surviving skill's name.
const reviewChangeSetName = verifyChangeName

type reviewChangeSetInput struct {
	NetworkID   string `json:"network_id"`
	ChangeSetID string `json:"change_set_id"`
	Device      string `json:"device"`
}

// reviewChangeSet (verify-change view describe) describes a Predict change set before anyone runs or commits it: what it is based on, which devices it edits,
// whether it has been predicted, and what its checks say on the base and on the newest prediction. It runs nothing.
func reviewChangeSet(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in reviewChangeSetInput
	if err := json.Unmarshal(raw, &in); err != nil || in.ChangeSetID == "" {
		return result.Result{}, fmt.Errorf("%w: view describe requires change_set_id", ErrInvalidInput)
	}
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	cs, err := s.ChangeSet(ctx, in.NetworkID, in.ChangeSetID)
	if err != nil {
		return result.Result{}, err
	}
	if cs == nil {
		return result.NewUnknown(reviewChangeSetName, fmt.Sprintf("The network has no change set %s", in.ChangeSetID), cx,
			[]string{"no such change set in this network (ids are matched exactly)"}, result.Options{})
	}
	devices := make([]string, 0, len(cs.DeviceToChanges))
	for d := range cs.DeviceToChanges {
		devices = append(devices, d)
	}
	sort.Strings(devices)
	detail := map[string]any{"id": string(cs.ID), "name": cs.Name, "description": cs.Description, "base_snapshot_id": string(cs.SnapshotID),
		"devices_edited": devices, "tags": cs.Tags}
	var limits []string
	failed := []string{}

	preds, perr := s.PredictedSnapshots(ctx, in.NetworkID, in.ChangeSetID)
	var newest string
	if perr != nil {
		limits = append(limits, "predicted snapshots could not be listed: "+perr.Error())
	} else {
		rows := make([]map[string]any, 0, len(preds))
		sort.SliceStable(preds, func(i, j int) bool { return preds[i].CreatedAt > preds[j].CreatedAt })
		for _, p := range preds {
			rows = append(rows, map[string]any{"id": string(p.ID), "state": p.State, "created_at": p.CreatedAt, "processed_at": p.ProcessedAt})
			if newest == "" && p.State == "PROCESSED" {
				newest = string(p.ID)
			}
		}
		detail["predictions"] = rows
		if newest == "" {
			limits = append(limits, "the change set has not been predicted (no processed predicted snapshot), so its effect is not measured; use verify-change with run_predict")
		}
	}
	detail["newest_processed_prediction"] = newest

	var base, after map[string]checkOutcome
	if b, err := s.ChangeSetChecks(ctx, in.NetworkID, in.ChangeSetID, string(cs.SnapshotID)); err != nil {
		limits = append(limits, "checks on the base snapshot could not be read: "+err.Error())
	} else {
		base = outcomes(b)
	}
	if newest != "" {
		if a, err := s.ChangeSetChecks(ctx, in.NetworkID, in.ChangeSetID, newest); err != nil {
			limits = append(limits, "checks on the prediction could not be read: "+err.Error())
		} else {
			after = outcomes(a)
		}
	}
	if base != nil {
		detail["checks_on_base"] = len(base)
	}
	if after != nil {
		detail["checks_on_prediction"] = len(after)
		var worse []map[string]any
		ids := make([]string, 0, len(after))
		for id := range after {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			a := after[id]
			b, had := base[id]
			if fwd.IsBad(a.status) && !(had && fwd.IsBad(b.status)) || had && a.violations > b.violations {
				worse = append(worse, map[string]any{"check_id": id, "name": a.name, "status_before": b.status, "status_after": a.status,
					"violations_before": b.violations, "violations_after": a.violations})
				failed = append(failed, a.name)
			}
		}
		detail["checks_that_got_worse"] = worse
		if len(after) == 0 {
			limits = append(limits, "the change set has no checks attached, so an unchanged verdict here proves nothing")
		}
	}

	if in.Device != "" {
		if d, err := s.SecurityRulesDiff(ctx, in.NetworkID, in.ChangeSetID, in.Device); err != nil {
			limits = append(limits, "the firewall rule diff for "+in.Device+" could not be read: "+err.Error())
		} else if d != nil {
			counts := map[string]int{}
			for _, e := range d.Entries {
				counts[e.DiffType]++
			}
			detail["security_rules_diff"] = map[string]any{"device": in.Device, "rulebases": len(d.Rulebases), "entries_by_change": counts}
		}
	}

	ev := []result.Evidence{result.NewEvidence(result.EvPredict, "reviewChangeSet", nil, detail, "")}
	next := []string{"verify-change"}
	switch {
	case len(failed) > 0:
		return result.Build(reviewChangeSetName, result.Failed, fmt.Sprintf("Change set %q makes %d check(s) worse on its prediction: %v", cs.Name, len(failed), failed),
			result.Deterministic, cx, result.Options{Evidence: ev, Limits: limits, NextActions: next})
	case newest == "" || after == nil:
		return result.Build(reviewChangeSetName, result.Unknown, fmt.Sprintf("Change set %q edits %d device(s); its effect is not measured", cs.Name, len(devices)),
			result.NoBasis, cx, result.Options{Evidence: ev, Limits: append(limits, "no prediction result was read")})
	}
	return result.Build(reviewChangeSetName, result.OK, fmt.Sprintf("Change set %q edits %d device(s); no check got worse on its newest prediction %s", cs.Name, len(devices), newest),
		result.Deterministic, cx, result.Options{Evidence: ev, Limits: limits, NextActions: next})
}

type checkOutcome struct {
	name       string
	status     string
	violations int64
}

func outcomes(list []forward.ChangeSetCheckResult) map[string]checkOutcome {
	out := make(map[string]checkOutcome, len(list))
	for _, c := range list {
		o := checkOutcome{name: c.Name, status: c.Status}
		if c.NumViolations != nil {
			o.violations = *c.NumViolations
		}
		out[string(c.ID)] = o
	}
	return out
}
