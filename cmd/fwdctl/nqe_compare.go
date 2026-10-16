package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/nqelint"
)

// nqeCompare runs two versions of a query on ONE snapshot and says whether their rows are the same. nqe diff is the other direction (one query, two snapshots); this is the release gate for a rewrite.
func (a *app) nqeCompare() *cobra.Command {
	var network, snapshot, fileA, fileB, idA, idB, commitA, commitB, paramsFile, key, out string
	var params []string
	var rounds, max, show int
	var ignoreOrder, warm bool
	c := &cobra.Command{
		Use: "compare", Short: "run two versions of a query on one snapshot and report whether the rows (and their shape) are the same",
		Long: "Runs version A and version B (each --a/--b FILE, or --a-query-id/--b-query-id with an optional commit) on the same snapshot with the same parameters, and reports: whether the rows are identical (row order is\n" +
			"ignored, array order inside a row counts), the rows added, removed and changed by --key (without --key, rows are compared as a set), a shape section (columns only in one side, columns whose JSON types differ),\n" +
			"the number of value differences that remain when array order is ignored, and the timing of every run (execution key, millis executing, queued, wall, rows, bytes, whether Forward served it from cache).\n" +
			"File queries are made cold: an always-true filter on a unique constant is added so Forward's result cache sees a new program each run (--warm turns that off; a saved query cannot be made cold). Runs alternate\n" +
			"A,B then B,A for --rounds 2, so a slow first run is not charged to one side. A and B being the same text is refused: nothing would be compared.\n" +
			"Exit status: 0 identical (with --ignore-array-order: identical apart from array order), 1 they differ, 64 bad usage or a key that is not unique, 3 error. Each run is a real execution and can be slow on a large network.",
		Example: "  fwdctl nqe compare --network 123 --snapshot 456 --a old.nqe --b new.nqe --key device,rule --params p.json --out diffs.jsonl",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if network == "" || ((fileA == "") == (idA == "")) || ((fileB == "") == (idB == "")) {
				return a.fail("usage: fwdctl nqe compare --network ID [--snapshot S] (--a FILE | --a-query-id Q) (--b FILE | --b-query-id Q) [--key COL[,COL]] [--params FILE] [--ignore-array-order] [--rounds N]")
			}
			side := func(file, id, commit string) (nqeSide, error) {
				if id != "" {
					if p := idsProblem(id, commit); p != "" {
						return nqeSide{}, fmt.Errorf("%s", p)
					}
					return nqeSide{id: id, commit: commit}, nil
				}
				b, err := os.ReadFile(file)
				if err != nil || strings.TrimSpace(string(b)) == "" {
					return nqeSide{}, fmt.Errorf("cannot read a query from %s", file)
				}
				return nqeSide{src: string(b)}, nil
			}
			sa, err := side(fileA, idA, commitA)
			if err != nil {
				return a.fail("%v", err)
			}
			sb, err := side(fileB, idB, commitB)
			if err != nil {
				return a.fail("%v", err)
			}
			if strings.TrimSpace(sa.src) == strings.TrimSpace(sb.src) && sa.id == sb.id && sa.commit == sb.commit {
				return a.fail("A and B are the same query (the same text or the same saved query and commit): there is nothing to compare")
			}
			if rounds < 1 {
				rounds = 1
			}
			p, perr := nqeParams(paramsFile, params)
			if perr != nil {
				return a.fail("%v", perr)
			}
			sess, err := a.session()
			if err != nil {
				return a.fail("%v", err)
			}
			ctx := cmd.Context()
			if snapshot == "" {
				s, serr := sess.LatestProcessed(ctx, network)
				if serr != nil || s == nil {
					fmt.Fprintln(a.err, "error: the network has no processed snapshot to run on; give --snapshot")
					return a.exit(3)
				}
				snapshot = string(s.ID)
			}
			type run struct {
				Side            string `json:"side"`
				Round           int    `json:"round"`
				ExecutionKey    string `json:"execution_key,omitempty"`
				MillisExecuting *int64 `json:"millis_executing,omitempty"`
				QueuedMillis    *int64 `json:"queued_millis,omitempty"`
				WallMillis      int64  `json:"wall_millis"`
				Rows            int    `json:"rows"`
				Bytes           int    `json:"response_bytes"`
				LikelyCached    bool   `json:"likely_cached,omitempty"`
				digest          string
			}
			var runs []run
			rows := map[string][]map[string]any{}
			stable := map[string]bool{"a": true, "b": true}
			firstDigest := map[string]string{}
			var notes []string
			cold := !warm
			if cold && sa.src == "" && sb.src == "" {
				cold = false
			}
			for r := 0; r < rounds; r++ {
				order := []string{"a", "b"}
				if r%2 == 1 {
					order = []string{"b", "a"}
				}
				for _, sd := range order {
					s := sa
					if sd == "b" {
						s = sb
					}
					req := fwd.NQERun{QueryID: s.id, CommitID: s.commit, Parameters: p, SnapshotID: snapshot}
					if s.id == "" {
						req.Query = s.src
						if cold {
							busted, berr := nqelint.BustCache(s.src, fmt.Sprintf("%d-%s-%d-%d", os.Getpid(), sd, r, time.Now().UnixNano()))
							if berr != nil {
								cold = false
								notes = append(notes, "the runs were not made cold ("+berr.Error()+"): a result Forward cached may make a later run look fast")
							} else {
								req.Query = busted
							}
						}
					}
					started := time.Now()
					got, total, meta, rerr := sess.RunNQEAsync(ctx, network, req, max, 20*time.Minute)
					if rerr != nil {
						fmt.Fprintf(a.err, "error: version %s, round %d: %v\n", strings.ToUpper(sd), r+1, rerr)
						return a.exit(3)
					}
					if total > int64(len(got)) {
						fmt.Fprintf(a.err, "error: version %s returned %d rows, more than --max %d; a comparison of a partial read would be wrong\n", strings.ToUpper(sd), total, max)
						return a.exit(3)
					}
					enc, _ := json.Marshal(got)
					d := canonicalDigest(got, false)
					runs = append(runs, run{Side: sd, Round: r + 1, ExecutionKey: meta.ExecutionKey, MillisExecuting: meta.MillisExecuting, QueuedMillis: meta.QueuedMillis,
						WallMillis: time.Since(started).Milliseconds(), Rows: len(got), Bytes: len(enc), LikelyCached: meta.LikelyCached, digest: d})
					if _, ok := rows[sd]; !ok {
						rows[sd], firstDigest[sd] = got, d
					} else if d != firstDigest[sd] {
						stable[sd] = false
					}
				}
			}
			keys := splitKeys(key)
			res, code := compareRows(rows["a"], rows["b"], keys, ignoreOrder, show, a.err)
			if code == 64 {
				return a.exit(64)
			}
			if out != "" {
				if err := writeCompareOut(out, res.all); err != nil {
					fmt.Fprintf(a.err, "error: %v\n", err)
					return a.exit(3)
				}
			}
			for _, sd := range []string{"a", "b"} {
				if !stable[sd] {
					notes = append(notes, "version "+strings.ToUpper(sd)+" returned different rows on different runs: the comparison used its first run")
				}
			}
			if len(keys) == 0 {
				notes = append(notes, "no --key: rows were compared as a set. Identical rows match whatever their order; a row whose value changed shows as one removed and one added, not as changed. Name the key columns (--key) to see changes per row")
			}
			notes = append(notes, "response_bytes is the size of the rows as compact JSON; a file saved with an indented --format json is larger")
			summary := map[string]any{"network": network, "snapshot": snapshot, "key": keys, "rows_a": len(rows["a"]), "rows_b": len(rows["b"]), "identical": res.identical, "counts": res.counts,
				"value_differences_ignoring_array_order": res.lenientChanged, "shape": res.shape, "runs": runs, "cold": cold, "added": nonNil(res.lists["added"]), "removed": nonNil(res.lists["removed"]),
				"changed": nonNil(res.lists["changed"]), "listing_capped_at": show, "notes": nonNil(notes)}
			if out != "" {
				summary["out"] = out
			}
			bs, _ := json.MarshalIndent(summary, "", "  ")
			fmt.Fprintln(a.out, string(bs))
			if res.differs(ignoreOrder) {
				return a.exit(1)
			}
			return nil
		},
	}
	f := c.Flags()
	f.StringVar(&network, "network", "", "network id")
	f.StringVar(&snapshot, "snapshot", "", "snapshot id (default: the newest processed)")
	f.StringVar(&fileA, "a", "", "version A: a query file")
	f.StringVar(&fileB, "b", "", "version B: a query file")
	f.StringVar(&idA, "a-query-id", "", "version A: a saved query id")
	f.StringVar(&idB, "b-query-id", "", "version B: a saved query id")
	f.StringVar(&commitA, "a-commit-id", "", "the commit of saved query A (default HEAD)")
	f.StringVar(&commitB, "b-commit-id", "", "the commit of saved query B (default HEAD)")
	f.StringVar(&paramsFile, "params", "", "a JSON file of query parameters (used for both versions)")
	f.StringArrayVar(&params, "param", nil, "a query parameter NAME=VALUE (repeatable)")
	f.StringVar(&key, "key", "", "the column(s) that identify a row, comma separated (without it rows are compared as a set)")
	f.StringVar(&out, "out", "", "write every difference to this file as JSON lines")
	f.IntVar(&rounds, "rounds", 1, "runs of each version; with 2 or more the order alternates (A,B,B,A) so neither side always runs first")
	f.IntVar(&max, "max", 200000, "refuse a result of more rows than this")
	f.IntVar(&show, "show", 20, "list at most this many of each kind in the summary")
	f.BoolVar(&ignoreOrder, "ignore-array-order", false, "treat arrays that differ only in element order as equal (and exit 0 then)")
	f.BoolVar(&warm, "warm", false, "do not add the cache-busting filter to file queries (a cached result then makes a run look fast)")
	return c
}

type nqeSide struct{ src, id, commit string }

func splitKeys(key string) []string {
	var out []string
	for _, k := range strings.Split(key, ",") {
		if k = strings.TrimSpace(k); k != "" {
			out = append(out, k)
		}
	}
	return out
}

// normalizeValue sorts every array by the JSON text of its (normalized) elements, so two arrays that hold the same elements in another order become equal.
func normalizeValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, e := range x {
			m[k] = normalizeValue(e)
		}
		return m
	case []any:
		l := make([]any, len(x))
		for i, e := range x {
			l[i] = normalizeValue(e)
		}
		sort.SliceStable(l, func(i, j int) bool {
			a, _ := json.Marshal(l[i])
			b, _ := json.Marshal(l[j])
			return string(a) < string(b)
		})
		return l
	}
	return v
}

func rowJSON(r map[string]any, lenient bool) string {
	var v any = r
	if lenient {
		v = normalizeValue(r)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// canonicalDigest is a digest of the rows as a set (row order ignored).
func canonicalDigest(rows []map[string]any, lenient bool) string {
	l := make([]string, len(rows))
	for i, r := range rows {
		l[i] = rowJSON(r, lenient)
	}
	sort.Strings(l)
	h := sha256.Sum256([]byte(strings.Join(l, "\n")))
	return hex.EncodeToString(h[:8])
}

type compareDiff struct {
	Change  string         `json:"change"`
	Key     map[string]any `json:"key,omitempty"`
	A       map[string]any `json:"a,omitempty"`
	B       map[string]any `json:"b,omitempty"`
	Columns []string       `json:"changed_columns,omitempty"`
}

type compareResult struct {
	identical      bool
	counts         map[string]int
	lenientChanged int
	lists          map[string][]compareDiff
	all            []compareDiff
	shape          map[string]any
}

func (r compareResult) differs(ignoreOrder bool) bool {
	if ignoreOrder {
		return r.counts["added"]+r.counts["removed"]+r.lenientChanged > 0
	}
	return !r.identical
}

// compareRows compares the rows of A and B. With keys it pairs rows by key (a repeated key is refused, code 64) and reports added, removed and changed; without, it compares the rows as a set.
func compareRows(a, b []map[string]any, keys []string, ignoreOrder bool, show int, stderr interface{ Write([]byte) (int, error) }) (compareResult, int) {
	res := compareResult{counts: map[string]int{"added": 0, "removed": 0, "changed": 0, "unchanged": 0}, lists: map[string][]compareDiff{}}
	res.identical = canonicalDigest(a, false) == canonicalDigest(b, false) && len(a) == len(b)
	res.shape = compareShape(a, b)
	record := func(d compareDiff) {
		res.all = append(res.all, d)
		res.counts[d.Change]++
		if len(res.lists[d.Change]) < show {
			res.lists[d.Change] = append(res.lists[d.Change], d)
		}
	}
	if len(keys) == 0 {
		count := func(rows []map[string]any, lenient bool) map[string]int {
			m := map[string]int{}
			for _, r := range rows {
				m[rowJSON(r, lenient)]++
			}
			return m
		}
		ca, cb := count(a, false), count(b, false)
		for k, n := range cb {
			if d := n - ca[k]; d > 0 {
				var row map[string]any
				_ = json.Unmarshal([]byte(k), &row)
				for i := 0; i < d; i++ {
					record(compareDiff{Change: "added", B: row})
				}
			}
		}
		for k, n := range ca {
			if d := n - cb[k]; d > 0 {
				var row map[string]any
				_ = json.Unmarshal([]byte(k), &row)
				for i := 0; i < d; i++ {
					record(compareDiff{Change: "removed", A: row})
				}
			}
		}
		res.counts["unchanged"] = len(a) - res.counts["removed"]
		if ignoreOrder { // without a key there is no "changed" row: added and removed carry the difference, recounted on the normalized rows
			la, lb := count(a, true), count(b, true)
			res.counts["added"], res.counts["removed"] = 0, 0
			for k, n := range lb {
				if n > la[k] {
					res.counts["added"] += n - la[k]
				}
			}
			for k, n := range la {
				if n > lb[k] {
					res.counts["removed"] += n - lb[k]
				}
			}
		}
		return res, 0
	}
	index := func(rows []map[string]any, side string) (map[string]map[string]any, int) {
		m := make(map[string]map[string]any, len(rows))
		for _, r := range rows {
			parts := make([]string, len(keys))
			for i, k := range keys {
				v, ok := r[k]
				if !ok {
					fmt.Fprintf(stderr, "error: the rows of version %s have no column %q; the columns are %s\n", side, k, strings.Join(rowColumns(r), ", "))
					return nil, 64
				}
				bs, _ := json.Marshal(v)
				parts[i] = string(bs)
			}
			id := strings.Join(parts, "\x00")
			if _, dup := m[id]; dup {
				fmt.Fprintf(stderr, "error: the key %s is not unique in version %s; name more columns in --key so each row has its own key\n", strings.Join(keys, ","), side)
				return nil, 64
			}
			m[id] = r
		}
		return m, 0
	}
	ia, code := index(a, "A")
	if code != 0 {
		return res, code
	}
	ib, code := index(b, "B")
	if code != 0 {
		return res, code
	}
	keyOf := func(r map[string]any) map[string]any {
		kv := map[string]any{}
		for _, k := range keys {
			kv[k] = r[k]
		}
		return kv
	}
	ids := make([]string, 0, len(ia)+len(ib))
	for id := range ia {
		ids = append(ids, id)
	}
	for id := range ib {
		if _, ok := ia[id]; !ok {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		ra, inA := ia[id]
		rb, inB := ib[id]
		switch {
		case inB && !inA:
			record(compareDiff{Change: "added", Key: keyOf(rb), B: rb})
		case inA && !inB:
			record(compareDiff{Change: "removed", Key: keyOf(ra), A: ra})
		default:
			if cols := changedColumns(ra, rb); len(cols) > 0 {
				record(compareDiff{Change: "changed", Key: keyOf(ra), A: ra, B: rb, Columns: cols})
				if rowJSON(ra, true) != rowJSON(rb, true) {
					res.lenientChanged++
				}
			} else {
				res.counts["unchanged"]++
			}
		}
	}
	return res, 0
}

// compareShape describes how the two result sets are shaped: the columns each has, the columns whose JSON types differ, and the nested key paths found in only one.
func compareShape(a, b []map[string]any) map[string]any {
	typesOf := func(rows []map[string]any) (map[string]map[string]bool, map[string]bool) {
		cols, paths := map[string]map[string]bool{}, map[string]bool{}
		var walk func(prefix string, v any)
		walk = func(prefix string, v any) {
			switch x := v.(type) {
			case map[string]any:
				for k, e := range x {
					walk(prefix+"."+k, e)
				}
			case []any:
				for _, e := range x {
					walk(prefix+"[]", e)
				}
			}
			paths[prefix] = true
		}
		for _, r := range rows {
			for k, v := range r {
				if cols[k] == nil {
					cols[k] = map[string]bool{}
				}
				cols[k][jsonType(v)] = true
				walk(k, v)
			}
		}
		return cols, paths
	}
	ca, pa := typesOf(a)
	cb, pb := typesOf(b)
	only := func(x, y map[string]bool) []string {
		var out []string
		for k := range x {
			if !y[k] {
				out = append(out, k)
			}
		}
		sort.Strings(out)
		if len(out) > 50 {
			out = append(out[:50], fmt.Sprintf("... %d more", len(out)-50))
		}
		return nonNil(out)
	}
	colSet := func(c map[string]map[string]bool) map[string]bool {
		m := map[string]bool{}
		for k := range c {
			m[k] = true
		}
		return m
	}
	typeDiff := map[string]any{}
	for k, ta := range ca {
		if tb, ok := cb[k]; ok && strings.Join(setKeys(ta), ",") != strings.Join(setKeys(tb), ",") {
			typeDiff[k] = map[string]any{"a": setKeys(ta), "b": setKeys(tb)}
		}
	}
	return map[string]any{"columns_only_in_a": only(colSet(ca), colSet(cb)), "columns_only_in_b": only(colSet(cb), colSet(ca)), "column_type_differences": typeDiff,
		"paths_only_in_a": only(pa, pb), "paths_only_in_b": only(pb, pa)}
}

func setKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func jsonType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64, int, int64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return fmt.Sprintf("%T", v)
}

func writeCompareOut(path string, all []compareDiff) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, d := range all {
		line, _ := json.Marshal(d)
		w.Write(append(line, '\n'))
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
