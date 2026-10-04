package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const editSyntheticQueryName = "edit-synthetic-query"

func init() { Register(editSyntheticQueryName, editSyntheticQuery) }

type editSyntheticQueryInput struct {
	NetworkID string `json:"network_id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	QueryID   string `json:"query_id"`
	Detach    bool   `json:"detach"`
	// BackdateSnapshotID, when set, applies the change to that existing snapshot and every later one now (they are invalidated and reprocess)
	// instead of from the next snapshot on.
	BackdateSnapshotID string `json:"backdate_snapshot_id"`
	Apply              bool   `json:"apply"`
}

var syntheticKinds = map[string]forward.SyntheticNodeKind{
	"internet": forward.SyntheticInternet, "intranet": forward.SyntheticIntranet, "l3vpn": forward.SyntheticL3VPN,
	"l2vpn": forward.SyntheticL2VPN, "adjacent-network": forward.SyntheticAdjacentNetwork,
}

const maxSyntheticPreviewRows = 25

// editSyntheticQuery attaches a saved NQE query to a synthetic node (so Forward generates the node's connections from its rows), replaces the
// query, or detaches it. It reads the node first, previews the query with Forward's own compute (changing nothing), refuses a query that
// produces an error, and after the write reads the node back: the new query id must be there and its result error-free. The undo is the previous
// query id (or a detach). With no query_id and no detach it only lists the queries that fit the node kind.
func editSyntheticQuery(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in editSyntheticQueryInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	kind, ok := syntheticKinds[in.Kind]
	if !ok {
		return result.Result{}, fmt.Errorf("%w: kind is internet, intranet, l3vpn, l2vpn or adjacent-network", ErrInvalidInput)
	}
	in.QueryID, in.Name = strings.TrimSpace(in.QueryID), strings.TrimSpace(in.Name)
	if in.NetworkID == "" || (kind != forward.SyntheticInternet && in.Name == "") {
		return result.Result{}, fmt.Errorf("%w: network_id is required, and name for every kind except internet (it has one node)", ErrInvalidInput)
	}
	if in.Detach && in.QueryID != "" {
		return result.Result{}, fmt.Errorf("%w: give query_id or detach, not both", ErrInvalidInput)
	}
	if in.QueryID != "" && !strings.HasPrefix(in.QueryID, "Q_") && !strings.HasPrefix(in.QueryID, "FQ_") {
		return result.Result{}, fmt.Errorf("%w: query_id is a library query id: Q_... (the organization's library) or FQ_... (Forward's)", ErrInvalidInput)
	}
	mode := result.ModeDryRun
	if in.Apply {
		mode = result.ModeApplied
	}
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	next := []string{"inspect-topology", "investigate-reachability"}
	var plan *backdatePlan
	if in.BackdateSnapshotID != "" {
		var perr error
		if plan, perr = planBackdate(ctx, s, in.NetworkID, in.BackdateSnapshotID); perr != nil {
			return result.Result{}, perr
		}
		if plan == nil {
			return result.NewUnknown(editSyntheticQueryName, fmt.Sprintf("The network holds no snapshot %s to backdate to", in.BackdateSnapshotID), cx,
				[]string{"no such snapshot (ids are matched exactly); nothing was changed"}, result.Options{NextActions: []string{"inspect-snapshots"}})
		}
	}
	node, err := s.SyntheticNode(ctx, in.NetworkID, kind, in.Name)
	if err != nil {
		return result.Result{}, err
	}
	if node == nil {
		return result.NewUnknown(editSyntheticQueryName, fmt.Sprintf("The network has no %s node %q", in.Kind, in.Name), cx,
			[]string{"no such synthetic node (names are matched exactly); nothing was changed. This skill attaches a query to an existing node: create the node in Forward first"},
			result.Options{NextActions: next})
	}
	prior := node.QueryID
	ev := func(extra map[string]any) []result.Evidence {
		d := map[string]any{"kind": in.Kind, "node": node.Name, "query_before": prior, "mode": mode}
		for k, v := range extra {
			d[k] = v
		}
		return []result.Evidence{result.NewEvidence(result.EvTopology, "syntheticQuery", nil, d, "")}
	}
	preview := func(r *forward.SyntheticQueryResult) map[string]any {
		out := map[string]any{"connection_count": len(r.Connections)}
		rows := make([]map[string]any, 0)
		for i, c := range r.Connections {
			if i >= maxSyntheticPreviewRows {
				break
			}
			rows = append(rows, map[string]any{"name": c.Name, "uplink": c.UplinkPort.Device + " " + c.UplinkPort.Port, "vlan": c.Vlan, "subnets": c.Subnets})
		}
		out["connections_shown"] = rows
		if r.Error != nil {
			out["error"] = map[string]string{"status": r.Error.Status, "message": r.Error.Message}
		}
		return out
	}
	limits := []string{"this is a preview feature of Forward (not in its published API spec)", "Forward runs the query's last commit on the latest processed snapshot, recomputes when the node is saved or the query is committed or deleted, and a failure leaves the node with only its manual connections"}

	// no query and no detach: help choose one
	if in.QueryID == "" && !in.Detach {
		qs, err := s.CompatibleSyntheticQueries(ctx, in.NetworkID, kind)
		if err != nil {
			return result.Result{}, err
		}
		rows := make([]map[string]string, 0, len(qs))
		for _, q := range qs {
			rows = append(rows, map[string]string{"query_id": q.QueryID, "path": q.Path})
		}
		finding := fmt.Sprintf("%d saved query(ies) fit a %s node; the node %q currently uses %s", len(qs), in.Kind, node.Name, map[bool]string{true: prior, false: "no query"}[prior != ""])
		return result.Build(editSyntheticQueryName, result.OK, finding, result.Deterministic, cx, result.Options{Mode: mode, Evidence: ev(map[string]any{"compatible_queries": rows}),
			Limits: append(limits, "nothing was changed: give query_id (and apply) to attach one, or detach"), NextActions: []string{"author-nqe-query"}})
	}

	want := in.QueryID
	if prior == want {
		return result.Build(editSyntheticQueryName, result.OK, fmt.Sprintf("The node %q already uses %s; nothing to change", node.Name, map[bool]string{true: "that query", false: "no query"}[want != ""]),
			result.Deterministic, cx, result.Options{Mode: mode, Evidence: ev(nil), Limits: limits, NextActions: next})
	}
	var computed *forward.SyntheticQueryResult
	if want != "" {
		computed, err = s.ComputeSyntheticQuery(ctx, in.NetworkID, kind, want)
		if err != nil {
			return result.Result{}, fmt.Errorf("previewing the query failed, nothing was changed: %w", err)
		}
		if computed.Error != nil {
			return result.Build(editSyntheticQueryName, result.Failed, fmt.Sprintf("Forward cannot use that query for a %s node (%s: %s); it was not attached", in.Kind, computed.Error.Status, computed.Error.Message), result.Deterministic, cx,
				result.Options{Mode: mode, Evidence: ev(map[string]any{"query_requested": want, "preview": preview(computed)}), NextActions: []string{"author-nqe-query", "validate-nqe-query"},
					Limits: append(limits, "nothing was changed; fix the query (fwdctl nqe lint --synthetic "+in.Kind+" shows the row problems offline) or choose another")})
		}
	}
	nameField := ""
	if kind != forward.SyntheticInternet {
		nameField = fmt.Sprintf(`"name": %q, `, node.Name)
	}
	undoCall := func(body string) string {
		return fmt.Sprintf(`echo '{"network_id": %q, "kind": %q, %s%s, "apply": true}' | fwdctl run edit-synthetic-query`, in.NetworkID, in.Kind, nameField, body)
	}
	undo := "run: " + undoCall(fmt.Sprintf(`"query_id": %q`, prior))
	if prior == "" {
		undo = "run: " + undoCall(`"detach": true`)
	}
	// An undo that points at a query the library no longer holds is no undo: say so BEFORE the change, not after.
	undoPossible := true
	if prior != "" && strings.HasPrefix(prior, "Q_") {
		q, qerr := s.OrgQueryByID(ctx, prior)
		switch {
		case qerr != nil:
			limits = append(limits, "could not check whether the node's current query "+prior+" is still in the library, so whether this change can be undone is unknown: "+qerr.Error())
		case q == nil:
			undoPossible = false
			limits = append(limits, "UNDO IS NOT POSSIBLE: the node's current query "+prior+" is not in the organization's library (deleted or never committed), so after this change nothing can restore it; the node currently holds only Forward's stored result of it (see connections_before). Re-create the query by committing equivalent source first if you may need to go back")
			undo = "cannot be undone: " + prior + " is not in the library. To go back, commit equivalent source with edit-nqe-query and attach the new id"
		}
	}
	action := map[bool]string{true: "set_query", false: "detach_query"}[want != ""]
	ch := result.Change{Action: action, Target: fmt.Sprintf("%s node %s", in.Kind, node.Name), Before: prior, After: want, Reversible: undoPossible, Undo: undo}
	extra := map[string]any{"query_requested": want, "undo_possible": undoPossible}
	if computed != nil {
		extra["preview"] = preview(computed)
	}
	// What the node holds now versus what the candidate would generate, so "276 vs 272, exactly the 4 INET rows gone" is proven before attaching.
	var before []forward.SyntheticNodeConn
	if node.QueryResult != nil {
		before = node.QueryResult.Connections
	}
	if want != "" || prior != "" {
		var cand []forward.SyntheticNodeConn
		if computed != nil {
			cand = computed.Connections
		}
		extra["connections_diff"] = diffConnections(before, cand)
	}
	if plan != nil {
		extra["backdate"] = plan
		ch.Undo += "; the backdate itself cannot be undone (the snapshots it invalidated are reprocessed), but reprocessing recomputes the same data from what was collected"
		limits = append(limits, plan.Note)
	}
	if !in.Apply {
		what := fmt.Sprintf("attach %s to", want)
		if want == "" {
			what = "detach its query from"
		} else if prior != "" {
			what = fmt.Sprintf("replace %s with %s on", prior, want)
		}
		count := ""
		if computed != nil {
			count = fmt.Sprintf(" (it generates %d connection(s))", len(computed.Connections))
		}
		if plan != nil {
			count += fmt.Sprintf(", then backdate to snapshot %s (invalidating %d snapshot(s) so they reprocess now)", plan.Snapshot, len(plan.Affected))
		}
		return result.Build(editSyntheticQueryName, result.OK, fmt.Sprintf("Dry run: would %s the %s node %q%s. Nothing was changed; run again with apply=true to make it", what, in.Kind, node.Name, count),
			result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Evidence: ev(extra), Limits: limits, NextActions: next})
	}
	after, err := s.SetSyntheticQuery(ctx, in.NetworkID, kind, node.Name, want)
	if err != nil {
		return result.Result{}, fmt.Errorf("the change failed, nothing is known to have changed: %w", err)
	}
	ch.Applied = true
	if after.QueryID != want || (want != "" && after.QueryResult != nil && after.QueryResult.Error != nil) {
		if after.QueryResult != nil {
			extra["result_after"] = preview(after.QueryResult)
		}
		return result.Build(editSyntheticQueryName, result.Failed, fmt.Sprintf("Forward accepted the change but the node now holds query %q (wanted %q) or its result has an error", after.QueryID, want),
			result.Deterministic, cx, result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(extra), Limits: append(limits, "read the node to see its state; the change can be undone with the undo text")})
	}
	if after.QueryResult != nil {
		extra["result_after"] = preview(after.QueryResult)
	}
	finding := fmt.Sprintf("Applied: the %s node %q now uses %s", in.Kind, node.Name, map[bool]string{true: want, false: "no query"}[want != ""])
	if plan != nil {
		if err := s.BackdateSynthetic(ctx, in.NetworkID, in.Kind, in.BackdateSnapshotID); err != nil {
			return result.Build(editSyntheticQueryName, result.Failed, fmt.Sprintf("The query change was applied but the backdate to snapshot %s failed: %v", in.BackdateSnapshotID, err), result.Deterministic, cx,
				result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(extra), Limits: append(limits, "the node holds the new query, which applies from the next snapshot; retry the backdate or wait for one"), NextActions: []string{"inspect-snapshots"}})
		}
		finding += fmt.Sprintf("; backdated to snapshot %s (%d snapshot(s) now UNPROCESSED; Forward does not reprocess them by itself: run edit-snapshot for each one, then edit-advanced-reachability if internet exposure is needed, since that only runs after a snapshot is PROCESSED)", in.BackdateSnapshotID, len(plan.Affected))
		next = []string{"edit-snapshot", "edit-advanced-reachability", "inspect-snapshots", "inspect-topology", "investigate-reachability"}
	}
	return result.Build(editSyntheticQueryName, result.OK, finding,
		result.Deterministic, cx, result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(extra), Limits: limits, NextActions: next})
}

// connKey identifies a connection by where it attaches: uplink device and port, VRF and VLAN.
func connKey(c forward.SyntheticNodeConn) string {
	v := ""
	if c.Vlan != nil {
		v = fmt.Sprint(*c.Vlan)
	}
	return strings.Join([]string{c.UplinkPort.Device, c.UplinkPort.Port, c.VRF, v}, "|")
}

// connFingerprint is everything else about a connection that a change could touch.
func connFingerprint(c forward.SyntheticNodeConn) string {
	b, _ := json.Marshal(connRow(c))
	return string(b)
}

const maxConnDiffRows = 40

// diffConnections compares two connection sets by attachment (uplink, VRF, VLAN): which were added, which removed, and which stayed but changed
// (gateway, discovery, subnets, peers...). Counts are exact; examples are bounded and in a stable order.
func diffConnections(before, after []forward.SyntheticNodeConn) map[string]any {
	idx := func(cs []forward.SyntheticNodeConn) map[string]forward.SyntheticNodeConn {
		m := map[string]forward.SyntheticNodeConn{}
		for _, c := range cs {
			m[connKey(c)] = c
		}
		return m
	}
	b, a := idx(before), idx(after)
	var added, removed, changed []map[string]any
	for k, c := range a {
		if old, ok := b[k]; !ok {
			added = append(added, connRow(c))
		} else if connFingerprint(old) != connFingerprint(c) {
			changed = append(changed, map[string]any{"before": connRow(old), "after": connRow(c)})
		}
	}
	for k, c := range b {
		if _, ok := a[k]; !ok {
			removed = append(removed, connRow(c))
		}
	}
	order := func(rows []map[string]any, key func(map[string]any) string) []map[string]any {
		sort.Slice(rows, func(i, j int) bool { return key(rows[i]) < key(rows[j]) })
		if len(rows) > maxConnDiffRows {
			rows = rows[:maxConnDiffRows]
		}
		return rows
	}
	rk := func(r map[string]any) string { return fmt.Sprint(r["vrf"], "|", r["uplink"], "|", r["vlan"]) }
	ck := func(r map[string]any) string { return rk(r["after"].(map[string]any)) }
	return map[string]any{"before_count": len(before), "after_count": len(after),
		"added_count": len(added), "removed_count": len(removed), "changed_count": len(changed),
		"added": order(added, rk), "removed": order(removed, rk), "changed": order(changed, ck),
		"note": "connections are matched by uplink, VRF and VLAN; 'before' is the result Forward stored for the node's current query"}
}
