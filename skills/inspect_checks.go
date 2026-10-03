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

const inspectChecksName = complianceName

type inspectChecksInput struct {
	NetworkID  string   `json:"network_id"`
	SnapshotID string   `json:"snapshot_id"`
	CheckID    string   `json:"check_id"`
	Statuses   []string `json:"statuses"`
	Catalogue  bool     `json:"catalogue"`
	Limit      int      `json:"limit"`
	Offset     int      `json:"offset"`
}

// inspectChecks lists the checks that exist on a snapshot and how they stand, one check in detail with Forward's diagnosis, or
// the catalogue of predefined checks a new one can be made from. It creates and changes nothing.
func inspectChecks(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in inspectChecksInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	if in.Catalogue {
		return checksCatalogue(ctx, s, in, cx)
	}
	sn, err := resolveSnapshot(ctx, s, in.NetworkID, in.SnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	if sn == nil {
		return result.NewUnknown(inspectChecksName, "The network has no processed snapshot to read checks from", cx,
			[]string{"no processed snapshot: checks are evaluated per snapshot"}, result.Options{NextActions: []string{"inspect-snapshots"}})
	}
	cx = fwd.Context(in.NetworkID, sn)
	sid := string(sn.ID)
	if in.CheckID != "" {
		return checkDetail(ctx, s, in, sid, cx)
	}
	list, err := s.AllChecks(ctx, sid, in.Statuses)
	if err != nil {
		return result.Result{}, err
	}
	if len(list) == 0 {
		return result.NewUnknown(inspectChecksName, "No checks were returned for this snapshot", cx,
			[]string{"an empty list is not health: no checks are defined on this snapshot, the status filter matched none, or the network's checks are not readable by this login"},
			result.Options{NextActions: []string{"check-network-compliance"}})
	}
	byStatus := map[string]int{}
	failing, undetermined := 0, 0
	rows := make([]map[string]any, 0, len(list))
	sort.SliceStable(list, func(i, j int) bool { return checkRank(list[i]) < checkRank(list[j]) })
	for _, c := range list {
		byStatus[c.Status]++
		switch checkRank(c) {
		case 0:
			failing++
		case 1:
			undetermined++
		}
		rows = append(rows, checkRow(c))
	}
	win, limits, ok := window(rows, in.Limit, in.Offset, 25, 200, "checks")
	if !ok {
		return result.NewUnknown(inspectChecksName, fmt.Sprintf("Offset %d is beyond the %d checks", in.Offset, len(rows)), cx,
			[]string{"offset is past the end of the list"}, result.Options{})
	}
	detail := map[string]any{"total": len(list), "by_status": byStatus, "offset": in.Offset, "checks": win}
	// ERROR and TIMEOUT mean Forward produced no verdict: they are not failures of policy, and never a pass.
	note := ""
	if undetermined > 0 {
		note = fmt.Sprintf("%d not evaluated (%s)", undetermined, notEvaluatedBreakdown(byStatus))
		limits = append(limits, "a check that is ERROR or TIMEOUT has no verdict: it is neither a failure nor a pass; read it with check_id for Forward's diagnosis. Some checks (for example pre/post diffs) need a reference snapshot")
	}
	st, conf := result.OK, result.Deterministic
	finding := fmt.Sprintf("%d checks on snapshot %s; none failing", len(list), sid)
	switch {
	case failing > 0:
		st = result.Failed
		finding = fmt.Sprintf("%d of %d checks are failing on snapshot %s", failing, len(list), sid)
		if note != "" {
			finding += "; " + note
		}
	case undetermined > 0:
		return result.NewUnknown(inspectChecksName, fmt.Sprintf("%d checks on snapshot %s; none failing, but %s", len(list), sid, note), cx, limits,
			result.Options{NextActions: []string{"check-network-compliance", "inspect-snapshots"},
				Evidence: []result.Evidence{result.NewEvidence(result.EvPolicy, "listChecks", cx.SnapshotID, detail, finding)}})
	}
	return result.Build(inspectChecksName, st, finding, conf, cx, result.Options{Limits: limits,
		NextActions: []string{"check-network-compliance", "verify-change"},
		Evidence:    []result.Evidence{result.NewEvidence(result.EvPolicy, "listChecks", cx.SnapshotID, detail, finding)}})
}

// checkRank orders checks for reading: 0 failing, 1 no verdict (ERROR, TIMEOUT and the other unevaluated statuses), 2 the rest.
// A disabled check is never ranked as failing or undetermined.
func checkRank(c forward.Check) int {
	switch {
	case c.Enabled != nil && !*c.Enabled:
		return 2
	case c.Status == "FAIL":
		return 0
	case c.Status == "ERROR" || c.Status == "TIMEOUT":
		return 1
	}
	return 2
}

func notEvaluatedBreakdown(byStatus map[string]int) string {
	var parts []string
	for _, st := range []string{"ERROR", "TIMEOUT"} {
		if n := byStatus[st]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, strings.ToLower(st)))
		}
	}
	return strings.Join(parts, ", ")
}

// needsReference reports whether a check reads like a diff against another snapshot, which cannot evaluate without one.
func needsReference(d *forward.CheckDetail) bool {
	text := strings.ToLower(d.Name + " " + d.Description + " " + d.Note + " " + string(d.Definition))
	return strings.Contains(text, "pre snapshot") || strings.Contains(text, "post snapshot") || strings.Contains(text, "diff")
}

func checkRow(c forward.Check) map[string]any {
	row := map[string]any{"id": string(c.ID), "name": c.Name, "status": c.Status, "priority": c.Priority}
	if c.NumViolations != nil {
		row["violations"] = *c.NumViolations
	}
	if c.Enabled != nil && !*c.Enabled {
		row["disabled"] = true
	}
	if c.Outdated != nil && *c.Outdated {
		row["outdated"] = true
	}
	if len(c.Tags) > 0 {
		row["tags"] = c.Tags
	}
	return row
}

func checkDetail(ctx context.Context, s *fwd.Session, in inspectChecksInput, sid string, cx result.Context) (result.Result, error) {
	d, err := s.CheckDetail(ctx, sid, in.CheckID)
	if fwd.NotFound(err) {
		return result.NewUnknown(inspectChecksName, fmt.Sprintf("Snapshot %s has no check %s", sid, in.CheckID), cx,
			[]string{"no such check on this snapshot (ids are matched exactly); list the checks first"}, result.Options{})
	}
	if err != nil {
		return result.Result{}, err
	}
	row := checkRow(d.Check)
	row["description"] = d.Description
	row["note"] = d.Note
	row["executed_at"] = d.ExecutedAt
	if d.Diagnosis != nil {
		row["diagnosis"] = d.Diagnosis
	}
	var limits []string
	if d.Outdated != nil && *d.Outdated {
		limits = append(limits, "the result was computed against an older definition of this check")
	}
	st := result.OK
	finding := fmt.Sprintf("Check %q is %s", d.Name, d.Status)
	if d.Status == "FAIL" {
		st = result.Failed
	}
	if d.Diagnosis != nil {
		var kinds []string
		for _, det := range d.Diagnosis.Details {
			kinds = append(kinds, det.FlowTypes()...)
		}
		if len(kinds) > 0 {
			limits = append(limits, fmt.Sprintf("the diagnosis names violation kind(s) %s and no flow or device (a loop violation carries none): Forward gives no witness for it here, so finding one means path searches (investigate-reachability)", strings.Join(kinds, ", ")))
		}
	}
	if d.Status == "ERROR" || d.Status == "TIMEOUT" {
		reason := ""
		if d.Diagnosis != nil {
			reason = strings.TrimSpace(d.Diagnosis.Summary)
		}
		if reason != "" {
			limits = append(limits, fmt.Sprintf("status %s: Forward produced no verdict, so this is neither a failure nor a pass; Forward's reason: %s", d.Status, reason))
		} else {
			limits = append(limits, fmt.Sprintf("status %s: Forward produced no verdict, so this is neither a failure nor a pass; Forward recorded no reason for it (some ERROR paths store none)", d.Status))
		}
		if needsReference(d) {
			limits = append(limits, "this check appears to compare against another snapshot (pre/post diff); it can ERROR where no reference snapshot applies")
		}
		return result.NewUnknown(inspectChecksName, fmt.Sprintf("Check %q has no verdict on this snapshot (%s)", d.Name, d.Status), cx, limits,
			result.Options{NextActions: []string{"inspect-snapshots", "check-network-compliance"}, Evidence: []result.Evidence{result.NewEvidence(result.EvPolicy, "getCheck", cx.SnapshotID, row, "")}})
	}
	if strings.EqualFold(d.Status, "NONE") || d.Status == "" {
		return result.NewUnknown(inspectChecksName, fmt.Sprintf("Check %q has no result on this snapshot", d.Name), cx,
			append(limits, "the check has not been evaluated"), result.Options{Evidence: []result.Evidence{result.NewEvidence(result.EvPolicy, "getCheck", cx.SnapshotID, row, "")}})
	}
	return result.Build(inspectChecksName, st, finding, result.Deterministic, cx, result.Options{Limits: limits,
		NextActions: []string{"check-network-compliance"},
		Evidence:    []result.Evidence{result.NewEvidence(result.EvPolicy, "getCheck", cx.SnapshotID, row, finding)}})
}

func checksCatalogue(ctx context.Context, s *fwd.Session, in inspectChecksInput, cx result.Context) (result.Result, error) {
	cat, err := s.PredefinedChecks(ctx)
	if err != nil {
		return result.Result{}, err
	}
	if len(cat) == 0 {
		return result.NewUnknown(inspectChecksName, "Forward returned no predefined checks", cx,
			[]string{"an empty catalogue is not credible for a Forward build; the login may lack access"}, result.Options{})
	}
	rows := make([]map[string]any, 0, len(cat))
	for _, c := range cat {
		rows = append(rows, map[string]any{"name": c.Name, "type": c.PredefinedCheckType, "description": c.Description})
	}
	win, limits, ok := window(rows, in.Limit, in.Offset, 50, 200, "predefined checks")
	if !ok {
		return result.NewUnknown(inspectChecksName, fmt.Sprintf("Offset %d is beyond the %d predefined checks", in.Offset, len(rows)), cx,
			[]string{"offset is past the end of the list"}, result.Options{})
	}
	finding := fmt.Sprintf("%d predefined checks a new check can be made from", len(cat))
	return result.Build(inspectChecksName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits,
		Evidence: []result.Evidence{result.NewEvidence(result.EvPolicy, "listPredefinedChecks", nil, map[string]any{"total": len(cat), "predefined": win}, finding)}})
}
