package main

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/forwardnetworks/fwdctl/fwd"
)

var queryIDShape = regexp.MustCompile(`^(F?Q)_([0-9A-Za-z]+)$`)

// queryIDProblem catches a mistyped query id before any request: a library query id is Q_ (or FQ_ for Forward's library) followed by exactly 40 hex digits, a SHA-1. A value far from 40 characters (a
// placeholder, an id of another form) is left to Forward to judge.
func queryIDProblem(id string) string {
	m := queryIDShape.FindStringSubmatch(strings.TrimSpace(id))
	if m == nil || len(m[2]) < 30 || len(m[2]) > 50 {
		return ""
	}
	if len(m[2]) != 40 {
		return fmt.Sprintf("query id %q has %d hex digits after %s_; a library query id has exactly 40", id, len(m[2]), m[1])
	}
	for i, r := range m[2] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return fmt.Sprintf("query id %q has %q at position %d after %s_, which is not a hex digit", id, string(r), i+1, m[1])
		}
	}
	return ""
}

// commitIDProblem is the same check for a full commit id (a SHA-1: 40 hex digits). A short prefix is left to Forward.
func commitIDProblem(id string) string {
	id = strings.TrimSpace(id)
	if len(id) < 30 || len(id) > 50 {
		return ""
	}
	if len(id) != 40 {
		return fmt.Sprintf("commit id %q has %d characters; a full commit id is a SHA-1 of exactly 40 hex digits", id, len(id))
	}
	for i, r := range id {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return fmt.Sprintf("commit id %q has %q at position %d, which is not a hex digit", id, string(r), i+1)
		}
	}
	return ""
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

// idsProblem checks the query id and commit id a command was given, before any request.
func idsProblem(queryID, commitID string) string {
	if queryID != "" {
		if p := queryIDProblem(queryID); p != "" {
			return p
		}
	}
	return commitIDProblem(commitID)
}
