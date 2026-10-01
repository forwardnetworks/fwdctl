package fwd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	forward "github.com/forwardnetworks/forward-go-sdk"
)

// NQEMeta is what a run knew about itself beyond its rows: the execution Forward gave it (async runs), its outcome and timing, and, for a run that failed,
// the HTTP status and the diagnostics Forward returned, so a caller can tell a transport or capacity failure (HTTP 503) from an outcome of the query itself.
type NQEMeta struct {
	Mode            string `json:"mode"` // sync | async
	ExecutionKey    string `json:"execution_key,omitempty"`
	Status          string `json:"status,omitempty"`
	Outcome         string `json:"outcome,omitempty"`
	CompletionType  string `json:"completion_type,omitempty"`
	MillisExecuting *int64 `json:"millis_executing,omitempty"`
	RowsProduced    *int64 `json:"rows_produced,omitempty"`
	// LikelyCached says the wall time was far below the execution time Forward recorded: the result was served from its cache, so the timing is not a cold measurement.
	LikelyCached bool              `json:"likely_cached,omitempty"`
	HTTPStatus   int               `json:"http_status,omitempty"`
	Diagnostics  []QueryDiagnostic `json:"diagnostics"`
	Error        string            `json:"error,omitempty"`
}

// MetaFromError fills the failure part of a meta from an error: the HTTP status, Forward's completion type and its diagnostics (never nil, so a consumer can test the list's length).
func MetaFromError(m *NQEMeta, err error) {
	if err == nil {
		return
	}
	m.Error = err.Error()
	var er *forward.ErrorResponse
	if errors.As(err, &er) {
		if er.Response != nil {
			m.HTTPStatus = er.Response.StatusCode
		}
		m.CompletionType = er.CompletionType
	}
	if d, _, ok := QueryErrors(err); ok {
		m.Diagnostics = d
	}
}

// RunNQEAsync runs a query through Forward's asynchronous execution API, waits for it and reads every row (up to maxRows). It returns the execution key,
// outcome and Forward's own execution time with the rows. timeout bounds the wait (0 means 10 minutes).
func (s *Session) RunNQEAsync(ctx context.Context, networkID string, r NQERun, maxRows int, timeout time.Duration) ([]map[string]any, int64, NQEMeta, error) {
	meta := NQEMeta{Mode: "async", Diagnostics: []QueryDiagnostic{}}
	if (r.Query == "") == (r.QueryID == "") {
		return nil, 0, meta, errors.New("pass exactly one of query or queryId")
	}
	snap, err := s.Snapshot(ctx, networkID, r.SnapshotID)
	if err != nil {
		return nil, 0, meta, err
	}
	if !IsReady(snap) {
		return nil, 0, meta, fmt.Errorf("snapshot %s is %s: %w", r.SnapshotID, StateOf(snap), ErrSnapshotNotReady)
	}
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ex, _, err := s.Client.NQE.Start(ctx, networkID, r.SnapshotID, forward.NQEExecutionRequest{Query: r.Query, QueryID: r.QueryID, CommitID: r.CommitID, Parameters: r.Parameters})
	if err != nil {
		MetaFromError(&meta, err)
		return nil, 0, meta, err
	}
	meta.ExecutionKey = ex.ExecutionKey
	for {
		cur, _, serr := s.Client.NQE.Status(ctx, networkID, meta.ExecutionKey)
		if serr != nil {
			MetaFromError(&meta, serr)
			return nil, 0, meta, serr
		}
		meta.Status, meta.Outcome, meta.MillisExecuting, meta.RowsProduced = cur.Status, cur.Outcome, cur.MillisExecuting, cur.RowsProduced
		switch strings.ToUpper(cur.Status) {
		case "COMPLETED":
			if cur.Outcome != "" && !strings.EqualFold(cur.Outcome, "OK") && !strings.EqualFold(cur.Outcome, "SUCCEEDED") {
				if cur.Error != nil {
					meta.Diagnostics = append(meta.Diagnostics, QueryDiagnostic{Message: cur.Error.Message})
				}
				err := fmt.Errorf("the execution %s completed with outcome %s", meta.ExecutionKey, cur.Outcome)
				meta.Error = err.Error()
				return nil, 0, meta, err
			}
			return s.readExecution(ctx, networkID, meta, maxRows)
		case "FAILED", "CANCELED", "TIMED_OUT":
			if cur.Error != nil {
				meta.Diagnostics = append(meta.Diagnostics, QueryDiagnostic{Message: cur.Error.Message})
			}
			err := fmt.Errorf("the execution %s ended %s", meta.ExecutionKey, cur.Status)
			meta.Error = err.Error()
			return nil, 0, meta, err
		}
		select {
		case <-ctx.Done():
			err := fmt.Errorf("the execution %s did not finish within %s: %w", meta.ExecutionKey, timeout, ctx.Err())
			meta.Error = err.Error()
			return nil, 0, meta, err
		case <-time.After(1 * time.Second):
		}
	}
}

func (s *Session) readExecution(ctx context.Context, networkID string, meta NQEMeta, maxRows int) ([]map[string]any, int64, NQEMeta, error) {
	if maxRows <= 0 {
		maxRows = 50000
	}
	var rows []map[string]any
	var total int64
	for len(rows) < maxRows {
		lim, off := int32(min(maxNQEPage, maxRows-len(rows))), int32(len(rows))
		page, _, err := s.Client.NQE.Result(ctx, networkID, meta.ExecutionKey, forward.NQEResultOptions{Limit: &lim, Offset: &off})
		if err != nil {
			MetaFromError(&meta, err)
			return rows, total, meta, err
		}
		total = page.TotalNumItems
		rows = append(rows, Records(page.Items)...)
		if len(page.Items) == 0 || int64(len(rows)) >= total {
			break
		}
	}
	return rows, total, meta, nil
}
