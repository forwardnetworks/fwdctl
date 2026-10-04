package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	forward "github.com/forwardnetworks/forward-go-sdk"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/forwardnetworks/fwdctl/fwd"
)

// cell renders one value for a table or CSV: scalars as they are, nil as empty, anything nested as compact JSON.
func cell(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprint(x)
	case bool:
		return fmt.Sprint(x)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// columnsOf is the union of the rows' keys, in a stable order: keys in first-seen row order would depend on map iteration, so they are sorted.
func columnsOf(rows []map[string]any) []string {
	seen := map[string]bool{}
	var cols []string
	for _, r := range rows {
		for k := range r {
			if !seen[k] {
				seen[k] = true
				cols = append(cols, k)
			}
		}
	}
	sort.Strings(cols)
	return cols
}

// renderRows prints rows as a table, CSV, JSON or JSON lines.
func renderRows(w io.Writer, rows []map[string]any, format string) error {
	cols := columnsOf(rows)
	switch format {
	case "csv":
		cw := csv.NewWriter(w)
		if err := cw.Write(cols); err != nil {
			return err
		}
		for _, r := range rows {
			rec := make([]string, len(cols))
			for i, c := range cols {
				rec[i] = cell(r[c])
			}
			if err := cw.Write(rec); err != nil {
				return err
			}
		}
		cw.Flush()
		return cw.Error()
	case "table":
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, strings.Join(cols, "\t"))
		for _, r := range rows {
			rec := make([]string, len(cols))
			for i, c := range cols {
				rec[i] = strings.NewReplacer("\t", " ", "\n", " ").Replace(cell(r[c]))
			}
			fmt.Fprintln(tw, strings.Join(rec, "\t"))
		}
		return tw.Flush()
	case "jsonl":
		enc := json.NewEncoder(w)
		for _, r := range rows {
			if err := enc.Encode(r); err != nil {
				return err
			}
		}
		return nil
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rows)
}

func validFormat(f string) bool {
	switch f {
	case "", "json", "jsonl", "table", "csv":
		return true
	}
	return false
}

// largestRows finds the biggest list of objects in a skill result's evidence (the rows a person wants as a table), and the path it was found at.
func largestRows(result any) (rows []map[string]any, where string) {
	var walk func(v any, path string)
	walk = func(v any, path string) {
		switch x := v.(type) {
		case []any:
			var objs []map[string]any
			for _, e := range x {
				if m, ok := e.(map[string]any); ok {
					objs = append(objs, m)
				}
			}
			if len(objs) > 0 && len(objs) == len(x) && len(objs) > len(rows) {
				rows, where = objs, path
			}
			for i, e := range x {
				walk(e, fmt.Sprintf("%s[%d]", path, i))
			}
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(x[k], path+"."+k)
			}
		}
	}
	var generic any
	b, _ := json.Marshal(result)
	_ = json.Unmarshal(b, &generic)
	if m, ok := generic.(map[string]any); ok {
		walk(m["evidence"], "evidence")
	}
	return rows, where
}

// namedList is a list of objects somewhere in a skill result's evidence: the key it sits under, its path, and its rows.
type namedList struct {
	key, path string
	rows      []map[string]any
}

// allLists finds every list of objects in a skill result's evidence, in path order.
func allLists(result any) []namedList {
	var out []namedList
	var walk func(v any, path, key string)
	walk = func(v any, path, key string) {
		switch x := v.(type) {
		case []any:
			var objs []map[string]any
			for _, e := range x {
				if m, ok := e.(map[string]any); ok {
					objs = append(objs, m)
				}
			}
			if len(objs) > 0 && len(objs) == len(x) {
				out = append(out, namedList{key: key, path: path, rows: objs})
			}
			for i, e := range x {
				walk(e, fmt.Sprintf("%s[%d]", path, i), key)
			}
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(x[k], path+"."+k, k)
			}
		}
	}
	var generic any
	b, _ := json.Marshal(result)
	_ = json.Unmarshal(b, &generic)
	if m, ok := generic.(map[string]any); ok {
		walk(m["evidence"], "evidence", "evidence")
	}
	return out
}

// pickList is the list a table or CSV should print: the one named (by the key it sits under, or by a path suffix) when a name is given, else the largest. others describes the
// lists NOT printed ("by_vendor (6)"), so a reader knows what else the result holds. ok is false when a name was given and matches nothing.
func pickList(result any, name string) (rows []map[string]any, where string, others []string, ok bool) {
	lists := allLists(result)
	// the evidence array itself is a list of objects (its items); it is the rows only when nothing inside the evidence is
	var inner []namedList
	for _, l := range lists {
		if l.path != "evidence" {
			inner = append(inner, l)
		}
	}
	if len(inner) > 0 {
		lists = inner
	}
	best := -1
	for i, l := range lists {
		if name != "" {
			if l.key == name || l.path == name || strings.HasSuffix(l.path, "."+name) {
				best = i
				break
			}
		} else if best < 0 || len(l.rows) > len(lists[best].rows) {
			best = i
		}
	}
	for i, l := range lists {
		if i != best {
			others = append(others, fmt.Sprintf("%s (%d)", l.key, len(l.rows)))
		}
	}
	if best < 0 {
		return nil, "", others, name == ""
	}
	return lists[best].rows, lists[best].path, others, true
}

// nqeRunOpts are the flags of `fwdctl nqe run` (cobra fills them).
type nqeRunOpts struct {
	network, file, snapshot, format, countBy, queryID, commitID, paramsFile, metaOut string
	max, pageOffset, pageLimit                                                       int
	asyncRun                                                                         bool
	retryTransient                                                                   int
	allowLarge                                                                       bool
	waitMax                                                                          time.Duration
	params                                                                           []string
}

// nqeRunCmd runs one NQE query from a file, a saved id or stdin and prints every row (paged for you), or one page with --limit/--offset.
func nqeRunCmd(a *app, o nqeRunOpts) int {
	stdin, stdout, stderr, session := a.in, a.out, a.err, a.session
	network, file, snapshot, format, countBy, queryID, commitID, paramsFile, metaOut := &o.network, &o.file, &o.snapshot, &o.format, &o.countBy, &o.queryID, &o.commitID, &o.paramsFile, &o.metaOut
	max, pageOffset, pageLimit, asyncRun, waitMax, paramKV := &o.max, &o.pageOffset, &o.pageLimit, &o.asyncRun, &o.waitMax, o.params
	if *pageOffset < 0 || *pageLimit < 0 || (*pageOffset > 0 && *pageLimit == 0) {
		fmt.Fprintln(stderr, "error: --offset needs --limit, and neither may be negative")
		return usage
	}
	if *network == "" || !validFormat(*format) {
		fmt.Fprintln(stderr, "error: usage: fwdctl nqe run --network ID [--file F] [--snapshot ID] [--format json|jsonl|table|csv] [--count-by FIELD] [--max N]")
		return usage
	}
	var src []byte
	var err error
	if *queryID != "" {
		if *file != "" {
			fmt.Fprintln(stderr, "error: give --query-id or --file, not both")
			return usage
		}
	} else if *commitID != "" {
		fmt.Fprintln(stderr, "error: --commit-id needs --query-id")
		return usage
	} else {
		if *file != "" {
			src, err = os.ReadFile(*file)
		} else {
			src, err = io.ReadAll(stdin)
		}
		if err != nil || strings.TrimSpace(string(src)) == "" {
			fmt.Fprintln(stderr, "error: give the query with --file, --query-id or on stdin")
			return usage
		}
	}
	params, perr := nqeParams(*paramsFile, paramKV)
	if perr != nil {
		fmt.Fprintf(stderr, "error: %v\n", perr)
		return usage
	}
	requestTimeout = *waitMax
	sess, err := session()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return usage
	}
	snap := *snapshot
	if snap == "" {
		s, err := sess.LatestProcessed(context.Background(), *network)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 3
		}
		if s == nil {
			fmt.Fprintln(stderr, "error: the network has no processed snapshot; nothing was read")
			return 2
		}
		snap = fwd.SnapshotID(fwd.Context(*network, s))
	}
	started := time.Now()
	run := fwd.NQERun{Query: string(src), QueryID: *queryID, CommitID: *commitID, Parameters: params, SnapshotID: snap}
	var rows []map[string]any
	var total int64
	var truncated bool
	meta := fwd.NQEMeta{Mode: "sync", Diagnostics: []fwd.QueryDiagnostic{}}
	paged := *pageLimit > 0
	attempts := 0
	for {
		attempts++
		if paged {
			var pm fwd.NQEMeta
			rows, total, pm, err = sess.RunNQEPage(context.Background(), *network, run, *pageLimit, *pageOffset, *asyncRun, *waitMax)
			meta = pm
			truncated = false
		} else if *asyncRun {
			rows, total, meta, err = sess.RunNQEAsync(context.Background(), *network, run, *max, *waitMax)
			truncated = int64(len(rows)) < total
		} else {
			rows, total, truncated, err = sess.RunNQEAllWith(context.Background(), *network, run, *max)
		}
		if err == nil || attempts > o.retryTransient || !transientGatewayError(err) {
			break
		}
		wait := min(time.Duration(attempts)*5*time.Second, 30*time.Second)
		fmt.Fprintf(stderr, "Forward answered with a gateway error (%v); retrying in %s (attempt %d of %d)\n", err, wait, attempts+1, o.retryTransient+1)
		transientSleep(wait)
	}
	if o.retryTransient > 0 {
		clean := attempts == 1
		meta.Attempts, meta.TransportClean = attempts, &clean
	}
	writeMeta := func() {
		if *metaOut == "" {
			return
		}
		if err != nil {
			fwd.MetaFromError(&meta, err)
		}
		meta.Mode = map[bool]string{true: "async", false: "sync"}[*asyncRun]
		// Forward caches an execution by a normalised form of the query (a comment, an unused let and useLatestDataFiles were all measured to reuse it; a filter on a changing
		// constant was measured NOT to be folded away, so it makes a new execution). A real execution takes at least the time Forward recorded for it, so a wall time below that
		// means the result came from the cache (measured: a hit took 43.7s against 114.6s recorded; a cold run 165-175s against 115-117s, polling and transfer included)
		if meta.MillisExecuting != nil && *meta.MillisExecuting > 2000 && time.Since(started).Milliseconds()*10 < *meta.MillisExecuting*9 {
			meta.LikelyCached = true
		}
		b, _ := json.MarshalIndent(struct {
			fwd.NQEMeta
			ElapsedSeconds float64 `json:"elapsed_seconds"`
			Rows           int     `json:"rows"`
			Total          int64   `json:"total"`
			Offset         int     `json:"offset"`
			Limit          int     `json:"limit,omitempty"`
			SnapshotID     string  `json:"snapshot_id"`
			QueryID        string  `json:"query_id,omitempty"`
			CommitID       string  `json:"commit_id,omitempty"`
		}{meta, time.Since(started).Seconds(), len(rows), total, *pageOffset, *pageLimit, snap, *queryID, *commitID}, "", "  ")
		if *metaOut == "-" {
			fmt.Fprintln(stderr, string(b))
		} else {
			_ = os.WriteFile(*metaOut, append(b, '\n'), 0o644)
		}
	}
	defer writeMeta()
	if err != nil {
		if diags, msg, ok := fwd.QueryErrors(err); ok {
			fmt.Fprintf(stderr, "the query does not compile: %s\n", msg)
			for _, d := range fwd.AnnotateDiagnostics(string(src), diags) {
				fmt.Fprintf(stderr, "  %s\n", d.Message)
				if d.SourceLine != "" {
					fmt.Fprintf(stderr, "    at: %s\n", d.SourceLine)
				}
				if d.Hint != "" {
					fmt.Fprintf(stderr, "    %s\n", d.Hint)
				}
			}
			return 1
		}
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 3
	}
	// timing and scope on stderr, so a harness can read them without them mixing into the rows
	fmt.Fprintf(stderr, "ran in %.2fs: %d row(s) of %d, snapshot %s", time.Since(started).Seconds(), len(rows), total, snap)
	if *queryID != "" {
		c := *commitID
		if c == "" {
			c = "head"
		}
		fmt.Fprintf(stderr, ", query %s at commit %s", *queryID, c)
	}
	fmt.Fprintln(stderr)
	if paged {
		fmt.Fprintf(stderr, "page: rows %d-%d of %d (offset %d, limit %d)\n", *pageOffset, *pageOffset+len(rows), total, *pageOffset, *pageLimit)
	} else if truncated {
		fmt.Fprintf(stderr, "note: %d rows exist; the first %d are shown (--max raises the bound)\n", total, len(rows))
	}
	if *countBy != "" {
		counts := map[string]int{}
		for _, r := range rows {
			counts[cell(r[*countBy])]++
		}
		out := make([]map[string]any, 0, len(counts))
		for k, n := range counts {
			out = append(out, map[string]any{*countBy: k, "count": n})
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i]["count"].(int) != out[j]["count"].(int) {
				return out[i]["count"].(int) > out[j]["count"].(int)
			}
			return fmt.Sprint(out[i][*countBy]) < fmt.Sprint(out[j][*countBy])
		})
		rows = out
	}
	if est := estimateRowsBytes(rows); est > maxOutputBytes && !o.allowLarge {
		fmt.Fprintf(stderr, "error: this result is about %d MB as text (%d rows), over the %d MB limit, and nothing was written: redirecting it to a file can fill the disk. Narrow the query, page it (--limit/--offset), summarise it (--count-by FIELD), or pass --allow-large and write to a disk with room\n", est>>20, len(rows), maxOutputBytes>>20)
		return 1
	}
	if err := renderRows(stdout, rows, *format); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 3
	}
	if len(rows) == 0 {
		// csv and table print nothing for no rows, which looks like a failure
		fmt.Fprintf(stderr, "0 rows returned (snapshot %s)\n", snap)
	}
	return 0
}

// nqeParams reads the query parameters: a JSON object from a file, then each NAME=JSON (a value that does not parse as JSON is a string).
func nqeParams(file string, kv []string) (map[string]any, error) {
	var out map[string]any
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &out); err != nil {
			return nil, fmt.Errorf("--params %s must be a JSON object: %v", file, err)
		}
	}
	for _, p := range kv {
		name, val, ok := strings.Cut(p, "=")
		if !ok || name == "" {
			return nil, fmt.Errorf("--param %q must be NAME=VALUE", p)
		}
		if out == nil {
			out = map[string]any{}
		}
		var v any
		if json.Unmarshal([]byte(val), &v) != nil {
			v = val
		}
		out[name] = v
	}
	return out, nil
}

// maxOutputBytes is the largest result nqe run writes without --allow-large (about what its text form takes).
const maxOutputBytes = 100 << 20

// estimateRowsBytes sizes a result by encoding a sample of its rows, so a huge result is caught before any of it is written.
func estimateRowsBytes(rows []map[string]any) int64 {
	if len(rows) == 0 {
		return 0
	}
	n := min(len(rows), 500)
	var total int64
	for _, r := range rows[:n] {
		b, _ := json.Marshal(r)
		total += int64(len(b)) + 1
	}
	return total / int64(n) * int64(len(rows))
}

// transientSleep waits between retries of a transient gateway failure (a variable so tests do not wait).
var transientSleep = time.Sleep

// transientGatewayError is a 502, 503 or 504 from Forward or its gateway: a restart or an overloaded upstream, not an answer about the query. A read-only query can be repeated safely.
func transientGatewayError(err error) bool {
	var er *forward.ErrorResponse
	if !errors.As(err, &er) || er.Response == nil {
		return false
	}
	switch er.Response.StatusCode {
	case 502, 503, 504:
		return true
	}
	return false
}
