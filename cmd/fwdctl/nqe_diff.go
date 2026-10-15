package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/forwardnetworks/fwdctl/fwd"
)

// nqeDiff runs one ad-hoc query on two snapshots and reports the rows added, removed and changed, by key. It needs no saved query and no write to the organization, and it returns every difference (counts
// exact; a capped listing; the full list in --out), where compare-nqe-results needs a committed query and shows a hundred rows.
func (a *app) nqeDiff() *cobra.Command {
	var network, file, before, after, key, out string
	var max, show int
	var exitZero bool
	c := &cobra.Command{
		Use: "diff", Short: "run an ad-hoc query on two snapshots and report the rows added, removed and changed by key",
		Long: "Runs the query (--file or stdin) on --before-snapshot and --after-snapshot and compares the rows by the --key columns: which keys were added, which removed, which changed (and which columns). The key columns must\n" +
			"identify a row uniquely in each snapshot (a repeated key is refused, since the diff would be ambiguous). Counts are exact; the stdout JSON lists up to --show of each kind; --out writes every difference as\n" +
			"JSON lines {change, key, before, after, changed_columns}. If a result is larger than --max rows the diff is refused rather than reported on a partial read.\n" +
			"Exit status: 0 no differences, 1 differences found (0 with --exit-zero), 64 bad usage or a key that is not unique, 3 error. Runs are asynchronous by default; for a large result run the query alone first (`nqe run --limit 1 --meta`) to see its size.",
		Example: "  fwdctl nqe diff --network 123 --before-snapshot 111 --after-snapshot 222 --file rules.nqe --key device,ruleId --out changes.jsonl",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if network == "" || before == "" || after == "" || strings.TrimSpace(key) == "" {
				return a.fail("usage: fwdctl nqe diff --network ID --before-snapshot A --after-snapshot B --key COL[,COL] [--file q.nqe] [--out FILE]")
			}
			var src []byte
			var err error
			if file != "" {
				src, err = os.ReadFile(file)
			} else {
				src, err = io.ReadAll(a.in)
			}
			if err != nil || strings.TrimSpace(string(src)) == "" {
				return a.fail("give the query with --file or on stdin")
			}
			sess, err := a.session()
			if err != nil {
				return a.fail("%v", err)
			}
			keys := strings.Split(key, ",")
			code := diffSnapshots(cmd.Context(), sess, network, string(src), before, after, keys, max, show, out, a.out, a.err)
			if code == 1 && exitZero {
				code = 0
			}
			if code != 0 {
				return a.exit(code)
			}
			return nil
		},
	}
	f := c.Flags()
	f.StringVar(&network, "network", "", "network id")
	f.StringVar(&file, "file", "", "the query (default: stdin)")
	f.StringVar(&before, "before-snapshot", "", "the earlier snapshot id")
	f.StringVar(&after, "after-snapshot", "", "the later snapshot id")
	f.StringVar(&key, "key", "", "the column(s) that identify a row, comma separated")
	f.StringVar(&out, "out", "", "write every difference to this file as JSON lines")
	f.IntVar(&max, "max", 200000, "refuse a result of more rows than this in either snapshot")
	f.BoolVar(&exitZero, "exit-zero", false, "exit 0 when differences are found (for scripts); errors and bad usage still fail")
	f.IntVar(&show, "show", 20, "list at most this many of each kind in the summary")
	return c
}

func diffSnapshots(ctx context.Context, sess *fwd.Session, network, query, before, after string, keys []string, max, show int, outPath string, stdout, stderr io.Writer) int {
	read := func(snap string) ([]map[string]any, bool) {
		rows, total, truncated, err := sess.RunNQEAllWith(ctx, network, fwd.NQERun{Query: query, SnapshotID: snap}, max)
		if err != nil {
			fmt.Fprintf(stderr, "error: snapshot %s: %v\n", snap, err)
			return nil, false
		}
		if truncated {
			fmt.Fprintf(stderr, "error: snapshot %s has %d rows, more than --max %d; a diff of a partial read would be wrong. Raise --max or narrow the query\n", snap, total, max)
			return nil, false
		}
		return rows, true
	}
	b, ok := read(before)
	if !ok {
		return 3
	}
	af, ok := read(after)
	if !ok {
		return 3
	}
	index := func(rows []map[string]any, snap string) (map[string]map[string]any, int) {
		m := make(map[string]map[string]any, len(rows))
		for _, r := range rows {
			parts := make([]string, len(keys))
			kv := map[string]any{}
			for i, k := range keys {
				v, present := r[strings.TrimSpace(k)]
				if !present {
					fmt.Fprintf(stderr, "error: the rows of snapshot %s have no column %q; the columns are %s\n", snap, strings.TrimSpace(k), strings.Join(rowColumns(r), ", "))
					return nil, 64
				}
				bs, _ := json.Marshal(v)
				parts[i] = string(bs)
				kv[strings.TrimSpace(k)] = v
			}
			id := strings.Join(parts, "\x00")
			if _, dup := m[id]; dup {
				fmt.Fprintf(stderr, "error: the key %s is not unique in snapshot %s (key %v repeats); name more columns in --key so each row has its own key\n", strings.Join(keys, ","), snap, kv)
				return nil, 64
			}
			m[id] = r
		}
		return m, 0
	}
	bi, code := index(b, before)
	if code != 0 {
		return code
	}
	ai, code := index(af, after)
	if code != 0 {
		return code
	}
	type diff struct {
		Change  string         `json:"change"`
		Key     map[string]any `json:"key"`
		Before  map[string]any `json:"before,omitempty"`
		After   map[string]any `json:"after,omitempty"`
		Columns []string       `json:"changed_columns,omitempty"`
	}
	keyOf := func(r map[string]any) map[string]any {
		kv := map[string]any{}
		for _, k := range keys {
			kv[strings.TrimSpace(k)] = r[strings.TrimSpace(k)]
		}
		return kv
	}
	var all []diff
	ids := make([]string, 0, len(ai))
	for id := range ai {
		ids = append(ids, id)
	}
	for id := range bi {
		if _, ok := ai[id]; !ok {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	unchanged := 0
	for _, id := range ids {
		br, inB := bi[id]
		ar, inA := ai[id]
		switch {
		case inA && !inB:
			all = append(all, diff{Change: "added", Key: keyOf(ar), After: ar})
		case inB && !inA:
			all = append(all, diff{Change: "removed", Key: keyOf(br), Before: br})
		default:
			if cols := changedColumns(br, ar); len(cols) > 0 {
				all = append(all, diff{Change: "changed", Key: keyOf(ar), Before: br, After: ar, Columns: cols})
			} else {
				unchanged++
			}
		}
	}
	counts := map[string]int{"added": 0, "removed": 0, "changed": 0, "unchanged": unchanged}
	lists := map[string][]diff{}
	for _, d := range all {
		counts[d.Change]++
		if len(lists[d.Change]) < show {
			lists[d.Change] = append(lists[d.Change], d)
		}
	}
	if outPath != "" {
		f, err := os.Create(outPath)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 3
		}
		w := bufio.NewWriter(f)
		for _, d := range all {
			line, _ := json.Marshal(d)
			w.Write(append(line, '\n'))
		}
		if err := w.Flush(); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 3
		}
		f.Close()
	}
	summary := map[string]any{"before_snapshot": before, "after_snapshot": after, "key": keys, "before_rows": len(b), "after_rows": len(af), "counts": counts,
		"added": nonNil(lists["added"]), "removed": nonNil(lists["removed"]), "changed": nonNil(lists["changed"]), "listing_capped_at": show}
	if outPath != "" {
		summary["out"] = outPath
	}
	bs, _ := json.MarshalIndent(summary, "", "  ")
	fmt.Fprintln(stdout, string(bs))
	if counts["added"]+counts["removed"]+counts["changed"] > 0 {
		return 1
	}
	return 0
}

func nonNil[T any](l []T) []T {
	if l == nil {
		return []T{}
	}
	return l
}

func rowColumns(r map[string]any) []string {
	out := make([]string, 0, len(r))
	for k := range r {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// changedColumns names the columns whose values differ between two rows of the same key.
func changedColumns(b, a map[string]any) []string {
	seen := map[string]bool{}
	var out []string
	for k, bv := range b {
		seen[k] = true
		av, ok := a[k]
		bb, _ := json.Marshal(bv)
		ab, _ := json.Marshal(av)
		if !ok || string(bb) != string(ab) {
			out = append(out, k)
		}
	}
	for k := range a {
		if !seen[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
