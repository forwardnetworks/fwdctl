package skills

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// invLifecycleRows reads each device's OS support dates (platform.osSupport, Forward's lifecycle data). A device Forward has no record for has no dates.
const invLifecycleRows = `foreach d in network.devices
select {
  Device: d.name,
  Vendor: d.platform.vendor,
  OS: d.platform.os,
  OSVersion: d.platform.osVersion,
  LastSupportDate: d.platform.osSupport?.lastSupportDate,
  LastMaintenanceDate: d.platform.osSupport?.lastMaintenanceDate
}`

const maxEndedShown = 25

// inventoryLifecycle is kind lifecycle: how many devices run an OS whose support has ended, ends within 12 months, runs later, or has no lifecycle record, by vendor and OS, and which devices are past
// their last support date. "No record" is its own count: Forward not knowing a date is not the same as supported.
func inventoryLifecycle(ctx context.Context, s *fwd.Session, in inventoryInput, cx result.Context, limits []string, now time.Time) (result.Result, error) {
	rows, nqeTotal, truncated, err := s.RunNQEAll(ctx, in.NetworkID, fwd.SnapshotID(cx), invLifecycleRows, 50000)
	if err != nil {
		return result.Result{}, err
	}
	if truncated {
		limits = append(limits, fmt.Sprintf("this read the first %d of %d devices, so every count here is low", len(rows), nqeTotal))
	}
	if len(rows) == 0 {
		return result.NewUnknown(inventoryName, "This snapshot has no devices, so there is no lifecycle to report", cx, limits, result.Options{NextActions: []string{"investigate-collection-failure"}})
	}
	today := now.Format("2006-01-02")
	soon := now.AddDate(1, 0, 0).Format("2006-01-02")
	type roll struct{ devices, ended, ending, later, none int }
	by, order := map[string]*roll{}, []string{}
	vendorOf := map[string]string{}
	osOf := map[string]string{}
	var ended []map[string]any
	var total, nEnded, nSoon, nLater, nNone int
	for _, r := range rows {
		key := str(r["Vendor"]) + "\x00" + str(r["OS"])
		g := by[key]
		if g == nil {
			g = &roll{}
			by[key] = g
			order = append(order, key)
			vendorOf[key], osOf[key] = str(r["Vendor"]), str(r["OS"])
		}
		g.devices++
		total++
		last := str(r["LastSupportDate"])
		switch {
		case last == "":
			g.none++
			nNone++
		case last < today:
			g.ended++
			nEnded++
			ended = append(ended, map[string]any{"device": str(r["Device"]), "vendor": str(r["Vendor"]), "os": str(r["OS"]), "os_version": str(r["OSVersion"]), "last_support_date": last})
		case last <= soon:
			g.ending++
			nSoon++
		default:
			g.later++
			nLater++
		}
	}
	sort.SliceStable(ended, func(i, j int) bool {
		return ended[i]["last_support_date"].(string) < ended[j]["last_support_date"].(string)
	})
	sort.SliceStable(order, func(i, j int) bool {
		a, b := by[order[i]], by[order[j]]
		if a.ended != b.ended {
			return a.ended > b.ended
		}
		return order[i] < order[j]
	})
	groups := make([]map[string]any, 0, len(order))
	for _, k := range order {
		g := by[k]
		groups = append(groups, map[string]any{"vendor": vendorOf[k], "os": osOf[k], "devices": g.devices, "support_ended": g.ended, "ending_within_12_months": g.ending, "supported_later": g.later, "no_record": g.none})
	}
	shown, capped := result.Cap(ended, maxEndedShown, "devices past their last support date", "the oldest 25 are listed")
	detail := map[string]any{"as_of": today, "devices": total, "support_ended": nEnded, "ending_within_12_months": nSoon, "supported_later": nLater, "no_record": nNone, "by_os": groups, "ended": shown}
	finding := fmt.Sprintf("%d of %d devices run an OS past its last support date (%d more end within 12 months, %d have no lifecycle record)", nEnded, total, nSoon, nNone)
	limits = append(limits, "dates are the OS last-support date in Forward's lifecycle data as of "+today+"; a device with no record is counted separately and is not 'supported'. Hardware (component) support dates are not included")
	return result.Build(inventoryName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, Omitted: capped, NextActions: []string{"inspect-vulnerabilities", "check-network-compliance"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvState, "runNqeQuery", cx.SnapshotID, detail, finding)}})
}
