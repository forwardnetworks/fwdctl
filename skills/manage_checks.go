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

const manageChecksName = "edit-checks"

func init() { Register(manageChecksName, manageChecks) }

type manageChecksInput struct {
	NetworkID  string         `json:"network_id"`
	SnapshotID string         `json:"snapshot_id"`
	Action     string         `json:"action"`
	Definition map[string]any `json:"definition"`
	Name       string         `json:"name"`
	Note       string         `json:"note"`
	Tags       []string       `json:"tags"`
	Priority   string         `json:"priority"`
	Persistent bool           `json:"persistent"`
	CheckID    string         `json:"check_id"`
	Apply      bool           `json:"apply"`
}

// manageChecks creates one check on a snapshot, or deactivates one. By default it is a dry run. A created check is local to its
// snapshot unless persistent is stated, because a persistent check is inherited by every later snapshot and Predict run and
// changes their verdicts. It never deactivates a snapshot's checks wholesale.
func manageChecks(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in manageChecksInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	switch in.Action {
	case "create":
		if len(in.Definition) == 0 {
			return result.Result{}, fmt.Errorf("%w: create needs a check definition", ErrInvalidInput)
		}
	case "deactivate":
		if in.CheckID == "" {
			return result.Result{}, fmt.Errorf("%w: deactivate needs check_id", ErrInvalidInput)
		}
	default:
		return result.Result{}, fmt.Errorf("%w: action must be create or deactivate", ErrInvalidInput)
	}
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	sn, err := resolveSnapshot(ctx, s, in.NetworkID, in.SnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	if sn == nil {
		return result.NewUnknown(manageChecksName, "There is no processed snapshot to put a check on", cx,
			[]string{"no such snapshot (or none processed); nothing was changed"}, result.Options{NextActions: []string{"inspect-snapshots"}})
	}
	cx = fwd.Context(in.NetworkID, sn)
	if in.Action == "create" {
		return createCheck(ctx, s, in, sn, cx)
	}
	return deactivateCheck(ctx, s, in, sn, cx)
}

func createCheck(ctx context.Context, s *fwd.Session, in manageChecksInput, sn *forward.Snapshot, cx result.Context) (result.Result, error) {
	sid := string(sn.ID)
	scope := "local to snapshot " + sid
	if in.Persistent {
		scope = "persistent: every later snapshot of the network and every Predict run will inherit and evaluate it"
	}
	if ct, _ := in.Definition["checkType"].(string); strings.EqualFold(ct, "Predefined") && in.Name != "" {
		return result.Result{}, fmt.Errorf("%w: a predefined check is named by Forward from its predefinedCheckType, and Forward answers 400 when a name is sent: leave name out (note, tags and priority are allowed)", ErrInvalidInput)
	}
	if in.Name != "" {
		names, err := s.AllChecks(ctx, sid, nil)
		if err != nil {
			return result.Result{}, err
		}
		for _, c := range names {
			if strings.TrimSpace(c.Name) == in.Name {
				return result.NewUnknown(manageChecksName, fmt.Sprintf("A check named %q already exists on snapshot %s; nothing was created", in.Name, sid), cx,
					[]string{"a check with this name exists (id " + string(c.ID) + "); creating another would double its violations in every report"}, result.Options{NextActions: []string{"check-network-compliance"}})
			}
		}
	}
	target := fmt.Sprintf("snapshot %s", sid)
	ch := result.Change{Action: "create_check", Target: target, After: map[string]any{"name": in.Name, "definition": in.Definition, "persistent": in.Persistent},
		Reversible: true, Undo: "run edit-checks with action=deactivate and the created check's id on this snapshot, apply=true (the check stops evaluating; its history stays readable)"}
	ev := func(mode string, extra map[string]any) []result.Evidence {
		d := map[string]any{"snapshot_id": sid, "name": in.Name, "persistent": in.Persistent, "scope": scope, "mode": mode}
		for k, v := range extra {
			d[k] = v
		}
		return []result.Evidence{result.NewEvidence(result.EvPolicy, "manageChecks", cx.SnapshotID, d, "")}
	}
	next := []string{"check-network-compliance", "check-network-compliance"}
	if !in.Apply {
		return result.Build(manageChecksName, result.OK, fmt.Sprintf("Dry run: would create check %q on snapshot %s (%s). Nothing was changed; run again with apply=true to make it", in.Name, sid, scope),
			result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Evidence: ev(result.ModeDryRun, nil), NextActions: next,
				Limits: []string{"the definition is not validated until Forward evaluates it on apply"}})
	}
	nc := forward.NewCheck{Definition: in.Definition, Name: in.Name, Note: in.Note, Tags: in.Tags, Priority: in.Priority}
	d, err := s.CreateCheck(ctx, sid, nc, in.Persistent)
	if err != nil {
		return result.Result{}, fmt.Errorf("creating the check failed, nothing is known to have been created: %w", err)
	}
	ch.Applied = true
	ch.After = map[string]any{"id": string(d.ID), "name": d.Name, "status": d.Status, "persistent": in.Persistent}
	ch.Undo = fmt.Sprintf("run edit-checks with action=deactivate, check_id=%s, snapshot_id=%s and apply=true (the check stops evaluating; its history stays readable)", d.ID, sid)
	extra := map[string]any{"created_id": string(d.ID), "status": d.Status}
	if d.NumViolations != nil {
		extra["violations"] = *d.NumViolations
	}
	opts := result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(result.ModeApplied, extra), NextActions: next}
	if d.ID == "" {
		return result.Build(manageChecksName, result.Failed, "Forward accepted the create but returned no check id; look for the check on the snapshot", result.Deterministic, cx, opts)
	}
	return result.Build(manageChecksName, result.OK, fmt.Sprintf("Created check %q (id %s) on snapshot %s (%s); it evaluates %s", d.Name, d.ID, sid, scope, d.Status),
		result.Deterministic, cx, opts)
}

func deactivateCheck(ctx context.Context, s *fwd.Session, in manageChecksInput, sn *forward.Snapshot, cx result.Context) (result.Result, error) {
	sid := string(sn.ID)
	d, err := s.CheckDetail(ctx, sid, in.CheckID)
	if fwd.NotFound(err) {
		return result.NewUnknown(manageChecksName, fmt.Sprintf("Snapshot %s has no check %s; nothing was changed", sid, in.CheckID), cx,
			[]string{"no such check on this snapshot (ids are matched exactly)"}, result.Options{NextActions: []string{"check-network-compliance"}})
	}
	if err != nil {
		return result.Result{}, err
	}
	if d.Enabled != nil && !*d.Enabled {
		return result.Build(manageChecksName, result.OK, fmt.Sprintf("Check %q is already deactivated; nothing to change", d.Name), result.Deterministic, cx,
			result.Options{Mode: modeOf(in.Apply), Evidence: []result.Evidence{result.NewEvidence(result.EvPolicy, "manageChecks", cx.SnapshotID, map[string]any{"check_id": in.CheckID, "already_deactivated": true}, "")}})
	}
	// Forward has no call to reactivate. The definition is kept in the change so the check can be re-created.
	ch := result.Change{Action: "deactivate_check", Target: fmt.Sprintf("check %s on snapshot %s", in.CheckID, sid),
		Before: map[string]any{"name": d.Name, "status": d.Status, "priority": d.Priority, "definition": d.Definition}, Reversible: false}
	ev := func(mode string) []result.Evidence {
		return []result.Evidence{result.NewEvidence(result.EvPolicy, "manageChecks", cx.SnapshotID, map[string]any{"check_id": in.CheckID, "name": d.Name, "mode": mode}, "")}
	}
	if !in.Apply {
		return result.Build(manageChecksName, result.OK, fmt.Sprintf("Dry run: would deactivate check %q (%s) on snapshot %s. This cannot be undone through the API (the definition is in the plan so it can be re-created). Nothing was changed; run again with apply=true to make it", d.Name, in.CheckID, sid),
			result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Evidence: ev(result.ModeDryRun), NextActions: []string{"check-network-compliance"}})
	}
	if err := s.DeactivateCheck(ctx, sid, in.CheckID); err != nil {
		return result.Result{}, fmt.Errorf("deactivating the check failed, nothing is known to have changed: %w", err)
	}
	ch.Applied = true
	return result.Build(manageChecksName, result.OK, fmt.Sprintf("Deactivated check %q (%s) on snapshot %s. It cannot be reactivated through the API; its definition is in the change", d.Name, in.CheckID, sid),
		result.Deterministic, cx, result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(result.ModeApplied), NextActions: []string{"check-network-compliance"}})
}

func modeOf(apply bool) string {
	if apply {
		return result.ModeApplied
	}
	return result.ModeDryRun
}
