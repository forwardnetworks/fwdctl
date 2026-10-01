package skills

import (
	"context"
	"encoding/json"
	"fmt"
	forward "github.com/forwardnetworks/forward-go-sdk"
	"sort"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const compareNQEName = "compare-nqe-results"

func init() { Register(compareNQEName, compareNQE) }

type compareNQEInput struct {
	NetworkID        string `json:"network_id"`
	BeforeSnapshotID string `json:"before_snapshot_id"`
	AfterSnapshotID  string `json:"after_snapshot_id"`
	QueryID          string `json:"query_id"`
	CommitID         string `json:"commit_id"`
	Limit            int    `json:"limit"`
}

const (
	defaultDiffRows = 25
	maxDiffRows     = 100
)

func compareNQE(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in compareNQEInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if in.Limit <= 0 {
		in.Limit = defaultDiffRows
	}
	in.Limit = min(in.Limit, maxDiffRows)
	before, err := s.Snapshot(ctx, in.NetworkID, in.BeforeSnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	after, err := s.Snapshot(ctx, in.NetworkID, in.AfterSnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	cx := fwd.Context(in.NetworkID, after)
	if !fwd.IsReady(before) || !fwd.IsReady(after) {
		return result.NewUnknown(compareNQEName, "Both snapshots must be processed to compare them", cx,
			[]string{fmt.Sprintf("before snapshot is %s, after snapshot is %s; nothing was compared", fwd.StateOf(before), fwd.StateOf(after))},
			result.Options{NextActions: []string{"investigate-collection-failure"}})
	}
	var limits []string
	if fwd.IsPredicted(before) || fwd.IsPredicted(after) {
		limits = append(limits, "one snapshot is a prediction: the difference is between a collected state and a prediction, not two collected states")
	}
	d, err := s.DiffNQE(ctx, in.BeforeSnapshotID, in.AfterSnapshotID, in.QueryID, in.CommitID, int32(in.Limit))
	if fwd.NotFound(err) {
		return result.NewUnknown(compareNQEName, fmt.Sprintf("Forward has no committed query %q to compare", in.QueryID), cx,
			append(limits, "only queries committed to the NQE library can be compared; use find-nqe-query to get an id"), result.Options{NextActions: []string{"find-nqe-query"}})
	}
	if err != nil {
		return result.Result{}, err
	}
	sid := fwd.SnapshotIDPtr(after)
	if len(d.Rows) == 0 {
		// A query that returns nothing in either snapshot differs by nothing. Check that it returns rows, or the
		// comparison proved nothing.
		out, err := s.RunNQE(ctx, in.NetworkID, fwd.NQERun{QueryID: in.QueryID, SnapshotID: in.AfterSnapshotID, Limit: 1})
		if err != nil {
			return result.Result{}, err
		}
		if len(out.Items) == 0 {
			return result.NewUnknown(compareNQEName, "The query returns no rows in either snapshot, so the comparison shows nothing", cx,
				append(limits, "zero differences over an empty result is not evidence the network is unchanged"), result.Options{NextActions: []string{"validate-nqe-query"}})
		}
		return result.Build(compareNQEName, result.OK, "No differences in this query's results between the two snapshots", result.Deterministic, cx,
			result.Options{Limits: limits, NextActions: []string{"verify-change"},
				Evidence: []result.Evidence{result.NewEvidence(result.EvNQE, "nqeDiff", sid, map[string]any{
					"query_id": in.QueryID, "before": in.BeforeSnapshotID, "after": in.AfterSnapshotID, "differences": 0}, "0 differences over a non-empty result")}})
	}
	byType := map[string]int{}
	rows := make([]map[string]any, 0, len(d.Rows))
	for _, r := range d.Rows {
		byType[r.Type]++
		row := map[string]any{"type": r.Type}
		if r.Before != nil {
			row["before"] = fwd.Records([]forward.NQERecord{*r.Before})[0]
		}
		if r.After != nil {
			row["after"] = fwd.Records([]forward.NQERecord{*r.After})[0]
		}
		rows = append(rows, row)
	}
	types := make([]string, 0, len(byType))
	for t := range byType {
		types = append(types, t)
	}
	sort.Strings(types)
	counts := ""
	for i, t := range types {
		if i > 0 {
			counts += ", "
		}
		counts += fmt.Sprintf("%d %s", byType[t], t)
	}
	if int(d.Total) > len(rows) {
		limits = append(limits, fmt.Sprintf("%d differences in total; the first %d are shown, so the counts by type cover only those (raise limit, at most %d)", d.Total, len(rows), maxDiffRows))
	}
	return result.Build(compareNQEName, result.OK, fmt.Sprintf("%d difference(s) between the snapshots (%s)", d.Total, counts), result.Deterministic, cx,
		result.Options{Limits: limits, NextActions: []string{"verify-change", "verify-change"},
			Evidence: []result.Evidence{result.NewEvidence(result.EvNQE, "nqeDiff", sid, map[string]any{
				"query_id": in.QueryID, "before": in.BeforeSnapshotID, "after": in.AfterSnapshotID, "total": d.Total, "by_type": byType, "rows": rows},
				fmt.Sprintf("%d differences", d.Total))}})
}
