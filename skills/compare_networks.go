package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const (
	maxCrossRows    = 200000
	maxCrossShown   = 100
	defaultKeyShown = 25
)

// compareAcrossNetworks is compare-nqe-results with after_network_id: the same saved query run on snapshot A of one network and snapshot B of another, diffed here by key.
// Forward's own diff compares two snapshots of one network, so this reads both result sets and compares them: rows only on one side, and rows with the same key whose other
// fields differ. key names the columns that identify a row (default: the whole row, so only added and removed); ignore drops columns before comparing (names, ids and other fields
// that are expected to differ between two networks).
func compareAcrossNetworks(ctx context.Context, s *fwd.Session, in compareNQEInput) (result.Result, error) {
	key := trimAll(in.Key)
	ignore := map[string]bool{}
	for _, f := range trimAll(in.Ignore) {
		ignore[f] = true
	}
	for _, k := range key {
		if ignore[k] {
			return result.Result{}, fmt.Errorf("%w: %q is both a key and ignored", ErrInvalidInput, k)
		}
	}
	a, err := s.Snapshot(ctx, in.NetworkID, in.BeforeSnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	b, err := s.Snapshot(ctx, in.AfterNetworkID, in.AfterSnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	cx := fwd.Context(in.AfterNetworkID, b)
	if a == nil || b == nil || !fwd.IsReady(a) || !fwd.IsReady(b) {
		return result.NewUnknown(compareNQEName, "Both snapshots must exist and be processed to compare them", cx,
			[]string{fmt.Sprintf("snapshot %s of network %s is %s, snapshot %s of network %s is %s; nothing was compared", in.BeforeSnapshotID, in.NetworkID, fwd.StateOf(a), in.AfterSnapshotID, in.AfterNetworkID, fwd.StateOf(b))},
			result.Options{NextActions: []string{"inspect-snapshots"}})
	}
	read := func(network, snap string) ([]map[string]any, bool, error) {
		rows, _, trunc, err := s.RunNQEAllWith(ctx, network, fwd.NQERun{QueryID: in.QueryID, CommitID: in.CommitID, SnapshotID: snap}, maxCrossRows)
		return rows, trunc, err
	}
	rowsA, truncA, err := read(in.NetworkID, in.BeforeSnapshotID)
	if err != nil {
		if fwd.NotFound(err) {
			return result.NewUnknown(compareNQEName, fmt.Sprintf("Forward has no committed query %q to compare", in.QueryID), cx,
				[]string{"only queries committed to the NQE library can be compared; use find-nqe-query to get an id"}, result.Options{NextActions: []string{"find-nqe-query"}})
		}
		return result.Result{}, err
	}
	rowsB, truncB, err := read(in.AfterNetworkID, in.AfterSnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	if len(rowsA) == 0 && len(rowsB) == 0 {
		return result.NewUnknown(compareNQEName, "The query returns no rows on either side, so the comparison shows nothing", cx,
			[]string{"zero differences over two empty results is not evidence the networks match"}, result.Options{NextActions: []string{"validate-nqe-query"}})
	}
	var limits []string
	if truncA || truncB {
		limits = append(limits, fmt.Sprintf("a result was cut at %d rows, so rows beyond it are missing from the comparison and may show as added or removed", maxCrossRows))
	}
	if len(key) > 0 {
		for _, rows := range [][]map[string]any{rowsA, rowsB} {
			for _, f := range key {
				if len(rows) > 0 {
					if _, ok := rows[0][f]; !ok {
						return result.Result{}, fmt.Errorf("%w: key column %q is not in the query's result (columns: %s)", ErrInvalidInput, f, strings.Join(columnsOf(rows[0]), ", "))
					}
				}
			}
		}
	}
	keyOf := func(r map[string]any) string {
		if len(key) == 0 {
			b, _ := json.Marshal(dropFields(r, ignore))
			return string(b)
		}
		parts := make([]any, len(key))
		for i, k := range key {
			parts[i] = r[k]
		}
		b, _ := json.Marshal(parts)
		return string(b)
	}
	index := func(rows []map[string]any) (map[string]map[string]any, int) {
		m := make(map[string]map[string]any, len(rows))
		dup := 0
		for _, r := range rows {
			k := keyOf(r)
			if _, seen := m[k]; seen {
				dup++
			}
			m[k] = dropFields(r, ignore)
		}
		return m, dup
	}
	ia, dupA := index(rowsA)
	ib, dupB := index(rowsB)
	if dupA+dupB > 0 {
		limits = append(limits, fmt.Sprintf("%d row(s) on one side or the other share a key with another row, so only the last of each key was compared: give a key that is unique, or none to compare whole rows", dupA+dupB))
	}
	var onlyA, onlyB []map[string]any
	var changed []map[string]any
	for k, ra := range ia {
		rb, ok := ib[k]
		if !ok {
			onlyA = append(onlyA, ra)
			continue
		}
		if len(key) == 0 {
			continue
		}
		var diffs []map[string]any
		for _, f := range columnsOf(ra) {
			if !jsonEqual(ra[f], rb[f]) {
				diffs = append(diffs, map[string]any{"field": f, "a": ra[f], "b": rb[f]})
			}
		}
		for _, f := range columnsOf(rb) {
			if _, ok := ra[f]; !ok {
				diffs = append(diffs, map[string]any{"field": f, "a": nil, "b": rb[f]})
			}
		}
		if len(diffs) > 0 {
			kv := map[string]any{}
			for _, kf := range key {
				kv[kf] = ra[kf]
			}
			changed = append(changed, map[string]any{"key": kv, "differences": diffs})
		}
	}
	for k, rb := range ib {
		if _, ok := ia[k]; !ok {
			onlyB = append(onlyB, rb)
		}
	}
	sortRows := func(rows []map[string]any) {
		sort.Slice(rows, func(i, j int) bool {
			x, _ := json.Marshal(rows[i])
			y, _ := json.Marshal(rows[j])
			return string(x) < string(y)
		})
	}
	sortRows(onlyA)
	sortRows(onlyB)
	sortRows(changed)
	limit := in.Limit
	if limit <= 0 {
		limit = defaultKeyShown
	}
	limit = min(limit, maxCrossShown)
	cut := func(rows []map[string]any) []map[string]any { return rows[:min(len(rows), limit)] }
	detail := map[string]any{"query_id": in.QueryID, "network_a": in.NetworkID, "snapshot_a": in.BeforeSnapshotID, "network_b": in.AfterNetworkID, "snapshot_b": in.AfterSnapshotID,
		"rows_a": len(rowsA), "rows_b": len(rowsB), "key": key, "ignored_fields": in.Ignore, "only_in_a": len(onlyA), "only_in_b": len(onlyB), "changed": len(changed),
		"only_in_a_rows": cut(onlyA), "only_in_b_rows": cut(onlyB), "changed_rows": cut(changed)}
	if len(onlyA)+len(onlyB)+len(changed) > 3*limit {
		limits = append(limits, fmt.Sprintf("the first %d of each list are shown (raise limit, at most %d)", limit, maxCrossShown))
	}
	limits = append(limits,
		"this compares the query's rows between two networks by key; it is not Forward's own snapshot diff (which works inside one network). Without key the whole row is the key, so only rows present on one side are found; give key to see rows whose other fields differ",
		"fields that are expected to differ between networks (names, ids, addresses) go in ignore, which drops them from both sides before comparing")
	finding := fmt.Sprintf("%d row(s) only in A (network %s), %d only in B (network %s)", len(onlyA), in.NetworkID, len(onlyB), in.AfterNetworkID)
	if len(key) > 0 {
		finding += fmt.Sprintf(", %d with the same key and different fields", len(changed))
	}
	finding += fmt.Sprintf("; %d rows in A, %d in B", len(rowsA), len(rowsB))
	st := result.OK
	return result.Build(compareNQEName, st, finding, result.Deterministic, cx, result.Options{Limits: limits, NextActions: []string{"inspect-inventory", "validate-nqe-query"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvNQE, "nqeCrossNetworkDiff", fwd.SnapshotIDPtr(b), detail, finding)}})
}

func trimAll(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func columnsOf(r map[string]any) []string {
	out := make([]string, 0, len(r))
	for k := range r {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func dropFields(r map[string]any, drop map[string]bool) map[string]any {
	if len(drop) == 0 {
		return r
	}
	out := make(map[string]any, len(r))
	for k, v := range r {
		if !drop[k] {
			out[k] = v
		}
	}
	return out
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
