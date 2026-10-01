package fwd

import "context"

// maxNQEPage is the page size RunNQEAll asks Forward for.
const maxNQEPage = 1000

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
