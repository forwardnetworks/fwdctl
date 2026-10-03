package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/nqelint"
	"github.com/forwardnetworks/fwdctl/nqeschema"
	"github.com/forwardnetworks/fwdctl/result"
)

const validateNQEName = "validate-nqe-query"

func init() { Register(validateNQEName, validateNQEQuery) }

type validateNQEInput struct {
	NetworkID  string         `json:"network_id"`
	Query      string         `json:"query"`
	SnapshotID string         `json:"snapshot_id"`
	Parameters map[string]any `json:"parameters"`
	SampleRows int            `json:"sample_rows"`
	Offset     int            `json:"offset"`
	// SyntheticKind names a synthetic device kind (internet, intranet, l3vpn, adjacent-network, l2vpn): the query is also checked as the connections of that kind
	// (offline row type, vlan 0), and the rows are the connections Forward would generate. Nothing is saved or attached.
	SyntheticKind string `json:"synthetic_kind"`
}

const maxNQERows = 200

func validateNQEQuery(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in validateNQEInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	snap, err := resolveSnapshot(ctx, s, in.NetworkID, in.SnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	cx := fwd.Context(in.NetworkID, snap)
	if !fwd.IsReady(snap) {
		return result.NewUnknown(validateNQEName, "No processed snapshot is available to run the query against", cx,
			[]string{"no processed snapshot; the query was not run"},
			result.Options{NextActions: []string{"investigate-collection-failure"}})
	}
	sid := fwd.SnapshotIDPtr(snap)
	sample := in.SampleRows
	if sample <= 0 {
		sample = 5
	}
	// Rows cost tokens (about 130 each), so the cap is modest; page with offset to read a larger result.
	sample = min(sample, maxNQERows)
	out, err := s.RunNQE(ctx, in.NetworkID, fwd.NQERun{Query: in.Query, Parameters: in.Parameters,
		SnapshotID: fwd.SnapshotID(cx), Limit: sample, Offset: in.Offset})
	if errors.Is(err, fwd.ErrSnapshotNotReady) {
		return result.NewUnknown(validateNQEName, "The snapshot became unavailable; the query was not run", cx,
			[]string{err.Error()}, result.Options{})
	}
	if diags, msg, ok := fwd.QueryErrors(err); ok {
		diags = fwd.AnnotateDiagnostics(in.Query, diags)
		detail := map[string]any{"message": msg, "diagnostics": diags}
		if offline := nqelint.Lint(in.Query); len(offline) > 0 {
			detail["offline_lint"] = offline // the same query read by our own parser: syntax errors and deprecations, with 1-based positions
		}
		hints := schemaHints(in.Query)
		if len(hints) > 0 {
			detail["schema_hints"] = hints
		}
		reason := msg
		where := ""
		if len(diags) > 0 {
			if diags[0].Message != "" {
				reason = diags[0].Message
			}
			if diags[0].Line != nil && diags[0].Column != nil {
				where = fmt.Sprintf(" at line %d, column %d (0-based)", *diags[0].Line, *diags[0].Column)
			}
		}
		for _, h := range hints {
			if h.Kind == "unknown_enum_value" && len(h.Suggestion) > 0 {
				where += "; " + h.Text + " is not a value, did you mean " + strings.Split(h.Text, ".")[0] + "." + h.Suggestion[0] + "?"
				break
			}
		}
		return result.Build(validateNQEName, result.Failed, "The query does not compile: "+reason+where,
			result.Deterministic, cx, result.Options{Evidence: []result.Evidence{
				result.NewEvidence(result.EvNQE, "runNqeQuery", sid, detail, fmt.Sprintf("%d diagnostic(s)", len(diags)))}})
	}
	if err != nil {
		return result.Result{}, err
	}
	var limits []string
	var synth []nqelint.Diagnostic
	if in.SyntheticKind != "" {
		var serr error
		if synth, serr = nqelint.CheckSyntheticRows(in.Query, in.SyntheticKind); serr != nil {
			return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, serr)
		}
		if !nqelint.CorporaPresent() {
			limits = append(limits, "the synthetic-device row check was NOT done: it needs the NQE data model, which this build does not carry (an official release build does); the query was only run")
		}
		limits = append(limits, "checked as the connections of a "+in.SyntheticKind+" node: this runs the unsaved source on the snapshot and checks its row type offline; Forward's own compute (which also catches a bad site name or empty subnets with discovery none) needs the query committed to the library")
		for _, d := range synth {
			limits = append(limits, fmt.Sprintf("synthetic-device row problem at line %d: %s", d.Line, d.Message))
		}
	}
	for _, d := range nqelint.Lint(in.Query) {
		if d.Severity == "warning" {
			limits = append(limits, fmt.Sprintf("the query runs, but at line %d, column %d: %s", d.Line, d.Column, d.Message))
		}
	}
	for _, h := range schemaHints(in.Query) {
		if h.Kind == "discouraged_filter" {
			limits = append(limits, "the query runs, but "+h.Message)
		}
	}
	if out.PredictedSnapshot {
		limits = append(limits, "run against a predicted snapshot; some signals are empty there regardless of the network")
	}
	if out.Total == 0 {
		limits = append(limits, "the query returned zero rows; it may be correct and find nothing, or it may filter on the "+
			"wrong thing -- this skill cannot tell which")
		return result.NewUnknown(validateNQEName, "The query runs but returned no rows", cx, limits, result.Options{})
	}
	rows := out.Items
	if len(rows) > sample {
		rows = rows[:sample]
	}
	if len(rows) == 0 {
		limits = append(limits, fmt.Sprintf("offset %d is past the end of the %d rows", in.Offset, out.Total))
		return result.NewUnknown(validateNQEName, fmt.Sprintf("Offset %d is beyond the query's %d rows", in.Offset, out.Total), cx, limits, result.Options{})
	}
	if end := int64(in.Offset + len(rows)); end < out.Total {
		limits = append(limits, fmt.Sprintf("%d rows in total; rows %d-%d shown. Page with offset=%d (sample_rows up to %d).", out.Total, in.Offset+1, end, end, maxNQERows))
	}
	status, finding := result.OK, fmt.Sprintf("The query runs and returned %d row(s)", out.Total)
	if len(synth) > 0 {
		status, finding = result.Failed, fmt.Sprintf("The query runs and returned %d row(s), but they are not valid %s connections: %s", out.Total, in.SyntheticKind, synth[0].Message)
	}
	return result.Build(validateNQEName, status, finding,
		result.Deterministic, cx, result.Options{Limits: limits, NextActions: []string{"check-network-compliance"},
			Evidence: []result.Evidence{result.NewEvidence(result.EvNQE, "runNqeQuery", sid,
				map[string]any{"rows": out.Total, "offset": in.Offset, "sample": fwd.Records(rows)}, fmt.Sprintf("%d row(s)", out.Total))}})
}

// schemaHints runs the static schema check. A schema that cannot load is not the query's problem, so it
// yields no hints rather than an error.
func schemaHints(query string) []nqeschema.Finding {
	m, err := nqeschema.Load()
	if err != nil {
		return nil
	}
	return m.Check(query)
}
