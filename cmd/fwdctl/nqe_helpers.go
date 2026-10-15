package main

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/forwardnetworks/fwdctl/fwd"
)

var queryIDShape = regexp.MustCompile(`^(F?Q)_([0-9a-fA-F]+)$`)

// queryIDProblem catches a mistyped query id before any request: a library query id is Q_ (or FQ_ for Forward's library) followed by exactly 40 hex digits, a SHA-1. A value that is not hex at all
// (a placeholder, an id of another form) is left to Forward to judge.
func queryIDProblem(id string) string {
	m := queryIDShape.FindStringSubmatch(strings.TrimSpace(id))
	if m == nil || len(m[2]) == 40 || len(m[2]) < 30 || len(m[2]) > 50 { // far from 40 digits is a placeholder, not a typo: left to Forward
		return ""
	}
	return fmt.Sprintf("query id %q has %d hex digits after %s_; a library query id has exactly 40", id, len(m[2]), m[1])
}

// cachedFailureNote says so when a failed execution came back much faster than the time Forward recorded for it: Forward caches a failure with its result, so the identical request cannot succeed.
func cachedFailureNote(m fwd.NQEMeta, wall time.Duration) string {
	o := strings.ToUpper(m.Outcome)
	if m.MillisExecuting == nil || *m.MillisExecuting < 2000 || o == "" || o == "OK" || o == "SUCCEEDED" {
		return ""
	}
	if wall.Milliseconds()*10 >= *m.MillisExecuting*9 {
		return ""
	}
	return "cached failure: Forward returned this " + m.Outcome + " from its cache without running the query again, so repeating the same request cannot succeed. Change the query text or its parameters (fwdctl nqe run --fresh adds an always-true filter that does it without changing the rows) and narrow the query; after a SYSTEM_ERROR wait for the analysis backlog to clear"
}

// inlineSizeWarning is said for a very large query sent as text: Forward analyses an inline query again on every run.
func inlineSizeWarning(bytes int) string {
	if bytes < 100*1024 {
		return ""
	}
	return fmt.Sprintf("this query is %d KB sent inline: Forward analyses an inline query again on every cold run and a very large one can back up the analysis queue. Save it to the NQE library and run it with --query-id (the analysis is cached by query, snapshot and parameters)", bytes/1024)
}

// estimateTotalBytes is the size, as text, a whole result would be, extrapolated from the rows of one page: what a job needs to decide to page, or to pass --allow-large, before paying for the full run.
func estimateTotalBytes(page []map[string]any, total int64) int64 {
	if len(page) == 0 {
		return 0
	}
	if total <= int64(len(page)) {
		return estimateRowsBytes(page)
	}
	return estimateRowsBytes(page) / int64(len(page)) * total
}
