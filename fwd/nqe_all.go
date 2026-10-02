package fwd

import "context"

// maxNQEPage is the page size RunNQEAll asks Forward for: Forward's own hard cap (a larger limit is refused, "'limit' cannot exceed
// 10000"). Measured against a 38,000-device network: a page's own cost is roughly flat whether it holds 1,000 or 10,000 rows (around
// half a second either way, dominated by fixed per-request cost, not row count), so the largest page Forward allows cuts the number of
// round trips, and so the wall-clock time, by roughly 10x on a result large enough to need more than one page.
const maxNQEPage = 10000

// RunNQEAll runs a query and pages through every row, up to maxRows (0 means 50,000). truncated says the result held more than maxRows. Each page is a normal RunNQE,
// so an unready snapshot is still an error and never an empty result.
func (s *Session) RunNQEAll(ctx context.Context, networkID, snapshotID, query string, maxRows int) (rows []map[string]any, total int64, truncated bool, err error) {
	return s.RunNQEAllWith(ctx, networkID, NQERun{Query: query, SnapshotID: snapshotID}, maxRows)
}

// RunNQEAllWith is RunNQEAll for any run: query text or a saved query id (optionally at a commit), with parameters. Limit and Offset of base are ignored.
func (s *Session) RunNQEAllWith(ctx context.Context, networkID string, base NQERun, maxRows int) (rows []map[string]any, total int64, truncated bool, err error) {
	if maxRows <= 0 {
		maxRows = 50000
	}
	for offset := 0; offset < maxRows; {
		page := min(maxNQEPage, maxRows-offset)
		run := base
		run.Limit, run.Offset = page, offset
		out, err := s.RunNQE(ctx, networkID, run)
		if err != nil {
			return nil, 0, false, err
		}
		total = out.Total
		rows = append(rows, Records(out.Items)...)
		offset += len(out.Items)
		if len(out.Items) == 0 || int64(offset) >= out.Total {
			return rows, total, false, nil
		}
	}
	return rows, total, int64(len(rows)) < total, nil
}
