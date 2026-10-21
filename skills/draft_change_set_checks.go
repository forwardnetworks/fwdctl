package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// draftCheck is a check attached to a change set: the path or NQE verification a use case carries with it, evaluated on the change set's base and on each prediction.
type draftCheck struct {
	Type                     string                     `json:"type"` // Isolation, Reachability, Existential or NQE
	Filters                  json.RawMessage            `json:"filters"`
	NoiseTypes               []string                   `json:"noise_types"`
	HeaderFieldsWithDefaults []string                   `json:"header_fields_with_defaults"`
	ReturnPath               string                     `json:"return_path"`
	QueryID                  string                     `json:"query_id"`
	Params                   map[string]json.RawMessage `json:"params"`
}

func (c draftCheck) definition() forward.ChangeSetCheckDefinition {
	t := strings.ToLower(strings.TrimSpace(c.Type))
	canon := map[string]string{"isolation": forward.ChangeSetCheckIsolation, "reachability": forward.ChangeSetCheckReachability,
		"existential": forward.ChangeSetCheckExists, "exists": forward.ChangeSetCheckExists, "nqe": forward.ChangeSetCheckNQE}[t]
	if canon == "" {
		canon = c.Type
	}
	return forward.ChangeSetCheckDefinition{CheckType: canon, Filters: c.Filters, NoiseTypes: c.NoiseTypes, HeaderFieldsWithDefaults: c.HeaderFieldsWithDefaults,
		ReturnPath: c.ReturnPath, QueryID: c.QueryID, Params: c.Params}
}

func isIntentCheck(t string) bool { return t != forward.ChangeSetCheckNQE }

// changeSetCheck is edit-change-set lifecycle add_check, edit_check and remove_check: attach, change or detach a change set's own check. Forward has no dry run of it, so a dry run validates the definition and plans.
func changeSetCheck(ctx context.Context, s *fwd.Session, in draftChangeSetInput, op string) (result.Result, error) {
	l := in.Lifecycle
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	var def forward.ChangeSetCheckDefinition
	switch op {
	case "add_check":
		if l.Check == nil {
			return result.Result{}, fmt.Errorf("%w: add_check needs lifecycle.check (type, and filters for an intent check or query_id for an NQE check)", ErrInvalidInput)
		}
		def = l.Check.definition()
		if err := def.Validate(); err != nil {
			return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
		if def.CheckType == forward.ChangeSetCheckNQE && strings.TrimSpace(l.Name) != "" {
			return result.Result{}, fmt.Errorf("%w: an NQE check takes no name (Forward names it after the query)", ErrInvalidInput)
		}
	case "edit_check", "remove_check":
		if strings.TrimSpace(l.CheckID) == "" {
			return result.Result{}, fmt.Errorf("%w: %s needs lifecycle.check_id (verify-change view describe lists the checks)", ErrInvalidInput, op)
		}
		if op == "edit_check" {
			if l.Check == nil && strings.TrimSpace(l.Name) == "" {
				return result.Result{}, fmt.Errorf("%w: edit_check needs lifecycle.name and/or lifecycle.check (a new definition replaces the whole old one)", ErrInvalidInput)
			}
			if l.Check != nil {
				def = l.Check.definition()
				if err := def.Validate(); err != nil {
					return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
				}
			}
		}
	}
	list, err := s.ChangeSets(ctx, in.NetworkID)
	if err != nil {
		if r, ok := denialResult(draftChangeSetName, err, cx, "listing the network's change sets"); ok {
			return r, nil
		}
		return result.Result{}, err
	}
	var cs *forward.ChangeSet
	for i := range list {
		if string(list[i].ID) == in.ChangeSetID {
			cs = &list[i]
		}
	}
	if cs == nil {
		return result.NewUnknown(draftChangeSetName, fmt.Sprintf("Change set %s is not in network %s", in.ChangeSetID, in.NetworkID), cx,
			[]string{"no change set with that id was listed; nothing was changed (verify-change view list shows the ids)"}, result.Options{NextActions: []string{"verify-change"}})
	}
	id := in.ChangeSetID
	existing, lerr := s.ChangeSetChecks(ctx, in.NetworkID, id, string(cs.SnapshotID))
	var limits []string
	if lerr != nil {
		limits = append(limits, "the checks already attached could not be read ("+lerr.Error()+"), so a duplicate was not looked for")
	}
	var found *forward.ChangeSetCheckResult
	for i := range existing {
		if string(existing[i].ID) == strings.TrimSpace(l.CheckID) {
			found = &existing[i]
		}
	}
	if op != "add_check" && found == nil && lerr == nil {
		return result.NewUnknown(draftChangeSetName, fmt.Sprintf("Change set %s has no check %s", id, l.CheckID), cx,
			[]string{"no check with that id is attached; nothing was changed (verify-change view describe lists the checks with their ids)"}, result.Options{NextActions: []string{"verify-change"}})
	}
	ch := result.Change{Target: fmt.Sprintf("change set %s (%s), check", id, cs.Name), Before: map[string]any{"change_set_id": id}, Reversible: true}
	var do func() (any, error)
	var verb string
	switch op {
	case "add_check":
		ch.Action, verb = "add_check", "attach a "+def.CheckType+" check to"
		newJSON, _ := json.Marshal(def)
		ch.After = map[string]any{"name": l.Name, "definition": json.RawMessage(newJSON)}
		ch.Undo = "remove_check on the new check's id (the response gives it)"
		for _, e := range existing {
			if d, err := e.ParsedDefinition(); err == nil {
				if b, _ := json.Marshal(d); string(b) == string(newJSON) {
					limits = append(limits, fmt.Sprintf("check %s already has this definition: Forward does not deduplicate, so adding it again makes a duplicate", e.ID))
				}
			}
		}
		limits = append(limits, "Forward runs the check on the base snapshot before it answers, so adding one can be slow; a check that fails to run still returns success with status ERROR or TIMEOUT, and a timeout can leave the check attached: list the checks (verify-change view describe) before adding it again")
		do = func() (any, error) {
			r, _, e := s.Client.Predict.AddChangeSetCheck(ctx, in.NetworkID, id, forward.NewChangeSetCheck{Name: strings.TrimSpace(l.Name), Definition: def})
			if r == nil {
				return nil, e
			}
			return map[string]any{"check_id": string(r.ID), "name": r.Name, "status_on_base": r.Status}, e
		}
	case "edit_check":
		ch.Action, verb = "update_check", "change check "+l.CheckID+" of"
		ch.Before = map[string]any{"check_id": l.CheckID, "name": nameOf(found), "definition": rawOf(found)}
		patch := forward.ChangeSetCheckPatch{}
		after := map[string]any{}
		if n := strings.TrimSpace(l.Name); n != "" {
			patch.Name, after["name"] = &n, n
		}
		if l.Check != nil {
			if found != nil {
				if old, perr := found.ParsedDefinition(); perr == nil && isIntentCheck(old.CheckType) != isIntentCheck(def.CheckType) {
					return result.Result{}, fmt.Errorf("%w: an edit cannot change a check's category (an intent check stays intent, an NQE check stays NQE): remove it and add the new one", ErrInvalidInput)
				}
			}
			patch.Definition = &def
			b, _ := json.Marshal(def)
			after["definition"] = json.RawMessage(b)
		}
		ch.After = after
		ch.Undo = "edit_check again with the earlier name or definition (Before)"
		limits = append(limits, "a new definition replaces the whole old one; the list fields keep their meaning: absent header_fields_with_defaults lets Forward apply its default, an empty list means none")
		do = func() (any, error) {
			_, e := s.Client.Predict.UpdateChangeSetCheck(ctx, in.NetworkID, id, l.CheckID, patch)
			return after, e
		}
	case "remove_check":
		ch.Action, verb = "remove_check", "remove check "+l.CheckID+" from"
		ch.Before = map[string]any{"check_id": l.CheckID, "name": nameOf(found), "definition": rawOf(found)}
		ch.Undo = "add_check with the definition in Before (the new check gets a new id)"
		do = func() (any, error) {
			_, e := s.Client.Predict.DeleteChangeSetCheck(ctx, in.NetworkID, id, l.CheckID)
			return nil, e
		}
	}
	if def.ReturnPath == forward.ReturnPathSymmetric {
		limits = append(limits, "returnPath SYMMETRIC: a colleague measured on one build that Forward accepts and stores it but symmetry was not asserted (the check passed on a flow whose two directions crossed different backbones, and on every prediction of a change set that moved traffic), so a passing check with it proves nothing about the return path; to assert symmetry add one check per direction, each through the device that direction must cross, and read the pair together. Not confirmed on other builds")
	}
	limits = append(limits, "needs MANAGE_CHANGE_SETS and the organization property PREDICT_MODELING; an unlicensed check category is refused with 403", "this route is a preview of Forward's appserver (not in the published OpenAPI), so its behaviour can change between builds", "a change set's checks run on its base and on each prediction; they do not touch a device")
	if !in.Apply {
		return result.Build(draftChangeSetName, result.OK, fmt.Sprintf("Dry run: would %s change set %s (%s). Nothing was changed; run again with apply=true", verb, id, cs.Name), result.Deterministic, cx,
			result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Limits: limits, NextActions: []string{"verify-change"},
				Evidence: []result.Evidence{result.NewEvidence(result.EvPredict, "changeSetCheck", nil, map[string]any{"mode": result.ModeDryRun, "op": op, "change_set_id": id}, "")}})
	}
	detail, derr := do()
	if derr != nil {
		return result.Result{}, fmt.Errorf("%s change set %s failed, it may be unchanged (list the checks before retrying): %w", verb, id, derr)
	}
	ch.Applied = true
	finding := fmt.Sprintf("Applied: %s change set %s (%s)", verb, id, cs.Name)
	if m, ok := detail.(map[string]any); ok && op == "add_check" {
		st := fmt.Sprint(m["status_on_base"])
		finding = fmt.Sprintf("Attached check %v to change set %s (%s); its result on the base snapshot is %s", m["check_id"], id, cs.Name, st)
		if strings.EqualFold(st, "ERROR") || strings.EqualFold(st, "TIMEOUT") {
			limits = append(limits, fmt.Sprintf("the check was attached but did not run on the base snapshot (status %s): its result means nothing; list the checks before adding it again, and read why in the Forward GUI", st))
		}
	}
	return result.Build(draftChangeSetName, result.OK, finding, result.Deterministic, cx, result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Limits: limits, NextActions: []string{"verify-change"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvPredict, "changeSetCheck", nil, map[string]any{"mode": result.ModeApplied, "op": op, "change_set_id": id, "result": detail}, finding)}})
}

func nameOf(c *forward.ChangeSetCheckResult) string {
	if c == nil {
		return ""
	}
	return c.Name
}

func rawOf(c *forward.ChangeSetCheckResult) json.RawMessage {
	if c == nil || len(c.Definition) == 0 {
		return nil
	}
	return c.Definition
}
