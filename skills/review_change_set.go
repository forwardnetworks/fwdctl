package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

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
	content := s.ChangeContent(ctx, in.NetworkID, in.ChangeSetID)
	devices := forward.EditedDevices(content.Committed)
	if devices == nil {
		devices = []string{}
	}
	detail := map[string]any{"id": string(cs.ID), "name": cs.Name, "description": cs.Description, "base_snapshot_id": string(cs.SnapshotID),
		"devices_edited": devices, "tags": cs.Tags}
	var limits []string
	if content.CommittedErr != nil {
		limits = append(limits, "the committed version could not be read: "+content.CommittedErr.Error())
	} else {
		detail["committed"] = stagedChanges(content.Committed)
	}
	draftDevices := forward.EditedDevices(content.Draft)
	if content.DraftErr != nil {
		limits = append(limits, "the draft could not be read: "+content.DraftErr.Error())
	} else {
		detail["draft_devices"] = len(draftDevices)
		if len(draftDevices) > 0 {
			detail["draft"] = stagedChanges(content.Draft)
			limits = append(limits, fmt.Sprintf("this login holds an uncommitted draft that edits %d device(s): it is not in a prediction until it is committed (a prediction run commits the draft first)", len(draftDevices)))
		}
	}
	failed := []string{}

	preds, perr := s.PredictedSnapshots(ctx, in.NetworkID, in.ChangeSetID)
	var newest string
	unfinished := false
	progressReads := 0
	if perr != nil {
		limits = append(limits, "predicted snapshots could not be listed: "+perr.Error())
	} else {
		rows := make([]map[string]any, 0, len(preds))
		sort.SliceStable(preds, func(i, j int) bool { return preds[i].CreatedAt > preds[j].CreatedAt })
		for _, p := range preds {
			row := map[string]any{"id": string(p.ID), "state": p.State, "finished": predictionFinished(p.State), "created_at": p.CreatedAt, "processed_at": p.ProcessedAt}
			if !predictionFinished(p.State) && progressReads < maxProgressReads {
				progressReads++
				row["processing"] = processingMap(s.Processing(ctx, string(p.ID)))
			}
			rows = append(rows, row)
			if !predictionFinished(p.State) {
				unfinished = true
			}
			if newest == "" && p.State == "PROCESSED" {
				newest = string(p.ID)
			}
		}
		detail["predictions"] = rows
		if unfinished {
			limits = append(limits, "a predicted snapshot reports UNPROCESSED while it is still being created or computed (inspect-snapshots shows its stage, such as CREATION or COMPUTING): only PROCESSED, FAILED, CANCELED and TIMED_OUT are finished")
		}
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
	if base != nil || after != nil {
		detail["checks"] = checkRows(base, after)
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
	definition json.RawMessage
}

func outcomes(list []forward.ChangeSetCheckResult) map[string]checkOutcome {
	out := make(map[string]checkOutcome, len(list))
	for _, c := range list {
		o := checkOutcome{name: c.Name, status: c.Status, definition: c.Definition}
		if c.NumViolations != nil {
			o.violations = *c.NumViolations
		}
		out[string(c.ID)] = o
	}
	return out
}

// predictionFinished says a predicted snapshot's state is final. A predicted snapshot reports UNPROCESSED while it is still being created or computed, so "not PROCESSING" is not "finished".
func predictionFinished(state string) bool {
	switch strings.ToUpper(state) {
	case "PROCESSED", "FAILED", "CANCELED", "TIMED_OUT", "RESTORE_FAILED":
		return true
	}
	return false
}

// stagedChanges lists, per edited device, what is staged: the number of CLI command lines, a whole-config replacement, pending firewall rule changes (the rules are read with a security rules diff, device by device), the BGP advertisements injected or modified, and the ones withdrawn (keys only).
func stagedChanges(changes map[string]forward.DeviceChanges) map[string]any {
	out := map[string]any{}
	for _, dev := range forward.EditedDevices(changes) {
		c := changes[dev]
		row := map[string]any{}
		if c.Commands != "" {
			row["commands"] = len(strings.Split(strings.TrimRight(c.Commands, "\n"), "\n"))
		}
		if c.HasConfig {
			row["has_config"] = true
		}
		if c.HasSecurityRuleChanges {
			row["has_security_rule_changes"] = true
		}
		if len(c.AddedAdvertisements) > 0 {
			ads := make([]map[string]any, 0, len(c.AddedAdvertisements))
			for _, a := range c.AddedAdvertisements {
				ads = append(ads, map[string]any{"vrf": a.VRF, "external_peer": a.ExternalPeer, "prefix": a.Prefix, "next_hop": a.NextHop, "type": a.Type})
			}
			row["injected_or_modified_advertisements"] = ads
		}
		if len(c.RemovedAdvertisements) > 0 {
			ads := make([]map[string]any, 0, len(c.RemovedAdvertisements))
			for _, a := range c.RemovedAdvertisements {
				ads = append(ads, map[string]any{"vrf": a.VRF, "external_peer": a.ExternalPeer, "prefix": a.Prefix})
			}
			row["withdrawn_advertisements"] = ads
		}
		out[dev] = row
	}
	return out
}

// maxProgressReads bounds the progress reads (two calls each) of one listing.
const maxProgressReads = 5

// processingMap is where a snapshot's processing stands, for the evidence of an unfinished prediction.
func processingMap(p fwd.Processing) map[string]any {
	m := map[string]any{"line": p.Line()}
	if p.Err != nil {
		return m
	}
	m["stage"], m["stage_state"], m["done"] = nilIfEmpty(p.Stage), nilIfEmpty(p.StageState), p.Done
	if p.Objects != nil {
		m["objects"] = *p.Objects
	}
	if p.ElapsedSeconds != nil {
		m["elapsed_seconds"] = *p.ElapsedSeconds
	}
	if p.EstimateSeconds != nil {
		m["estimate_total_seconds"] = *p.EstimateSeconds
	}
	m["over_estimate"] = p.OverEstimate()
	return m
}

// checkRows lists each check attached to the change set with what it is (name, and type and note from its definition when Forward gives them) and its result on the base and on the newest prediction.
func checkRows(base, after map[string]checkOutcome) []map[string]any {
	ids := map[string]bool{}
	for id := range base {
		ids[id] = true
	}
	for id := range after {
		ids[id] = true
	}
	order := make([]string, 0, len(ids))
	for id := range ids {
		order = append(order, id)
	}
	sort.Strings(order)
	rows := make([]map[string]any, 0, len(order))
	for _, id := range order {
		b, hb := base[id]
		a, ha := after[id]
		def := b.definition
		name := b.name
		if ha {
			def, name = a.definition, a.name
		}
		row := map[string]any{"check_id": id, "name": name}
		var d map[string]any
		if len(def) > 0 && json.Unmarshal(def, &d) == nil {
			for _, k := range []string{"type", "checkType"} {
				if v, ok := d[k].(string); ok && v != "" {
					row["type"] = v
					break
				}
			}
			for _, k := range []string{"note", "description"} {
				if v, ok := d[k].(string); ok && v != "" {
					row["note"] = v
					break
				}
			}
		}
		if hb {
			row["status_on_base"], row["violations_on_base"] = b.status, b.violations
		}
		if ha {
			row["status_on_prediction"], row["violations_on_prediction"] = a.status, a.violations
		}
		rows = append(rows, row)
	}
	return rows
}
