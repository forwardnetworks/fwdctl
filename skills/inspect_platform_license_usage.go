package skills

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	forward "github.com/forwardnetworks/forward-go-sdk"
	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const licenseUsageSeriesCap = 300

// classicDevicesNote says what the device figures are, without deciding how a contract counts them.
const classicDevicesNote = "classic devices are reported as Forward counts them: STANDARD and LITE devices are separate figures with separate caps (standardDeviceCap, liteDeviceCap) and are not added here; how a contract counts lite devices toward 'classic devices' is not in Forward, so read it from the order"

// parseUsageTime reads an RFC 3339 time or a plain date (UTC midnight); empty is the zero time.
func parseUsageTime(label, v string) (time.Time, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("%w: %s %q is not an RFC 3339 time or a YYYY-MM-DD date", ErrInvalidInput, label, v)
}

// licenseUsageRows reads used against licensed for the three units a licence sells (classic devices, cloud credit hours, edge assets) and the tier.
// With org_id it reads that organization by id (Forward support, SaaS only); without it, the caller's own organization, which also has the
// per-licence CCH series over a window. Each read that fails is said in the limits and the rest still returns.
func licenseUsageRows(ctx context.Context, s *fwd.Session, in inspectPlatformInput) ([]any, []string, error) {
	start, err := parseUsageTime("start", in.Start)
	if err != nil {
		return nil, nil, err
	}
	end, err := parseUsageTime("end", in.End)
	if err != nil {
		return nil, nil, err
	}
	var rows []any
	limits := []string{classicDevicesNote,
		"read from Forward's source through the SDK (Preview routes, not in the published spec); not run against every deployment",
		"devices and edge assets have no per-licence split in Forward: their caps are summed over the active licences and their use is one series; only cloud credit hours (CCH) is attributed licence by licence"}
	lic := s.Client.Licensing
	fail := func(what string, e error) { limits = append(limits, fmt.Sprintf("%s could not be read: %v", what, e)) }

	if in.OrgID != "" {
		limits = append(limits, "organization "+in.OrgID+" read by id with a Forward support login (SaaS only); the per-licence CCH series over a window (start/end) is a read of the caller's own organization, so leave org_id out and use that organization's own login for it")
		if infos, _, e := lic.SaaSOrgInfos(ctx); e != nil {
			fail("the tier, status and summed caps", e)
		} else {
			found := false
			for _, i := range infos {
				if string(i.OrgID) != in.OrgID {
					continue
				}
				found = true
				row := map[string]any{"row": "org_license", "tier": i.Tier, "facets": i.Facets(), "status": i.Status, "next_license_expiration": nilIfEmpty(i.NextLicenseExpiration)}
				caps := map[string]any{}
				for k, v := range map[string]*int{"standard_devices": i.StandardDeviceCap, "lite_devices": i.LiteDeviceCap, "cloud_credit_hours": i.CloudCreditHours, "edge_assets": i.EdgeAssetCap} {
					if v != nil {
						caps[k] = *v
					}
				}
				row["caps_summed_over_active_licenses"] = caps
				if len(caps) == 0 {
					row["caps_note"] = "Forward gives the summed caps only for an ACTIVE organization"
				}
				rows = append(rows, row)
			}
			if !found {
				limits = append(limits, "organization "+in.OrgID+" is not in the supported SaaS organizations this login can see: no tier or caps were read")
			}
		}
		if all, _, e := lic.AllOrgUsage(ctx); e != nil {
			fail("the edge-asset and device counts", e)
		} else {
			for _, u := range all {
				if string(u.OrgID) == in.OrgID {
					rows = append(rows, map[string]any{"row": "org_used", "standard_devices_used": u.StandardDeviceCount, "lite_devices_used": u.LiteDeviceCount, "edge_assets_used": u.EdgeAssetCount, "cloud_credit_hours_used": u.CloudCreditHoursUsed})
				}
			}
		}
		if cu, _, e := lic.OrgCloudUsage(ctx, in.OrgID); e != nil {
			fail("the per-licence CCH periods", e)
		} else {
			rows = append(rows, cchPeriodRows(cu.Licenses, cu.Periods, cu.UpdatedAt)...)
		}
		return rows, limits, nil
	}

	limits = append(limits, "the caller's own organization (no org_id)")
	if u, _, e := lic.CCHUsage(ctx); e != nil {
		fail("CCH used against licensed", e)
	} else {
		row := map[string]any{"row": "cch_now", "cch_used": u.CCHUsed, "cch_licensed": u.CCHLicensed, "cch_remaining": u.Remaining(), "cch_debt": u.Over(), "as_of": nilIfEmpty(u.Instant)}
		if u.GracePeriodEndTime != "" {
			row["grace_period_end"] = u.GracePeriodEndTime
		}
		rows = append(rows, row)
	}
	if h, _, e := lic.CCHHistory(ctx, start, end); e != nil {
		fail("the per-licence CCH history", e)
	} else {
		for _, l := range h.Licenses {
			instants := make([]string, 0, len(l.Usage))
			for k := range l.Usage {
				instants = append(instants, k)
			}
			sort.Strings(instants)
			series := make([]map[string]any, 0, len(instants))
			for _, k := range instants {
				series = append(series, map[string]any{"instant": k, "cumulative_cch_used": l.Usage[k]})
			}
			shown, total := result.CapRow(series, licenseUsageSeriesCap)
			row := map[string]any{"row": "cch_license", "license_id": string(l.ID), "cch_granted": l.CCH, "replaces": l.Replaces, "replaced": l.Replaced,
				"started_at": nilIfEmpty(l.StartedAt), "expires_at": nilIfEmpty(l.ExpiresAt), "last_cumulative_cch_used": l.LastUsed(), "cch_remaining_at_last_point": l.Remaining(),
				"series": shown, "series_points_total": total}
			if total > len(shown) {
				row["series_note"] = fmt.Sprintf("the first %d of %d points; narrow start and end for the rest", len(shown), total)
			}
			rows = append(rows, row)
		}
		limits = append(limits, "cumulative_cch_used is each licence's running total at each instant (what Forward's available-CCH chart draws); cch_remaining_at_last_point is the granted amount minus the last total in the window, so for a replaced licence it is what was left when it was replaced. The window is start/end; an empty start begins at the first usage")
	}
	if dc, _, e := lic.DeviceCounts(ctx); e != nil {
		fail("device counts", e)
	} else {
		rows = append(rows, map[string]any{"row": "devices", "standard_licensed": dc.StandardDeviceCount, "standard_unlicensed": dc.StandardUnlicensedDeviceCount, "standard_cap": dc.StandardDeviceCap,
			"lite_licensed": dc.LiteDeviceCount, "lite_unlicensed": dc.LiteUnlicensedDeviceCount, "lite_cap": dc.LiteDeviceCap})
	}
	if ea, _, e := lic.EdgeAssetCounts(ctx); e != nil {
		fail("edge asset counts", e)
	} else {
		rows = append(rows, map[string]any{"row": "edge_assets", "licensed": ea.EdgeAssetCount, "unlicensed": ea.UnlicensedEdgeAssetCount, "cap": ea.EdgeAssetCap})
	}
	if !start.IsZero() && !end.IsZero() {
		if pts, _, e := lic.UsageHistory(ctx, start, end); e != nil {
			fail("the device and edge-asset use over time", e)
		} else {
			series := make([]map[string]any, 0, len(pts))
			for _, p := range pts {
				series = append(series, map[string]any{"time": p.Time().Format(time.RFC3339), "standard_used": p.StandardDeviceUsage, "standard_cap": p.StandardDeviceCap, "lite_used": p.LiteDeviceUsage, "lite_cap": p.LiteDeviceCap, "edge_assets_used": p.EdgeAssetUsage, "edge_assets_cap": p.EdgeAssetCap})
			}
			shown, total := result.CapRow(series, licenseUsageSeriesCap)
			rows = append(rows, map[string]any{"row": "device_and_edge_use_over_time", "points": shown, "points_total": total})
		}
	} else {
		limits = append(limits, "device and edge-asset use over time is read only when both start and end are given")
	}
	return rows, limits, nil
}

// cchPeriodRows turns the stored periods of an organization into one row per CCH licence with the CCH used in each period (by id, support login).
func cchPeriodRows(lics []forward.CCHLicense, periods []forward.CCHPeriod, updatedAt string) []any {
	byLicense := map[string][]map[string]any{}
	order := []string{}
	for _, p := range periods {
		id := string(p.LicenseID)
		if _, ok := byLicense[id]; !ok {
			order = append(order, id)
		}
		byLicense[id] = append(byLicense[id], map[string]any{"start": p.Start, "cch_used": p.CCHUsed, "end_reason": nilIfEmpty(p.EndReason), "grace_period": p.GracePeriod})
	}
	var rows []any
	seen := map[string]bool{}
	add := func(l forward.CCHLicense) {
		id := string(l.ID)
		seen[id] = true
		var sum float64
		for _, p := range byLicense[id] {
			sum += p["cch_used"].(float64)
		}
		shown, total := result.CapRow(byLicense[id], licenseUsageSeriesCap)
		rows = append(rows, map[string]any{"row": "cch_license", "license_id": id, "cch_granted": l.CCH, "replaces": l.Replaces, "started_at": nilIfEmpty(l.StartedAt), "expires_at": nilIfEmpty(l.ExpiresAt),
			"cch_used_in_periods": sum, "periods": shown, "periods_total": total, "as_of": nilIfEmpty(updatedAt)})
	}
	for _, l := range lics {
		add(l)
	}
	for _, id := range order {
		if !seen[id] {
			unattributed := "a period with no licence (CCH used while no CCH licence was active)"
			if id != "" {
				unattributed = "periods of a licence Forward no longer lists"
			}
			shown, total := result.CapRow(byLicense[id], licenseUsageSeriesCap)
			rows = append(rows, map[string]any{"row": "cch_unattributed", "license_id": nilIfEmpty(id), "what": unattributed, "periods": shown, "periods_total": total})
		}
	}
	return rows
}
