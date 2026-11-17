package skills

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// The library-wide reads of find-nqe-query: a recursive listing with an intent filter (a customer keeps categories at the end of a query's
// @intent text as a stand-in for tags), and a search of the query SOURCES for a pattern (what breaks when a field changes). Read-only.

const (
	maxRecursiveRows = 200 // queries shown by a recursive listing
	maxSourceReads   = 300 // query sources read by one source search (each is one request)
	maxSourceRows    = 100 // queries shown by a source search
	maxLinesPerQuery = 3   // matching lines shown per query
	sourceReadPar    = 8
	maxLineChars     = 160
)

// compileFindRegex compiles a user pattern (Go RE2 syntax) and says which input was wrong. Case-sensitive unless it starts with (?i).
func compileFindRegex(input, text string) (*regexp.Regexp, error) {
	if len(text) > 500 {
		return nil, fmt.Errorf("%w: %s is at most 500 characters", ErrInvalidInput, input)
	}
	re, err := regexp.Compile(text)
	if err != nil {
		return nil, fmt.Errorf("%w: %s is not a valid regular expression (Go RE2 syntax; escape a literal dot as \\.): %v", ErrInvalidInput, input, err)
	}
	return re, nil
}

// libraryUnder returns the library's queries whose path is under dir (any depth), sorted by path, optionally keeping only those whose intent matches.
func libraryUnder(ctx context.Context, s *fwd.Session, dir string, intent *regexp.Regexp) (all, kept []forward.NQEQuery, err error) {
	// the whole library is asked for and filtered here by path prefix: how Forward treats ?dir= (one level or all) is not relied on
	lib, err := s.NQELibrary(ctx, "")
	if err != nil {
		return nil, nil, err
	}
	for _, q := range lib {
		if !strings.HasPrefix(strings.TrimSpace(q.Path), dir) {
			continue
		}
		all = append(all, q)
		if intent == nil || intent.MatchString(q.Intent) {
			kept = append(kept, q)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].Path < kept[j].Path })
	return all, kept, nil
}

func normalizeFindDir(d string) (string, error) {
	d = strings.TrimSpace(d)
	if d == "" {
		d = "/"
	}
	if !strings.HasPrefix(d, "/") || strings.Contains(d, "..") {
		return "", fmt.Errorf("%w: directory is a library directory starting with / (for example /L3/), or / for the top", ErrInvalidInput)
	}
	if !strings.HasSuffix(d, "/") {
		d += "/"
	}
	return d, nil
}

// listNQERecursive lists every query under in.Directory, at any depth, with its intent, optionally only those whose intent matches in.Intent.
func listNQERecursive(ctx context.Context, s *fwd.Session, in findNQEInput, cx result.Context) (result.Result, error) {
	dir, err := normalizeFindDir(in.Directory)
	if err != nil {
		return result.Result{}, err
	}
	var intent *regexp.Regexp
	if in.Intent != "" {
		if intent, err = compileFindRegex("intent", in.Intent); err != nil {
			return result.Result{}, err
		}
	}
	all, kept, err := libraryUnder(ctx, s, dir, intent)
	if err != nil {
		return result.Result{}, err
	}
	byRepo := map[string]int{}
	rows := make([]map[string]any, 0, len(kept))
	for _, q := range kept {
		byRepo[q.Repository]++
		rows = append(rows, map[string]any{"path": q.Path, "query_id": q.QueryID, "intent": q.Intent, "repository": q.Repository})
	}
	limits := []string{fmt.Sprintf("every query under %s at any depth (%d in all), read from the library listing; the source of a query is not read", dir, len(all))}
	if intent != nil {
		limits = append(limits, fmt.Sprintf("only queries whose intent matches the regular expression %q (case-sensitive unless it starts with (?i); ^\\s*$ is the queries with no intent): %d of %d", in.Intent, len(kept), len(all)))
	}
	shown, omitted := result.Cap(rows, maxRecursiveRows, "queries listed", "narrow directory or intent to see the rest")
	finding := fmt.Sprintf("%d quer%s under %s", len(kept), map[bool]string{true: "y", false: "ies"}[len(kept) == 1], dir)
	if intent != nil {
		finding += fmt.Sprintf(" with an intent matching %q (of %d)", in.Intent, len(all))
	}
	if len(all) == 0 {
		return result.NewUnknown(findNQEName, fmt.Sprintf("The library has no query under %s", dir), cx, append(limits, "no such directory (a directory exists only while a query is in it)"), result.Options{NextActions: []string{"find-nqe-query"}})
	}
	return result.Build(findNQEName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, Omitted: omitted, NextActions: []string{"validate-nqe-query", "edit-nqe-query"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvNQE, "listNqeQueries", nil, map[string]any{"directory": dir, "recursive": true, "intent_filter": nilIfEmpty(in.Intent),
			"queries_in_directory": len(all), "matched": len(kept), "by_repository": byRepo, "queries": shown}, finding)}})
}

// searchNQESources reads the source of the organization's queries under in.Directory (those whose intent matches in.Intent, when given) and lists
// the ones whose source matches in.SourceMatches, with the first matching lines. Each source is one request, so at most maxSourceReads are read;
// when more qualify the search is partial and the answer says so (a partial "none" is unknown, never a pass).
func searchNQESources(ctx context.Context, s *fwd.Session, in findNQEInput, cx result.Context) (result.Result, error) {
	dir, err := normalizeFindDir(in.Directory)
	if err != nil {
		return result.Result{}, err
	}
	pat, err := compileFindRegex("source_matches", in.SourceMatches)
	if err != nil {
		return result.Result{}, err
	}
	var intent *regexp.Regexp
	if in.Intent != "" {
		if intent, err = compileFindRegex("intent", in.Intent); err != nil {
			return result.Result{}, err
		}
	}
	_, kept, err := libraryUnder(ctx, s, dir, intent)
	if err != nil {
		return result.Result{}, err
	}
	var org []forward.NQEQuery
	builtin := 0
	for _, q := range kept {
		if strings.HasPrefix(q.QueryID, "Q_") {
			org = append(org, q)
		} else {
			builtin++
		}
	}
	limits := []string{fmt.Sprintf("searched the source of the organization's saved queries under %s (%d), by a regular expression (Go RE2; case-sensitive unless it starts with (?i); a literal dot is \\.), line by line", dir, len(org))}
	if builtin > 0 {
		limits = append(limits, fmt.Sprintf("%d Forward built-in quer%s under the directory (FQ_ ids) are not read: this skill reads only the organization's own sources", builtin, map[bool]string{true: "y", false: "ies"}[builtin == 1]))
	}
	if len(org) == 0 {
		return result.NewUnknown(findNQEName, fmt.Sprintf("No saved query of the organization under %s qualifies, so no source was searched", dir), cx, limits, result.Options{NextActions: []string{"find-nqe-query"}})
	}
	toRead, omitted := result.Cap(org, maxSourceReads, "query sources read", "narrow directory or intent so the search covers them all")
	commit := in.CommitID
	if commit == "" {
		head, _, herr := s.Client.NQERepository.Head(ctx)
		if herr != nil {
			return result.Result{}, herr
		}
		commit = string(head)
	}
	type outcome struct {
		q     forward.NQEQuery
		lines []map[string]any
		n     int
		err   error
	}
	outs := make([]outcome, len(toRead))
	var wg sync.WaitGroup
	sem := make(chan struct{}, sourceReadPar)
	// a refusal (401, 403, 429, a route Forward does not serve) stops the search: no further source is requested once one is seen
	stopCtx, stop := context.WithCancel(ctx)
	defer stop()
	var refusedFlag atomic.Bool
	isRefusal := func(e error) bool {
		return forward.IsStatus(e, http.StatusUnauthorized) || forward.IsStatus(e, http.StatusForbidden) || forward.IsStatus(e, http.StatusTooManyRequests) || errors.Is(e, forward.ErrEndpointNotServed)
	}
	for i := range toRead {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			acquired := false
			select {
			case sem <- struct{}{}:
				acquired = true
			case <-stopCtx.Done():
			}
			if stopCtx.Err() != nil {
				if acquired {
					<-sem
				}
				outs[i] = outcome{q: toRead[i], err: stopCtx.Err()}
				return
			}
			defer func() { <-sem }()
			o := outcome{q: toRead[i]}
			got, _, gerr := s.Client.NQERepository.GetQueryByID(stopCtx, commit, toRead[i].QueryID)
			if gerr != nil {
				o.err = gerr
				if isRefusal(gerr) {
					refusedFlag.Store(true)
					stop()
				}
				outs[i] = o
				return
			}
			for ln, line := range strings.Split(got.SourceCode, "\n") {
				if pat.MatchString(line) {
					o.n++
					if len(o.lines) < maxLinesPerQuery {
						t := clipChars(strings.TrimSpace(line), maxLineChars)
						o.lines = append(o.lines, map[string]any{"line": ln + 1, "text": t})
					}
				}
			}
			outs[i] = o
		}(i)
	}
	wg.Wait()
	var rows []map[string]any
	failed, refused := 0, false
	var firstErr error
	for _, o := range outs {
		switch {
		case o.err != nil:
			failed++
			if firstErr == nil || (errors.Is(firstErr, context.Canceled) && !errors.Is(o.err, context.Canceled)) {
				firstErr = o.err
			}
			if ctx.Err() != nil {
				refused = true
			}
		case o.n > 0:
			rows = append(rows, map[string]any{"path": o.q.Path, "query_id": o.q.QueryID, "intent": o.q.Intent, "matching_lines": o.n, "lines": o.lines})
		}
	}
	if refusedFlag.Load() {
		refused = true
	}
	read := len(toRead) - failed
	limits = append(limits, fmt.Sprintf("source read at library commit %s", commit))
	partial := len(omitted) > 0 || failed > 0
	if failed > 0 {
		limits = append(limits, fmt.Sprintf("%d query source(s) could not be read (first error: %v), so they were not searched", failed, firstErr))
	}
	if refused {
		return result.NewUnknown(findNQEName, "The library's sources were refused or the request was stopped, so the search is not complete", cx, limits, result.Options{NextActions: []string{"inspect-access"}})
	}
	detail := map[string]any{"directory": dir, "source_matches": in.SourceMatches, "intent_filter": nilIfEmpty(in.Intent), "sources_read": read, "queries_qualifying": len(org), "queries_with_a_match": len(rows)}
	shown, omittedRows := result.Cap(rows, maxSourceRows, "queries with a match", "narrow directory or the pattern")
	detail["queries"] = shown
	opts := result.Options{Limits: limits, Omitted: append(omitted, omittedRows...), NextActions: []string{"find-nqe-query", "edit-nqe-query"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvNQE, "searchNqeSources", nil, detail, "")}}
	switch {
	case len(rows) == 0 && partial:
		return result.NewUnknown(findNQEName, fmt.Sprintf("No match in the %d source(s) read, but %d of %d qualifying quer%s were not searched", read, len(org)-read, len(org), map[bool]string{true: "y", false: "ies"}[len(org)-read == 1]), cx, limits, opts)
	case len(rows) == 0:
		return result.Build(findNQEName, result.OK, fmt.Sprintf("None of the %d query source(s) under %s matches", read, dir), result.Deterministic, cx, opts)
	}
	finding := fmt.Sprintf("%d of %d query source(s) under %s match", len(rows), read, dir)
	if partial {
		finding += fmt.Sprintf("; %d qualifying quer%s not searched", len(org)-read, map[bool]string{true: "y", false: "ies"}[len(org)-read == 1])
	}
	return result.Build(findNQEName, result.OK, finding, result.Deterministic, cx, opts)
}

// clipChars shortens text to n characters (never inside a multi-byte one) and marks the cut with an ellipsis.
func clipChars(text string, n int) string {
	var b strings.Builder
	count := 0
	for _, r := range text {
		if count == n {
			b.WriteString("…")
			return b.String()
		}
		b.WriteRune(r)
		count++
	}
	return text
}
