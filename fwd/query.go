package fwd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	forward "github.com/forwardnetworks/forward-go-sdk"
)

// DefaultNQELimit is the row cap for one NQE run.
const DefaultNQELimit = 200

// ErrSnapshotNotReady means no rows were returned because the snapshot is not usable. It is NOT zero
// rows and must never be reported as a pass.
var ErrSnapshotNotReady = errors.New("snapshot is not ready; no results were returned")

// NQERun is one NQE query.
type NQERun struct {
	Query, QueryID string
	// CommitID pins a saved query (QueryID) to a commit of the library; empty is the head.
	CommitID   string
	Parameters map[string]any
	SnapshotID string
	Limit      int
	Offset     int
}

// NQEOutcome is what a run returned.
type NQEOutcome struct {
	Items             []forward.NQERecord
	Total             int64
	Truncated         bool
	PredictedSnapshot bool
}

// RunNQE runs a query against a processed snapshot. It refuses (ErrSnapshotNotReady) to run against an
// unprocessed one, so an unready snapshot can never surface as an empty result, and it maps Forward's own
// not-ready refusal the same way. PredictedSnapshot marks results read from a Predict snapshot, where an
// empty result means nothing.
func (s *Session) RunNQE(ctx context.Context, networkID string, r NQERun) (NQEOutcome, error) {
	if (r.Query == "") == (r.QueryID == "") {
		return NQEOutcome{}, errors.New("pass exactly one of query or queryId")
	}
	snap, err := s.Snapshot(ctx, networkID, r.SnapshotID)
	if err != nil {
		return NQEOutcome{}, err
	}
	if !IsReady(snap) {
		return NQEOutcome{}, fmt.Errorf("snapshot %s is %s: %w", r.SnapshotID, StateOf(snap), ErrSnapshotNotReady)
	}
	limit := r.Limit
	if limit <= 0 {
		limit = DefaultNQELimit
	}
	lim := int32(limit)
	res, _, err := s.Client.NQE.Run(ctx, networkID, r.SnapshotID, forward.NQEQueryRequest{
		Query: r.Query, QueryID: r.QueryID, CommitID: r.CommitID, Parameters: r.Parameters,
		Options: nqeOptions(&lim, r.Offset),
	})
	if err != nil {
		// Forward refuses NQE on a snapshot it cannot read; the SDK classifies every such refusal, on any route.
		// None of them is zero rows.
		if errors.Is(err, forward.ErrSnapshotNotProcessed) || errors.Is(err, forward.ErrSnapshotProcessingFailed) ||
			errors.Is(err, forward.ErrNoSnapshots) {
			return NQEOutcome{}, fmt.Errorf("snapshot %s: %w: %v", r.SnapshotID, ErrSnapshotNotReady, err)
		}
		return NQEOutcome{}, err
	}
	out := NQEOutcome{Items: res.Items, Total: res.TotalNumItems, PredictedSnapshot: IsPredicted(snap)}
	if out.Total < int64(len(out.Items)) {
		out.Total = int64(len(out.Items))
	}
	out.Truncated = out.Total > int64(len(out.Items))
	s.Annotate(intp(len(out.Items)), out.Truncated)
	return out, nil
}

// QueryDiagnostic is one compile/type error Forward reported, with its 0-based position.
type QueryDiagnostic struct {
	Message string `json:"message"`
	Line    *int32 `json:"line"`
	Column  *int32 `json:"column"`
}

// QueryErrors extracts Forward's diagnostics when err is a rejected NQE query. ok is false for any other
// error (a transport failure, an auth failure), which is not a finding about the query.
func QueryErrors(err error) (diags []QueryDiagnostic, message string, ok bool) {
	var er *forward.ErrorResponse
	if !errors.As(err, &er) || er.Response == nil || er.Response.StatusCode != 400 {
		return nil, "", false
	}
	if len(er.Errors) == 0 && er.CompletionType == "" {
		return nil, "", false
	}
	for _, e := range er.Errors {
		d := QueryDiagnostic{Message: e.Message}
		if e.Location != nil && e.Location.Start != nil {
			line, col := e.Location.Start.Line, e.Location.Start.Character
			d.Line, d.Column = &line, &col
		}
		diags = append(diags, d)
	}
	return diags, er.Message, true
}

func nqeOptions(limit *int32, offset int) *forward.NQEOptions {
	o := &forward.NQEOptions{Limit: limit}
	if offset > 0 {
		off := int32(offset)
		o.Offset = &off
	}
	return o
}

// Records turns NQE result rows into plain maps a result envelope can carry.
func Records(items []forward.NQERecord) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		row := make(map[string]any, len(it))
		for k, v := range it {
			var x any
			if err := json.Unmarshal(v, &x); err != nil {
				x = string(v)
			}
			row[k] = x
		}
		out = append(out, row)
	}
	return out
}

// NQEDiff is a committed query's row differences between two snapshots.
type NQEDiff struct {
	Rows  []forward.NQEDiffEntry
	Total int32
}

// DiffNQE compares a committed (library) query's results across two snapshots. Only committed queries can be
// diffed; Forward has no route to diff ad-hoc query text.
func (s *Session) DiffNQE(ctx context.Context, beforeID, afterID, queryID, commitID string, limit int32) (NQEDiff, error) {
	req := forward.NQEDiffRequest{QueryID: queryID, CommitID: commitID}
	if limit > 0 {
		req.Options = &forward.NQEOptions{Limit: &limit}
	}
	res, _, err := s.Client.NQE.Diff(ctx, beforeID, afterID, req)
	if err != nil {
		return NQEDiff{}, err
	}
	s.Annotate(intp(len(res.Rows)), int32(len(res.Rows)) < res.TotalNumRows)
	return NQEDiff{Rows: res.Rows, Total: res.TotalNumRows}, nil
}

// NQELibrary lists the committed NQE queries, optionally under one directory.
func (s *Session) NQELibrary(ctx context.Context, dir string) ([]forward.NQEQuery, error) {
	qs, _, err := s.Client.NQE.ListQueries(ctx, dir)
	if err == nil {
		s.Annotate(intp(len(qs)), false)
	}
	return qs, err
}
