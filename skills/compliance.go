package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"
	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const complianceName = "check-network-compliance"

func init() { Register(complianceName, checkNetworkCompliance) }

const (
	sampleRows   = 5
	defaultCited = 10
)

type nqePolicy struct {
	Name         string         `json:"name"`
	Query        string         `json:"query"`
	QueryID      string         `json:"query_id"`
	Parameters   map[string]any `json:"parameters"`
	ScopeQuery   string         `json:"scope_query"`
	ScopeQueryID string         `json:"scope_query_id"`
}

type complianceInput struct {
	NetworkID  string     `json:"network_id"`
	SnapshotID string     `json:"snapshot_id"`
	UseChecks  bool       `json:"use_checks"`
	CheckIDs   []string   `json:"check_ids"`
	NQE        *nqePolicy `json:"nqe"`
	Limit      int        `json:"limit"`
	// View is "evaluate" (the default: judge a policy) or "read" (list the checks Forward has, one check, or the catalogue).
	View string `json:"view"`
	// Inputs of view read; giving one without a view selects read.
	CheckID   string   `json:"check_id"`
	Statuses  []string `json:"statuses"`
	Catalogue bool     `json:"catalogue"`
}

// notEvaluated are statuses where Forward has not produced a verdict; they are unknown, never a pass.
var notEvaluated = map[string]bool{"NONE": true, "PROCESSING": true, "ERROR": true, "TIMEOUT": true,
	"REQUIRES_ADDITIONAL_SNAPSHOT_PROCESSING": true, "": true}

type policyTally struct {
	violated    []string
	unevaluated int
	compliant   int
	evidence    []result.Evidence
	limits      []string
	cited       int // how many failing checks to cite as evidence
}

func checkNetworkCompliance(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in complianceInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if in.View == "" && (in.CheckID != "" || in.Catalogue || len(in.Statuses) > 0) {
		in.View = "read"
	}
	switch in.View {
	case "", "evaluate":
	case "read":
		return inspectChecks(ctx, s, raw)
	default:
		return result.Result{}, fmt.Errorf("%w: view must be evaluate or read", ErrInvalidInput)
	}
	useChecks := in.UseChecks || len(in.CheckIDs) > 0
	if !useChecks && in.NQE == nil {
		return result.NewError(complianceName, "give use_checks (or check_ids) and/or an nqe policy", fwd.Context(in.NetworkID, nil)), nil
	}
	snap, err := resolveSnapshot(ctx, s, in.NetworkID, in.SnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	cx := fwd.Context(in.NetworkID, snap)
	if !fwd.IsReady(snap) {
		return result.NewUnknown(complianceName, "No processed snapshot is available to evaluate", cx,
			[]string{"no processed snapshot; nothing was evaluated"},
			result.Options{NextActions: []string{"investigate-collection-failure"}})
	}
	sid := fwd.SnapshotIDPtr(snap)
	snapID := fwd.SnapshotID(cx)
	var t policyTally
	t.cited = in.Limit
	if t.cited <= 0 {
		t.cited = defaultCited
	}
	if cx.State == "predicted" {
		t.limits = append(t.limits, "evaluated on a predicted snapshot, not collected state")
	}
	if useChecks {
		if err := evalChecks(ctx, s, snapID, sid, in.CheckIDs, &t); err != nil {
			return result.Result{}, err
		}
	}
	if in.NQE != nil {
		if err := evalNQE(ctx, s, in.NetworkID, snapID, sid, in.NQE, &t); err != nil {
			return result.Result{}, err
		}
	}
	switch {
	case len(t.violated) > 0:
		names, _ := result.CapRow(t.violated, 5) // the finding states the full count
		return result.Build(complianceName, result.Failed, fmt.Sprintf("%d policy(ies) violated, worst first, violation count in parentheses: %s", len(t.violated), strings.Join(names, ", ")),
			result.Deterministic, cx, result.Options{Limits: t.limits, Evidence: t.evidence, NextActions: []string{"inspect-history", "plan-compliance-audit", "investigate-reachability"}})
	case t.unevaluated > 0 || t.compliant == 0:
		limits := t.limits
		if len(limits) == 0 {
			limits = []string{"nothing was evaluated"}
		}
		return result.NewUnknown(complianceName, "Some policies were not evaluated, so compliance is undetermined", cx, limits,
			result.Options{Evidence: t.evidence})
	}
	ev := t.evidence
	if len(ev) == 0 {
		ev = []result.Evidence{result.NewEvidence(result.EvPolicy, "getChecks", sid, map[string]any{"compliant": t.compliant}, "")}
	}
	return result.Build(complianceName, result.OK, fmt.Sprintf("%d policy(ies) evaluated and compliant", t.compliant),
		result.Deterministic, cx, result.Options{Limits: t.limits, Evidence: ev})
}

func evalChecks(ctx context.Context, s *fwd.Session, snapID string, sid *string, ids []string, t *policyTally) error {
	checks, err := s.Checks(ctx, snapID)
	if err != nil {
		return err
	}
	if len(ids) > 0 {
		want := map[string]bool{}
		for _, id := range ids {
			want[id] = true
		}
		have := map[string]bool{}
		for _, c := range checks {
			have[c.ID] = true
		}
		var missing []string
		for _, id := range ids {
			if !have[id] {
				missing = append(missing, id)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			t.limits = append(t.limits, "requested check(s) not found on the snapshot: "+strings.Join(missing, ", "))
		}
		kept := checks[:0:0]
		for _, c := range checks {
			if want[c.ID] {
				kept = append(kept, c)
			}
		}
		checks = kept
	}
	disabled, enabled := 0, []fwd.CheckState{}
	for _, c := range checks {
		if c.Enabled {
			enabled = append(enabled, c)
		} else {
			disabled++
		}
	}
	if disabled > 0 {
		t.limits = append(t.limits, fmt.Sprintf("%d disabled check(s) were not evaluated", disabled))
	}
	var unevaluated []string
	var failing []fwd.CheckState
	for _, c := range enabled {
		switch {
		case c.Status == "FAIL":
			failing = append(failing, c)
		case notEvaluated[c.Status]:
			unevaluated = append(unevaluated, c.Name)
		default:
			t.compliant++
		}
	}
	// Cite the worst failing checks only: a network can fail hundreds, and every cited check costs a harness tokens.
	sort.SliceStable(failing, func(i, j int) bool { return failing[i].Violations > failing[j].Violations })
	for _, c := range failing {
		t.violated = append(t.violated, fmt.Sprintf("%s (%d)", c.Name, c.Violations))
	}
	for i, c := range failing {
		if i == t.cited {
			t.limits = append(t.limits, fmt.Sprintf("%d checks failed; the %d with the most violations are cited (set limit for more)", len(failing), t.cited))
			break
		}
		t.evidence = append(t.evidence, result.NewEvidence(result.EvPolicy, "getChecks", sid,
			map[string]any{"check_id": c.ID, "name": c.Name, "status": c.Status, "violations": c.Violations},
			fmt.Sprintf("%s: FAIL (%d violations)", c.Name, c.Violations)))
	}
	t.unevaluated += len(unevaluated)
	if len(unevaluated) > 0 {
		shown, _ := result.CapRow(unevaluated, 5) // the limit states the full count
		t.limits = append(t.limits, fmt.Sprintf("%d check(s) were not evaluated (status NONE/PROCESSING/ERROR/TIMEOUT): %s", len(unevaluated), strings.Join(shown, ", ")))
	}
	if len(enabled) == 0 {
		t.unevaluated++
		t.limits = append(t.limits, "no enabled checks were found; nothing was evaluated")
	}
	return nil
}

func evalNQE(ctx context.Context, s *fwd.Session, networkID, snapID string, sid *string, p *nqePolicy, t *policyTally) error {
	name := firstNonEmpty(p.Name, p.QueryID, "nqe policy")
	if p.Query == "" && p.QueryID == "" {
		t.limits = append(t.limits, name+": no violations query given")
		t.unevaluated++
		return nil
	}
	viol, err := s.RunNQE(ctx, networkID, fwd.NQERun{Query: p.Query, QueryID: p.QueryID, Parameters: p.Parameters, SnapshotID: snapID})
	if errors.Is(err, fwd.ErrSnapshotNotReady) {
		t.limits = append(t.limits, name+": "+err.Error())
		t.unevaluated++
		return nil
	}
	if err != nil {
		return err
	}
	scope := int64(-1)
	if p.ScopeQuery != "" || p.ScopeQueryID != "" {
		sc, err := s.RunNQE(ctx, networkID, fwd.NQERun{Query: p.ScopeQuery, QueryID: p.ScopeQueryID, Parameters: p.Parameters, SnapshotID: snapID, Limit: 1})
		if err != nil {
			return err
		}
		scope = sc.Total
	}
	scopeVal := any(nil)
	if scope >= 0 {
		scopeVal = scope
	}
	// A catalog query can return a row for every device and control, passes included, with a `violation` column (true, false or
	// none). Its row count is then not a violation count: say so instead of reporting every row as a violation.
	if has, not := rowsNotViolations(viol.Items); has && not > 0 {
		rows, _ := result.CapRow(viol.Items, sampleRows)
		t.limits = append(t.limits, fmt.Sprintf("%s: the rows are not all violations (a `violation` column is false or empty on %d of the %d rows read, %d rows in all), so the row count is not a violation count; filter the query to violation == true (author-nqe-query) and run it again", name, not, len(viol.Items), viol.Total))
		t.unevaluated++
		t.evidence = append(t.evidence, result.NewEvidence(result.EvNQE, "runNqeQuery", sid,
			map[string]any{"name": name, "rows": viol.Total, "violations": nil, "scope": scopeVal, "sample": rows}, fmt.Sprintf("%s: %d rows, not all violations", name, viol.Total)))
		return nil
	}
	if viol.Truncated {
		t.limits = append(t.limits, fmt.Sprintf("%s: %d violations, %d returned", name, viol.Total, len(viol.Items)))
	}
	switch {
	case viol.Total > 0:
		rows, _ := result.CapRow(viol.Items, sampleRows) // "violations" beside the sample is the full count
		t.violated = append(t.violated, fmt.Sprintf("%s (%d)", name, viol.Total))
		t.evidence = append(t.evidence, result.NewEvidence(result.EvNQE, "runNqeQuery", sid,
			map[string]any{"name": name, "violations": viol.Total, "scope": scopeVal, "sample": rows}, fmt.Sprintf("%s: %d violation(s)", name, viol.Total)))
	case scope < 0:
		t.limits = append(t.limits, name+": zero violations but no scope query, so an empty scope cannot be ruled out")
		t.unevaluated++
	case scope == 0:
		t.limits = append(t.limits, name+": the scope is empty; nothing was evaluated")
		t.unevaluated++
	case viol.PredictedSnapshot:
		t.limits = append(t.limits, name+": zero violations on a predicted snapshot; some signals are empty there regardless of the network")
		t.unevaluated++
	default:
		t.compliant++
		t.evidence = append(t.evidence, result.NewEvidence(result.EvNQE, "runNqeQuery", sid,
			map[string]any{"name": name, "violations": 0, "scope": scope, "sample": []any{}}, fmt.Sprintf("%s: 0 violations across %d in scope", name, scope)))
	}
	return nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// rowsNotViolations says whether the rows carry a `violation` column and how many of the rows read are not true.
func rowsNotViolations(items []forward.NQERecord) (has bool, not int) {
	for _, it := range items {
		raw, ok := it["violation"]
		if !ok {
			continue
		}
		has = true
		if strings.TrimSpace(string(raw)) != "true" {
			not++
		}
	}
	return has, not
}
