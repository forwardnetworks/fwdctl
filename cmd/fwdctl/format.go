package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
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

// nqeRunCmd runs one NQE query from a file and prints every row (paged for you): fwdctl nqe run --network ID [--file F] [--format table|csv|json|jsonl]
// [--count-by FIELD] [--max N] [--snapshot ID]. The query is read from stdin without --file.
func nqeRunCmd(args []string, stdin io.Reader, stdout, stderr io.Writer, session func() (*fwd.Session, error)) int {
	fs := flag.NewFlagSet("nqe run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	network := fs.String("network", "", "network id (required)")
	file := fs.String("file", "", "file with the NQE query (default: stdin)")
	snapshot := fs.String("snapshot", "", "snapshot id (default: the latest processed)")
	format := fs.String("format", "json", "json, jsonl, table or csv")
	countBy := fs.String("count-by", "", "print how many rows have each value of this field instead of the rows")
	max := fs.Int("max", 50000, "stop after this many rows (stated on stderr when more exist)")
	queryID := fs.String("query-id", "", "run a saved query by id instead of a file (a library query: see fwdctl run find-nqe-query)")
	commitID := fs.String("commit-id", "", "with --query-id: the library commit to run it at (default: the head)")
	paramsFile := fs.String("params", "", "JSON file with the query's parameters, an object of name to typed value")
	asyncRun := fs.Bool("async", false, "run through Forward's asynchronous execution API (the execution key and outcome are in --meta)")
	metaOut := fs.String("meta", "", "write a JSON object about the run (mode, execution key, outcome, Forward's execution time, rows, HTTP status and diagnostics on failure) to this file, or - for stderr")
	waitMax := fs.Duration("timeout", 10*time.Minute, "with --async: how long to wait for the execution")
	var paramKV paramList
	fs.Var(&paramKV, "param", "one parameter as NAME=JSON (repeatable; a value that is not JSON is a string), overrides --params")
	if err := fs.Parse(args); err != nil {
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
	if *asyncRun {
		rows, total, meta, err = sess.RunNQEAsync(context.Background(), *network, run, *max, *waitMax)
		truncated = int64(len(rows)) < total
	} else {
		rows, total, truncated, err = sess.RunNQEAllWith(context.Background(), *network, run, *max)
	}
	writeMeta := func() {
		if *metaOut == "" {
			return
		}
		if err != nil {
			fwd.MetaFromError(&meta, err)
		}
		meta.Mode = map[bool]string{true: "async", false: "sync"}[*asyncRun]
		// Forward caches an execution by a normalised form of the query (a comment, an unused let and useLatestDataFiles were all measured to reuse it): a wall time far below
		// the execution time Forward recorded means the result came from the cache
		if meta.MillisExecuting != nil && *meta.MillisExecuting > 2000 && time.Since(started).Milliseconds()*5 < *meta.MillisExecuting {
			meta.LikelyCached = true
		}
		b, _ := json.MarshalIndent(struct {
			fwd.NQEMeta
			ElapsedSeconds float64 `json:"elapsed_seconds"`
			Rows           int     `json:"rows"`
			Total          int64   `json:"total"`
			SnapshotID     string  `json:"snapshot_id"`
			QueryID        string  `json:"query_id,omitempty"`
			CommitID       string  `json:"commit_id,omitempty"`
		}{meta, time.Since(started).Seconds(), len(rows), total, snap, *queryID, *commitID}, "", "  ")
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
			for _, d := range diags {
				fmt.Fprintf(stderr, "  %s\n", d.Message)
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
	if truncated {
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

// paramList collects repeated --param NAME=JSON flags.
type paramList []string

func (p *paramList) String() string     { return strings.Join(*p, ",") }
func (p *paramList) Set(v string) error { *p = append(*p, v); return nil }

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
