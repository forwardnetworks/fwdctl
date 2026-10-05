package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const historyName = "inspect-history"

func init() { Register(historyName, inspectHistory) }

type historyInput struct {
	NetworkID string `json:"network_id"`
	CheckID   string `json:"check_id"`
	Device    string `json:"device"`
	Snapshots int    `json:"snapshots"`
}

const (
	defaultHistorySnapshots = 8
	maxHistorySnapshots     = 20
)

// inspectHistory answers "when did this start": one check's status and violation count on each of the last N collected snapshots,
// newest first, stopping at the snapshot where the status last differs from the newest. The work is bounded (N snapshots, one read
// each, one at a time). A snapshot that predates the check has no row for it: that is "absent", never a pass.
func inspectHistory(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in historyInput
	if err := json.Unmarshal(raw, &in); err != nil || (in.CheckID == "") == (in.Device == "") {
		return result.Result{}, fmt.Errorf("%w: give exactly one of check_id (when did a check change) or device (when did its configuration change)", ErrInvalidInput)
	}
	n := in.Snapshots
	if n <= 0 {
		n = defaultHistorySnapshots
	}
	n = min(n, maxHistorySnapshots)
	cx := result.Context{NetworkID: in.NetworkID, State: "historical"}
	all, err := s.Snapshots(ctx, in.NetworkID)
	if err != nil {
		return result.Result{}, err
	}
	var readable []string
	times := map[string]string{}
	sort.SliceStable(all, func(i, j int) bool { return snapCreated(all[i]) > snapCreated(all[j]) })
	for _, sn := range all {
		if sn.State == "PROCESSED" && !sn.Predicted() && !sn.IsDraft {
			readable = append(readable, string(sn.ID))
			times[string(sn.ID)] = snapCreated(sn)
		}
	}
	if len(readable) == 0 {
		return result.NewUnknown(historyName, "The network has no processed collected snapshot to read history from", cx,
			[]string{"no processed snapshot"}, result.Options{NextActions: []string{"inspect-snapshots"}})
	}
	if in.Device != "" {
		return deviceConfigHistory(ctx, s, in, readable, times, n, cx)
	}
	var limits []string
	readable, omitted := result.Cap(readable, n, "processed snapshots", fmt.Sprintf("only the newest were read; raise snapshots (at most %d) to look further back", maxHistorySnapshots))
	type row struct {
		Snapshot   string `json:"snapshot_id"`
		At         string `json:"at"`
		Status     string `json:"status"`
		Violations int64  `json:"violations"`
		Outdated   bool   `json:"outdated,omitempty"`
	}
	var series []row
	newest, flipped, absent := "", "", ""
	for i, id := range readable {
		list, err := s.AllChecks(ctx, id, nil)
		if err != nil {
			limits = append(limits, fmt.Sprintf("snapshot %s could not be read (%v); the series stops there", id, err))
			break
		}
		var found bool
		for _, c := range list {
			if string(c.ID) != in.CheckID {
				continue
			}
			found = true
			r := row{Snapshot: id, At: times[id], Status: c.Status, Outdated: c.Outdated != nil && *c.Outdated}
			if c.NumViolations != nil {
				r.Violations = *c.NumViolations
			}
			series = append(series, r)
			if i == 0 {
				newest = c.Status
			} else if c.Status != newest {
				flipped = id
			}
		}
		if !found {
			if i == 0 {
				return result.NewUnknown(historyName, fmt.Sprintf("Check %s is not on the newest snapshot", in.CheckID), cx,
					[]string{"no check with this id on the newest snapshot (ids are exact); list the checks first"}, result.Options{NextActions: []string{"check-network-compliance"}})
			}
			absent = id
			series = append(series, row{Snapshot: id, At: times[id], Status: "ABSENT"})
		}
		if flipped != "" || absent != "" {
			break
		}
	}
	detail := map[string]any{"check_id": in.CheckID, "series": series, "newest_status": newest}
	var finding string
	switch {
	case flipped != "":
		detail["status_last_differed_at"] = flipped
		finding = fmt.Sprintf("Check %s is %s now and was different on snapshot %s (%s); it has been %s since the next snapshot", in.CheckID, newest, flipped, times[flipped], newest)
	case absent != "":
		detail["not_present_at"] = absent
		finding = fmt.Sprintf("Check %s is %s on every snapshot since it appeared; snapshot %s predates it", in.CheckID, newest, absent)
		limits = append(limits, "a snapshot before the check existed has no row for it: absent, not passing")
	default:
		finding = fmt.Sprintf("Check %s has been %s on all %d snapshots read", in.CheckID, newest, len(series))
		limits = append(limits, "the change, if any, is older than the snapshots read")
	}
	cx.SnapshotID, cx.SnapshotTime = &readable[0], func() *string { t := times[readable[0]]; return &t }()
	return result.Build(historyName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, Omitted: omitted,
		NextActions: []string{"compare-device-config", "verify-change"},
		Evidence:    []result.Evidence{result.NewEvidence(result.EvPolicy, "checkHistory", cx.SnapshotID, detail, finding)}})
}

// deviceConfigHistory answers "when did this device's configuration last change": the newest adjacent pair of processed snapshots
// whose collected CONFIG files differ for the device. Each pair is one diff read; the walk stops at the first change.
func deviceConfigHistory(ctx context.Context, s *fwd.Session, in historyInput, readable []string, times map[string]string, n int, cx result.Context) (result.Result, error) {
	var limits []string
	readable, omitted := result.Cap(readable, n, "processed snapshots", fmt.Sprintf("only the newest were read; raise snapshots (at most %d) to look further back", maxHistorySnapshots))
	if len(readable) < 2 {
		return result.NewUnknown(historyName, "Fewer than two processed snapshots: there is nothing to compare", cx,
			[]string{"history needs at least two processed collected snapshots"}, result.Options{NextActions: []string{"inspect-snapshots"}})
	}
	// A device the newest snapshot does not hold has no history here: say so rather than report "no change".
	if files, err := s.DeviceFiles(ctx, in.NetworkID, in.Device, readable[0]); err != nil && fwd.NotFound(err) || (err == nil && len(files) == 0) {
		return result.NewUnknown(historyName, fmt.Sprintf("The newest processed snapshot holds no device %s", in.Device), cx,
			[]string{"no collected files for that device name on the newest snapshot (names are matched exactly); nothing was compared"}, result.Options{NextActions: []string{"inspect-inventory"}})
	} else if err != nil {
		return result.Result{}, err
	}
	type pair struct {
		Newer   string `json:"newer"`
		Older   string `json:"older"`
		Changed bool   `json:"changed"`
	}
	var pairs []pair
	var changedAt *pair
	for i := 0; i+1 < len(readable); i++ {
		newer, older := readable[i], readable[i+1]
		diffs, err := s.DiffFiles(ctx, older, newer, "CONFIG")
		if err != nil {
			limits = append(limits, fmt.Sprintf("the diff of snapshots %s and %s could not be read (%v); the walk stops there", older, newer, err))
			break
		}
		changed := false
		for _, d := range diffs {
			if d.Device == in.Device && (len(d.Files) > 0 || (d.HasConfigChange != nil && *d.HasConfigChange)) {
				changed = true
			}
		}
		p := pair{Newer: newer, Older: older, Changed: changed}
		pairs = append(pairs, p)
		if changed {
			changedAt = &p
			break
		}
	}
	detail := map[string]any{"device": in.Device, "pairs_compared": pairs}
	var finding string
	if changedAt != nil {
		detail["changed_between"] = map[string]any{"older": changedAt.Older, "older_at": times[changedAt.Older], "newer": changedAt.Newer, "newer_at": times[changedAt.Newer]}
		finding = fmt.Sprintf("The configuration of %s last changed between snapshot %s (%s) and snapshot %s (%s)", in.Device, changedAt.Older, times[changedAt.Older], changedAt.Newer, times[changedAt.Newer])
	} else {
		finding = fmt.Sprintf("No configuration change of %s between any of the %d adjacent pairs read", in.Device, len(pairs))
		limits = append(limits, "the change, if any, is older than the snapshots read; a device absent from a snapshot is not distinguished from an unchanged one here (inspect-inventory shows which snapshots hold it)")
	}
	limits = append(limits, "adjacent collected snapshots only: a change between two collections is attributed to the interval, not to a time inside it")
	cx.SnapshotID, cx.SnapshotTime = &readable[0], func() *string { t := times[readable[0]]; return &t }()
	return result.Build(historyName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, Omitted: omitted,
		NextActions: []string{"compare-device-config", "inspect-inventory"},
		Evidence:    []result.Evidence{result.NewEvidence(result.EvConfig, "diffFiles", cx.SnapshotID, detail, finding)}})
}
