package skills

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// The device index of a snapshot: just enough to say which devices there are and what kind.
const invDeviceIndex = `foreach device in network.devices
select { Device: device.name, Vendor: device.platform.vendor, Type: device.platform.deviceType }`

const maxCompareDevices = 200000

type invDevice struct{ name, vendor, typ string }

func readDeviceIndex(ctx context.Context, s *fwd.Session, networkID, snapshotID string) (map[string]invDevice, bool, error) {
	rows, _, trunc, err := s.RunNQEAll(ctx, networkID, snapshotID, invDeviceIndex, maxCompareDevices)
	if err != nil {
		return nil, false, err
	}
	out := make(map[string]invDevice, len(rows))
	for _, r := range rows {
		n := str(r["Device"])
		if n != "" {
			out[n] = invDevice{n, str(r["Vendor"]), str(r["Type"])}
		}
	}
	return out, trunc, nil
}

// namePrefix is the part of a device name before its first separator (`al-oxdc-fsri01` is `al`), which usually is the site or region: a quick way to see WHERE a batch of
// new devices sits.
func namePrefix(name string) string {
	if i := strings.IndexAny(name, "-_."); i > 0 {
		return name[:i]
	}
	return "(no separator)"
}

func countBy(devs []invDevice, key func(invDevice) string) map[string]int {
	m := map[string]int{}
	for _, d := range devs {
		k := key(d)
		if k == "" {
			k = "(none)"
		}
		m[k]++
	}
	return m
}

func topDeviceCountRows(m map[string]int, n int) []map[string]any { return topDeviceCounts(m, n) }

// biggest is the group with the most devices (ties by name) and its count; "" when there are none.
func biggest(m map[string]int) (string, int) {
	best, n := "", 0
	for k, v := range m {
		if v > n || (v == n && k < best) {
			best, n = k, v
		}
	}
	return best, n
}

// inventoryCompare is kind devices with compare_to_snapshot_id: which devices the snapshot has that the baseline lacks (added) and the other way (removed), with counts by vendor,
// device type and name prefix, and the net change per vendor and type. It reads only each snapshot's device list, so it is fast; it says nothing about why a device appeared.
func inventoryCompare(ctx context.Context, s *fwd.Session, in inventoryInput, cx result.Context, limits []string) (result.Result, error) {
	base, err := s.Snapshot(ctx, in.NetworkID, in.CompareToSnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	if base == nil {
		return result.NewUnknown(inventoryName, fmt.Sprintf("The network holds no snapshot %s to compare with", in.CompareToSnapshotID), cx,
			[]string{"compare_to_snapshot_id is matched exactly against this network's snapshots; nothing was read"}, result.Options{NextActions: []string{"inspect-snapshots"}})
	}
	if !fwd.IsReady(base) {
		return notReadySnapshot(inventoryName, fwd.Context(in.NetworkID, base), base, "the snapshot to compare with")
	}
	curID, baseID := fwd.SnapshotID(cx), string(base.ID)
	var cur, old map[string]invDevice
	var curTrunc, oldTrunc bool
	var errs [2]error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); cur, curTrunc, errs[0] = readDeviceIndex(ctx, s, in.NetworkID, curID) }()
	go func() { defer wg.Done(); old, oldTrunc, errs[1] = readDeviceIndex(ctx, s, in.NetworkID, baseID) }()
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			return result.Result{}, e
		}
	}
	var added, removed []invDevice
	for n, d := range cur {
		if _, ok := old[n]; !ok {
			added = append(added, d)
		}
	}
	for n, d := range old {
		if _, ok := cur[n]; !ok {
			removed = append(removed, d)
		}
	}
	byName := func(l []invDevice) { sort.Slice(l, func(i, j int) bool { return l[i].name < l[j].name }) }
	byName(added)
	byName(removed)
	names := func(l []invDevice) []string {
		out := make([]string, 0, len(l))
		for _, d := range l {
			out = append(out, d.name)
		}
		return out
	}
	page := func(l []string) []string {
		if in.Offset >= len(l) {
			return []string{}
		}
		return l[in.Offset:min(len(l), in.Offset+in.Limit)]
	}
	vendor := func(d invDevice) string { return d.vendor }
	typ := func(d invDevice) string { return d.typ }
	prefix := func(d invDevice) string { return namePrefix(d.name) }
	all := func(m map[string]invDevice) []invDevice {
		out := make([]invDevice, 0, len(m))
		for _, d := range m {
			out = append(out, d)
		}
		return out
	}
	net := func(key func(invDevice) string) []map[string]any {
		b, c := countBy(all(old), key), countBy(all(cur), key)
		seen := map[string]bool{}
		var ks []string
		for k := range b {
			seen[k] = true
			ks = append(ks, k)
		}
		for k := range c {
			if !seen[k] {
				ks = append(ks, k)
			}
		}
		sort.Slice(ks, func(i, j int) bool {
			ci, cj := c[ks[i]]-b[ks[i]], c[ks[j]]-b[ks[j]]
			if ci < 0 {
				ci = -ci
			}
			if cj < 0 {
				cj = -cj
			}
			if ci != cj {
				return ci > cj
			}
			return ks[i] < ks[j]
		})
		out := make([]map[string]any, 0, len(ks))
		for i, k := range ks {
			if i == 15 {
				break
			}
			out = append(out, map[string]any{"name": k, "baseline": b[k], "current": c[k], "change": c[k] - b[k]})
		}
		return out
	}
	detail := map[string]any{"baseline_snapshot_id": baseID, "devices": map[string]any{"baseline": len(old), "current": len(cur), "added": len(added), "removed": len(removed), "unchanged": len(cur) - len(added)},
		"added_by_vendor": topDeviceCountRows(countBy(added, vendor), 15), "added_by_type": topDeviceCountRows(countBy(added, typ), 15), "added_by_name_prefix": topDeviceCountRows(countBy(added, prefix), 15),
		"removed_by_vendor": topDeviceCountRows(countBy(removed, vendor), 15), "removed_by_type": topDeviceCountRows(countBy(removed, typ), 15), "removed_by_name_prefix": topDeviceCountRows(countBy(removed, prefix), 15),
		"net_by_vendor": net(vendor), "net_by_type": net(typ), "offset": in.Offset, "added": page(names(added)), "removed": page(names(removed))}
	if curTrunc || oldTrunc {
		limits = append(limits, fmt.Sprintf("a device list was cut at %d devices, so the counts are of the first %d", maxCompareDevices, maxCompareDevices))
	}
	if len(added) > in.Limit || len(removed) > in.Limit {
		limits = append(limits, fmt.Sprintf("added and removed list %d names each page; offset pages through them, and the counts above cover all of them", in.Limit))
	}
	limits = append(limits,
		"a device is 'added' when its name is in this snapshot and not in the baseline, and 'removed' the other way: a device renamed between the two shows as one of each, and a device that was in scope but failed to collect is absent from a snapshot's device list, so it shows as removed; nothing here says why a device appeared (a wider discovery, a newly imported source list, or a scope change), which Forward does not record on the snapshot",
		"name prefix is the text before the first '-', '_' or '.', which is often the site or region")
	finding := fmt.Sprintf("%+d devices between %s and %s: %d added, %d removed (%d then, %d now)", len(cur)-len(old), baseID, curID, len(added), len(removed), len(old), len(cur))
	if len(added) > 0 {
		if k, n := biggest(countBy(added, typ)); n > 0 {
			finding += fmt.Sprintf("; added mostly %s (%d)", k, n)
		}
		if k, n := biggest(countBy(added, vendor)); n > 0 {
			finding += fmt.Sprintf(", chiefly %s (%d)", k, n)
		}
	}
	// a device type that lost and gained about as many devices did not change in size: more likely renamed (or re-scoped) than replaced
	ad, rm := countBy(added, typ), countBy(removed, typ)
	var churn []map[string]any
	var churnNames []string
	for t, a := range ad {
		r := rm[t]
		lo, hi := min(a, r), max(a, r)
		if lo >= 50 && float64(lo) >= 0.5*float64(hi) {
			churn = append(churn, map[string]any{"device_type": t, "added": a, "removed": r, "net": a - r})
			churnNames = append(churnNames, fmt.Sprintf("%s +%d/-%d", t, a, r))
		}
	}
	sort.Slice(churn, func(i, j int) bool { return churn[i]["device_type"].(string) < churn[j]["device_type"].(string) })
	sort.Strings(churnNames)
	if len(churn) > 0 {
		detail["likely_renamed_or_rescoped"] = churn
		finding += "; LIKELY RENAMED, not new hardware: " + strings.Join(churnNames, ", ") + " (about as many removed as added of the same type)"
	}
	if len(added) == 0 && len(removed) == 0 {
		finding = fmt.Sprintf("the same %d devices in %s and %s", len(cur), baseID, curID)
	}
	return result.Build(inventoryName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits,
		NextActions: []string{"investigate-collection-failure", "inspect-collection"},
		Evidence:    []result.Evidence{result.NewEvidence(result.EvState, "runNqeQuery", cx.SnapshotID, detail, finding)}})
}

// notReadySnapshot is the answer for a snapshot that exists but cannot be read yet, in the terms of its state and with the step that fixes it, instead of a bare "no processed snapshot".
// what names the snapshot's role in the question ("the snapshot to compare with"); empty means the one asked about.
func notReadySnapshot(skill string, cx result.Context, sn *forward.Snapshot, what string) (result.Result, error) {
	subject := "The snapshot"
	if what != "" {
		subject = "The " + strings.TrimPrefix(what, "the ")
	}
	id, state := string(sn.ID), sn.State
	msg := fmt.Sprintf("%s %s is %s, so nothing can be read from it", subject, id, state)
	limits := []string{"nothing was read"}
	next := []string{"inspect-snapshots", "investigate-collection-failure"}
	switch {
	case state == "UNPROCESSED":
		msg = fmt.Sprintf("%s %s is UNPROCESSED: Forward has not built its model (it was never processed, or it was invalidated), so nothing can be read from it", subject, id)
		limits = append(limits, "edit-snapshot builds it (dry run first; the snapshot's answers are unavailable while it processes). Without processing, investigate-collection-failure view history still shows each collection's duration and device count")
		if sn.TotalDevices > 0 {
			limits = append(limits, fmt.Sprintf("Forward's snapshot list records %d devices for it, but not what they are", sn.TotalDevices))
		}
		next = []string{"edit-snapshot", "investigate-collection-failure", "inspect-snapshots"}
	case fwd.InProgress(state):
		msg = fmt.Sprintf("%s %s is still %s", subject, id, strings.ToLower(state))
		limits = append(limits, "wait for it to finish (inspect-snapshots shows its progress), then ask again")
	case state == "FAILED":
		msg = fmt.Sprintf("%s %s FAILED processing, so nothing can be read from it", subject, id)
		limits = append(limits, "investigate-collection-failure says why; edit-snapshot retries it (dry run first)")
		next = []string{"investigate-collection-failure", "edit-snapshot"}
	}
	return result.NewUnknown(skill, msg, cx, limits, result.Options{NextActions: next})
}
