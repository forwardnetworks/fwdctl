package skills

import (
	"context"
	"fmt"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const defaultRetentionRows = 20

// snapshotRetention is inspect-snapshots with retention: how Forward thins this network's snapshots (per age band: keep all, or one per day, week, month or quarter) and the
// snapshots the next cleanup pass would delete. It reads and previews only: the SDK exposes no way to run the cleanup from here.
func snapshotRetention(ctx context.Context, s *fwd.Session, in inspectSnapshotsInput, cx result.Context) (result.Result, error) {
	pol, _, err := s.Client.Networks.GetSnapshotRetentionPolicy(ctx, in.NetworkID)
	if err != nil {
		return accessError(err, cx, "read the snapshot retention policy")
	}
	var limits []string
	d := map[string]any{"enabled": pol.Enabled, "last_week": pol.LastWeek, "last_month": pol.LastMonth, "last_quarter": pol.LastQuarter, "last_year": pol.LastYear, "older": pol.Older}
	finding := "snapshot thinning is OFF for this network: every snapshot is kept by the policy"
	if pol.Enabled {
		finding = fmt.Sprintf("snapshot thinning is on: last week %s, last month %s, last quarter %s, last year %s, older %s", pol.LastWeek, pol.LastMonth, pol.LastQuarter, pol.LastYear, pol.Older)
	}
	if prev, _, perr := s.Client.Networks.PreviewSnapshotRetention(ctx, in.NetworkID); perr != nil {
		limits = append(limits, "what the next cleanup would delete could not be read (the preview needs the DELETE_SNAPSHOT permission, even though it deletes nothing): "+perr.Error())
	} else {
		n := in.Limit
		if n <= 0 {
			n = defaultRetentionRows
		}
		rows := []map[string]any{}
		for i, sn := range prev.Snapshots {
			if i == n {
				break
			}
			rows = append(rows, map[string]any{"snapshot_id": string(sn.ID), "created_at": sn.CreatedAt, "note": nilIfEmpty(sn.Note)})
		}
		d["next_cleanup_would_delete"] = map[string]any{"count": prev.Count, "shown": rows}
		finding += fmt.Sprintf("; the next cleanup would delete %d snapshot(s)", prev.Count)
		if prev.Count > len(rows) {
			limits = append(limits, fmt.Sprintf("%d of the %d snapshots the next cleanup would delete are shown; raise limit to see more", len(rows), prev.Count))
		}
	}
	limits = append(limits,
		"the policy is what Forward saved for this network, or the deployment's default when none was saved; nothing is changed here",
		"granularities: ALL keeps every snapshot of the age band, ONE_PER_DAY (TWO_DAYS, WEEK, TWO_WEEKS, MONTH, QUARTER) keeps one per period, NONE keeps none. Last week is always ALL",
		"Forward always keeps the 10 newest processed snapshots, favourite snapshots, and predictions or forks with the snapshots they derive from, whatever the policy; the preview says what remains to be deleted",
		"changing the policy is on-premises only: the SaaS policy is fixed. The organization property SNAPSHOT_AUTO_CLEANUP can switch cleanup off for the whole organization, which this view does not check (inspect-environment lists it)")
	return result.Build(inspectSnapshotsName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, NextActions: []string{"inspect-snapshots", "inspect-environment"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvState, "getSnapshotRetentionPolicy", nil, d, finding)}})
}
