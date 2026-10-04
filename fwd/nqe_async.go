package fwd

import (
	"context"
	"errors"
	"fmt"
	"net"
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
	LikelyCached bool `json:"likely_cached,omitempty"`
	// Attempts is how many times the run was tried when --retry-transient allowed more than one; TransportClean is false when a retry was needed (a Forward restart or a gateway failure).
	Attempts       int               `json:"attempts,omitempty"`
	TransportClean *bool             `json:"transport_clean,omitempty"`
	HTTPStatus     int               `json:"http_status,omitempty"`
	Diagnostics    []QueryDiagnostic `json:"diagnostics"`
	Error          string            `json:"error,omitempty"`
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
	ctx, cancel, meta, err := s.startAndWait(ctx, networkID, r, timeout)
	if cancel != nil {
		defer cancel()
	}
	if err != nil {
		return nil, 0, meta, err
	}
	return s.readExecution(ctx, networkID, meta, maxRows)
}

// startAndWait starts an asynchronous execution and waits until it has completed OK. The returned context carries the wait's deadline and its cancel func must be called.
func (s *Session) startAndWait(ctx context.Context, networkID string, r NQERun, timeout time.Duration) (context.Context, context.CancelFunc, NQEMeta, error) {
	meta := NQEMeta{Mode: "async", Diagnostics: []QueryDiagnostic{}}
	if (r.Query == "") == (r.QueryID == "") {
		return ctx, nil, meta, errors.New("pass exactly one of query or queryId")
	}
	snap, err := s.Snapshot(ctx, networkID, r.SnapshotID)
	if err != nil {
		return ctx, nil, meta, err
	}
	if !IsReady(snap) {
		return ctx, nil, meta, fmt.Errorf("snapshot %s is %s: %w", r.SnapshotID, StateOf(snap), ErrSnapshotNotReady)
	}
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	ex, _, err := s.Client.NQE.Start(ctx, networkID, r.SnapshotID, forward.NQEExecutionRequest{Query: r.Query, QueryID: r.QueryID, CommitID: r.CommitID, Parameters: r.Parameters})
	if err != nil {
		MetaFromError(&meta, err)
		return ctx, cancel, meta, err
	}
	meta.ExecutionKey = ex.ExecutionKey
	for {
		cur, _, serr := s.Client.NQE.Status(ctx, networkID, meta.ExecutionKey)
		if serr != nil {
			MetaFromError(&meta, serr)
			return ctx, cancel, meta, serr
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
				return ctx, cancel, meta, err
			}
			return ctx, cancel, meta, nil
		case "FAILED", "CANCELED", "TIMED_OUT":
			if cur.Error != nil {
				meta.Diagnostics = append(meta.Diagnostics, QueryDiagnostic{Message: cur.Error.Message})
			}
			err := fmt.Errorf("the execution %s ended %s", meta.ExecutionKey, cur.Status)
			meta.Error = err.Error()
			return ctx, cancel, meta, err
		}
		select {
		case <-ctx.Done():
			err := fmt.Errorf("the execution %s did not finish within %s: %w", meta.ExecutionKey, timeout, ctx.Err())
			meta.Error = err.Error()
			return ctx, cancel, meta, err
		case <-time.After(1 * time.Second):
		}
	}
}

// runNQEPageAsync reads one page of a query through the asynchronous execution API. RunNQE falls back to it when the synchronous call is cut off by the HTTP timeout: the
// query then runs (or, if the first attempt finished meanwhile, is served from Forward's cache) without holding one request open.
func (s *Session) runNQEPageAsync(ctx context.Context, networkID string, r NQERun, limit int) ([]forward.NQERecord, int64, error) {
	items, total, _, err := s.runNQEPageAsyncMeta(ctx, networkID, r, limit, s.NQEWait)
	return items, total, err
}

// runNQEPageAsyncMeta is runNQEPageAsync that also returns the execution's meta (key, outcome, Forward's execution time).
func (s *Session) runNQEPageAsyncMeta(ctx context.Context, networkID string, r NQERun, limit int, timeout time.Duration) ([]forward.NQERecord, int64, NQEMeta, error) {
	ctx, cancel, meta, err := s.startAndWait(ctx, networkID, r, timeout)
	if cancel != nil {
		defer cancel()
	}
	if err != nil {
		return nil, 0, meta, err
	}
	lim, off := int32(limit), int32(r.Offset)
	page, _, err := s.Client.NQE.Result(ctx, networkID, meta.ExecutionKey, forward.NQEResultOptions{Limit: &lim, Offset: &off})
	if err != nil {
		MetaFromError(&meta, err)
		return nil, 0, meta, err
	}
	return page.Items, page.TotalNumItems, meta, nil
}

// RunNQEPage reads ONE page (limit rows from offset) of a query, synchronously or, with async, through the execution API (timeout bounds the wait). It returns the rows, the
// total Forward reports and, for async, the execution meta. Paging the same query twice with consecutive offsets is how a caller checks the pages join into the full result.
func (s *Session) RunNQEPage(ctx context.Context, networkID string, r NQERun, limit, offset int, async bool, timeout time.Duration) ([]map[string]any, int64, NQEMeta, error) {
	r.Limit, r.Offset = limit, offset
	if async {
		snap, err := s.Snapshot(ctx, networkID, r.SnapshotID)
		if err != nil {
			return nil, 0, NQEMeta{Mode: "async", Diagnostics: []QueryDiagnostic{}}, err
		}
		if !IsReady(snap) {
			return nil, 0, NQEMeta{Mode: "async", Diagnostics: []QueryDiagnostic{}}, fmt.Errorf("snapshot %s is %s: %w", r.SnapshotID, StateOf(snap), ErrSnapshotNotReady)
		}
		items, total, meta, err := s.runNQEPageAsyncMeta(ctx, networkID, r, limit, timeout)
		return Records(items), total, meta, err
	}
	out, err := s.RunNQE(ctx, networkID, r)
	if err != nil {
		return nil, 0, NQEMeta{Mode: "sync", Diagnostics: []QueryDiagnostic{}}, err
	}
	return Records(out.Items), out.Total, NQEMeta{Mode: "sync", Diagnostics: []QueryDiagnostic{}}, nil
}

// isClientTimeout reports whether err is the HTTP client's timeout (or a cancelled deadline) rather than an answer from Forward.
func isClientTimeout(err error) bool {
	if err == nil {
		return false
	}
	var ne net.Error
	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) || strings.Contains(err.Error(), "Client.Timeout")
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
