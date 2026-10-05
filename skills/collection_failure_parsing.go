package skills

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// allDevicesQuery reads every device's outcome and platform (not only the failed ones), so failures can be set against how many devices of that platform there are.
const allDevicesQuery = `// all devices
stage(r) =
  when r is
    collectionFailed(e) -> "collection";
    processingFailed(e) -> "processing";
    completed -> "ok";

reason(r) =
  when r is
    collectionFailed(e) -> toString(e);
    processingFailed(e) -> toString(e);
    completed -> "";

foreach device in network.devices
let result = device.snapshotInfo.result
select {
  device: device.name,
  stage: stage(result),
  reason: reason(result),
  vendor: device.platform.vendor,
  os: device.platform.os,
  osVersion: device.platform.osVersion,
  model: device.platform.model
}`

type deviceOutcome struct {
	name, stage, typ, vendor, os, osVersion, model string
}

func (d deviceOutcome) failed() bool { return d.stage == "collection" || d.stage == "processing" }

func (d deviceOutcome) category() string {
	if d.stage == "collection" {
		return fwd.FailureCategory(d.typ)
	}
	return "processing"
}

func readOutcomes(ctx context.Context, s *fwd.Session, networkID, snapshotID string) ([]deviceOutcome, bool, error) {
	rows, _, trunc, err := s.RunNQEAll(ctx, networkID, snapshotID, allDevicesQuery, maxModelRows)
	if err != nil {
		return nil, false, err
	}
	out := make([]deviceOutcome, 0, len(rows))
	for _, r := range rows {
		out = append(out, deviceOutcome{name: str(r["device"]), stage: str(r["stage"]), typ: failureType(str(r["reason"])), vendor: str(r["vendor"]), os: str(r["os"]),
			osVersion: str(r["osVersion"]), model: str(r["model"])})
	}
	return out, trunc, nil
}

// groupKey names the platform a device belongs to for the roll-up. A device that failed before Forward could identify it has no platform.
func groupKey(d deviceOutcome, by string) string {
	var parts []string
	switch by {
	case "vendor":
		parts = []string{d.vendor}
	case "os":
		parts = []string{d.vendor, d.os}
	case "os_version":
		parts = []string{d.vendor, d.os, d.osVersion}
	case "model":
		parts = []string{d.vendor, d.model}
	}
	var keep []string
	for _, p := range parts {
		if p != "" && p != "<nil>" {
			keep = append(keep, p)
		}
	}
	if len(keep) == 0 {
		return "(not identified: failed before Forward could read the platform)"
	}
	return strings.Join(keep, " ")
}

// failureRollup is view devices with group_by: failures per vendor, OS, OS version or model against the devices of that platform, which is how a parser regression on one
// software version shows up.
func failureRollup(ctx context.Context, s *fwd.Session, in collectionInput, cx result.Context) (result.Result, error) {
	switch in.GroupBy {
	case "vendor", "os", "os_version", "model":
	default:
		return result.Result{}, fmt.Errorf("%w: group_by must be vendor, os, os_version or model", ErrInvalidInput)
	}
	devs, trunc, err := readOutcomes(ctx, s, in.NetworkID, fwd.SnapshotID(cx))
	if err != nil {
		return result.Result{}, err
	}
	if len(devs) == 0 {
		return result.NewUnknown(collectionFailureName, "The snapshot's device model lists no devices", cx, []string{"nothing to group: the model is empty, which is not proof nothing failed"}, result.Options{})
	}
	type agg struct {
		total, failed int
		types         map[string]int
	}
	groups := map[string]*agg{}
	failedTotal := 0
	for _, d := range devs {
		if in.Failure != "" && d.failed() && !strings.EqualFold(d.category(), in.Failure) && !strings.EqualFold(d.typ, in.Failure) {
			continue
		}
		k := groupKey(d, in.GroupBy)
		a := groups[k]
		if a == nil {
			a = &agg{types: map[string]int{}}
			groups[k] = a
		}
		a.total++
		if d.failed() {
			a.failed++
			failedTotal++
			a.types[d.typ]++
		}
	}
	rows := make([]map[string]any, 0, len(groups))
	for k, a := range groups {
		if a.failed == 0 && in.Device == "" {
			continue
		}
		rows = append(rows, map[string]any{"group": k, "devices": a.total, "failed": a.failed, "failure_rate": fmt.Sprintf("%.0f%%", 100*float64(a.failed)/float64(a.total)), "by_type": a.types})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if a, b := rows[i]["failed"].(int), rows[j]["failed"].(int); a != b {
			return a > b
		}
		return rows[i]["group"].(string) < rows[j]["group"].(string)
	})
	var limits []string
	if trunc {
		limits = append(limits, fmt.Sprintf("the device read hit its %d-row bound; totals are of the devices read", maxModelRows))
	}
	if failedTotal == 0 {
		return result.Build(collectionFailureName, result.OK, fmt.Sprintf("No device failed among the %d in the model (grouped by %s)", len(devs), in.GroupBy), result.Deterministic, cx,
			result.Options{Limits: append(limits, "devices Forward never recorded are not in the model: the summary view compares the model with the snapshot's metrics"),
				Evidence: []result.Evidence{result.NewEvidence(result.EvCollection, "runNqeQuery", cx.SnapshotID, map[string]any{"devices": len(devs), "failed": 0, "group_by": in.GroupBy}, "")}})
	}
	win, wl, ok := window(rows, in.Limit, in.Offset, 25, 100, "groups")
	if !ok {
		return result.NewUnknown(collectionFailureName, fmt.Sprintf("Offset %d is beyond the %d groups", in.Offset, len(rows)), cx, []string{"offset is past the end of the list"}, result.Options{})
	}
	top := rows[0]
	finding := fmt.Sprintf("%d of %d devices failed; most in %s (%d of %d, %s)", failedTotal, len(devs), top["group"], top["failed"], top["devices"], top["failure_rate"])
	limits = append(limits, "failure_rate is failed devices over all devices of that platform in the snapshot: a rate near 100% on one OS version with few failures elsewhere points at that version's parser or support, not at credentials or the network",
		"a failure is the device's recorded result (collection or processing); a PARSER_EXCEPTION carries no line or message in Forward's API, so read the device's files with inspect-device-files")
	return result.Build(collectionFailureName, result.Failed, finding, result.Deterministic, cx, result.Options{Limits: limits, Omitted: wl, NextActions: []string{"inspect-device-files", "inspect-collection"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvCollection, "runNqeQuery", cx.SnapshotID, map[string]any{"devices": len(devs), "failed": failedTotal, "group_by": in.GroupBy, "offset": in.Offset, "groups": win}, finding)}})
}

// previousSnapshotID finds the newest processed, non-predicted snapshot created before sid (by creation time, so the order Forward lists them in does not matter).
func previousSnapshotID(ctx context.Context, s *fwd.Session, networkID, sid string) (string, error) {
	list, err := s.Snapshots(ctx, networkID)
	if err != nil {
		return "", err
	}
	cur := ""
	for _, sn := range list {
		if string(sn.ID) == sid {
			cur = sn.CreatedAt
		}
	}
	best, bestAt := "", ""
	for _, sn := range list {
		if string(sn.ID) == sid || sn.State != "PROCESSED" || fwd.IsPredicted(&sn) || sn.CreatedAt == "" || (cur != "" && sn.CreatedAt >= cur) {
			continue
		}
		if sn.CreatedAt > bestAt {
			best, bestAt = string(sn.ID), sn.CreatedAt
		}
	}
	return best, nil
}

// failureCompare is view devices with compare_to: which devices fail now that did not before, which recovered, and whether a new failure follows a software version change.
func failureCompare(ctx context.Context, s *fwd.Session, in collectionInput, cx result.Context) (result.Result, error) {
	before := in.CompareTo
	if before == "previous" {
		p, err := previousSnapshotID(ctx, s, in.NetworkID, fwd.SnapshotID(cx))
		if err != nil {
			return result.Result{}, err
		}
		if p == "" {
			return result.NewUnknown(collectionFailureName, "There is no earlier processed snapshot to compare with", cx, []string{"only one processed snapshot, or the earlier ones are predictions or still processing"}, result.Options{})
		}
		before = p
	}
	if before == fwd.SnapshotID(cx) {
		return result.Result{}, fmt.Errorf("%w: compare_to is the snapshot itself", ErrInvalidInput)
	}
	if sn, err := s.Snapshot(ctx, in.NetworkID, before); err != nil {
		return result.Result{}, err
	} else if sn == nil || !fwd.IsReady(sn) {
		return result.NewUnknown(collectionFailureName, fmt.Sprintf("Snapshot %s is not a processed snapshot of this network, so it cannot be compared", before), cx,
			[]string{"compare_to must be a processed snapshot of the same network"}, result.Options{})
	}
	now, t1, err := readOutcomes(ctx, s, in.NetworkID, fwd.SnapshotID(cx))
	if err != nil {
		return result.Result{}, err
	}
	was, t2, err := readOutcomes(ctx, s, in.NetworkID, before)
	if err != nil {
		return result.Result{}, err
	}
	prev := map[string]deviceOutcome{}
	for _, d := range was {
		prev[d.name] = d
	}
	var newF, still, changed []map[string]any
	curr := map[string]bool{}
	versionChanged := 0
	for _, d := range now {
		curr[d.name] = true
		p, had := prev[d.name]
		if !d.failed() {
			continue
		}
		row := map[string]any{"device": d.name, "category": d.category(), "type": d.typ, "vendor": d.vendor, "os": d.os, "os_version": d.osVersion}
		switch {
		case !had:
			row["before"] = "not in the earlier snapshot"
			newF = append(newF, row)
		case !p.failed():
			row["before"] = "ok"
			if p.osVersion != "" && p.osVersion != d.osVersion {
				row["os_version_before"] = p.osVersion
				versionChanged++
			}
			newF = append(newF, row)
		case p.typ != d.typ:
			row["before"] = p.typ
			changed = append(changed, row)
		default:
			still = append(still, row)
		}
	}
	var fixed []map[string]any
	for _, p := range was {
		if p.failed() && !curr[p.name] {
			fixed = append(fixed, map[string]any{"device": p.name, "type": p.typ, "now": "not in this snapshot"})
		} else if c := findOutcome(now, p.name); p.failed() && c != nil && !c.failed() {
			fixed = append(fixed, map[string]any{"device": p.name, "type": p.typ, "now": "ok"})
		}
	}
	sortRows := func(r []map[string]any) {
		sort.SliceStable(r, func(i, j int) bool { return str(r[i]["device"]) < str(r[j]["device"]) })
	}
	for _, r := range [][]map[string]any{newF, still, changed, fixed} {
		sortRows(r)
	}
	var limits []string
	if t1 || t2 {
		limits = append(limits, fmt.Sprintf("a device read hit its %d-row bound; the comparison covers the devices read", maxModelRows))
	}
	var omitted []result.Omission
	cap25 := func(r []map[string]any, what string) []map[string]any {
		out, om := result.Cap(r, 25, what, "the first 25 are listed")
		omitted = append(omitted, om...)
		return out
	}
	detail := map[string]any{"compared_with": before, "new_failures": cap25(newF, "new failures"), "new_failure_count": len(newF), "changed_failure": cap25(changed, "failures with a changed type"),
		"recovered": cap25(fixed, "recovered devices"), "recovered_count": len(fixed), "still_failing_count": len(still), "new_after_os_version_change": versionChanged}
	finding := fmt.Sprintf("Compared with snapshot %s: %d new failure(s), %d recovered, %d still failing, %d changed type", before, len(newF), len(fixed), len(still), len(changed))
	if versionChanged > 0 {
		finding += fmt.Sprintf("; %d of the new failures are on a device whose OS version changed", versionChanged)
	}
	limits = append(limits, "a device is 'new' when it did not fail in the earlier snapshot, including one that was not in it; the OS version comes from each snapshot's own model, so a new failure after a version change is a lead (a parser or support gap for that version), not proof")
	st := result.OK
	if len(newF)+len(changed)+len(still) > 0 {
		st = result.Failed
	}
	return result.Build(collectionFailureName, st, finding, result.Deterministic, cx, result.Options{Limits: limits, Omitted: omitted, NextActions: []string{"inspect-device-files", "inspect-history"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvCollection, "runNqeQuery", cx.SnapshotID, detail, finding)}})
}

func findOutcome(ds []deviceOutcome, name string) *deviceOutcome {
	for i := range ds {
		if ds[i].name == name {
			return &ds[i]
		}
	}
	return nil
}
