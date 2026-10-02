package skills

import (
	"context"
	"fmt"
	"sort"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const maxAddressableNames = 500

// vulnDevices is view devices: one row per device with the CVEs that may affect it, from Forward's device-level route (one call), so the set of
// internet-addressable devices needs no per-CVE calls. A device with no CVE matching the filters is not in Forward's answer at all.
func vulnDevices(ctx context.Context, s *fwd.Session, in vulnInput, cx result.Context, sid *string, snapID string, limits []string) (result.Result, error) {
	opts := forward.DeviceVulnerabilityListOptions{SnapshotID: snapID}
	if in.KnownExploitedOnly {
		yes := true
		opts.Exploit = &yes
	}
	all, _, err := s.Client.Vulnerabilities.ListDevices(ctx, in.NetworkID, opts)
	if err != nil {
		return result.Result{}, err
	}
	n := len(all.Devices)
	s.Annotate(&n, false)

	type row struct {
		name, model, osv, worst  string
		addressable              *bool
		cves, exposed, unsettled int
	}
	var rows []row
	var addressable, notAddressable, unknown int
	var names []string
	for _, d := range all.Devices {
		// the severity filter is "at least", which Forward's exact-match parameter cannot say, so it is applied here to the per-severity counts
		cves, worst := 0, ""
		for sev, n := range d.SeverityToCVECount {
			if fwd.SeverityRank(string(sev)) < fwd.SeverityRank(in.MinSeverity) || n == 0 {
				continue
			}
			cves += n
			if fwd.SeverityRank(string(sev)) > fwd.SeverityRank(worst) {
				worst = string(sev)
			}
		}
		if cves == 0 {
			continue
		}
		switch {
		case d.InternetAddressable == nil:
			unknown++
		case *d.InternetAddressable:
			addressable++
			names = append(names, d.Name)
		default:
			notAddressable++
		}
		if in.InternetAddressable != nil && (d.InternetAddressable == nil || *d.InternetAddressable != *in.InternetAddressable) {
			continue
		}
		r := row{name: d.Name, model: d.Model, osv: d.OSVersion, worst: worst, addressable: d.InternetAddressable, cves: cves}
		for res, n := range d.ResultToCVECount {
			switch {
			case fwd.Exposed(string(res)):
				r.exposed += n
			case fwd.Potential(string(res)):
				r.unsettled += n
			}
		}
		rows = append(rows, r)
	}
	if in.InternetAddressable != nil && unknown > 0 && addressable+notAddressable == 0 {
		in.flow = readFlowFacts(ctx, s)
		return exposureUnknown("", in, cx, limits), nil
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if ai, aj := a.addressable != nil && *a.addressable, b.addressable != nil && *b.addressable; ai != aj {
			return ai
		}
		if ra, rb := fwd.SeverityRank(a.worst), fwd.SeverityRank(b.worst); ra != rb {
			return ra > rb
		}
		if a.exposed != b.exposed {
			return a.exposed > b.exposed
		}
		return a.name < b.name
	})
	sort.Strings(names)
	shown := rows
	if len(shown) > in.Limit {
		shown = shown[:in.Limit]
		limits = append(limits, fmt.Sprintf("%d devices match; the first %d are shown (addressable devices first, then worst severity, then most exposed CVEs); addressable_device_names lists every addressable one", len(rows), in.Limit))
		s.Annotate(nil, true)
	}
	out := make([]map[string]any, 0, len(shown))
	for _, r := range shown {
		m := map[string]any{"device": r.name, "internet_addressable": r.addressable, "cves": r.cves, "worst_severity": r.worst, "exposed_cves": r.exposed, "unsettled_cves": r.unsettled}
		if r.model != "" {
			m["model"] = r.model
		}
		if r.osv != "" {
			m["os_version"] = r.osv
		}
		out = append(out, m)
	}
	detail := map[string]any{"devices": out, "devices_with_matching_cves": addressable + notAddressable + unknown, "internet_addressable_devices": addressable,
		"not_internet_addressable_devices": notAddressable, "exposure_unknown_devices": unknown, "total_devices_analysed": all.TotalDevices}
	if len(names) > maxAddressableNames {
		limits = append(limits, fmt.Sprintf("%d addressable devices; addressable_device_names holds the first %d", len(names), maxAddressableNames))
		names = names[:maxAddressableNames]
	}
	if len(names) > 0 {
		detail["addressable_device_names"] = names
	}
	if unknown > 0 {
		limits = append(limits, fmt.Sprintf("internet_addressable is unknown on %d of the %d devices with matching CVEs: Forward gave no value, which is not 'no' (%s)", unknown, addressable+notAddressable+unknown, exposureWhy("", in.adv)))
	}
	limits = append(limits,
		"only devices with at least one CVE matching the filters are listed: a device Forward flags addressable that has none is not here, so this counts addressable devices with CVEs, not every addressable device",
		addressableCaveat)
	if all.IndexCreatedAt != "" {
		limits = append(limits, "CVE index created "+all.IndexCreatedAt)
	}
	ev := []result.Evidence{result.NewEvidence(result.EvState, "getDeviceVulnerabilities", sid, detail, "")}
	finding := fmt.Sprintf("%d of %d devices with matching CVEs are internet addressable (%d not, %d unknown)", addressable, addressable+notAddressable+unknown, notAddressable, unknown)
	if len(rows) == 0 {
		return result.NewUnknown(vulnerabilitiesName, "No device with a CVE matching the filters was returned, so nothing was assessed", cx, limits, result.Options{Evidence: ev})
	}
	exposedAddr := 0
	for _, r := range rows {
		if r.addressable != nil && *r.addressable && r.exposed > 0 {
			exposedAddr++
		}
	}
	switch {
	case exposedAddr > 0:
		return result.Build(vulnerabilitiesName, result.Failed, fmt.Sprintf("%s; %d of them carry an exposed CVE", finding, exposedAddr), result.Deterministic, cx,
			result.Options{Limits: limits, Evidence: ev, NextActions: []string{"inspect-vulnerabilities", "investigate-reachability", "plan-vulnerability-response"}})
	case unknown > 0 && addressable == 0:
		return result.NewUnknown(vulnerabilitiesName, finding, cx, limits, result.Options{Evidence: ev})
	}
	return result.Build(vulnerabilitiesName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, Evidence: ev})
}

// noDevicesViewInputs rejects the inputs that belong to another view.
func noDevicesViewInputs(in vulnInput) error {
	if in.CVEID != "" || in.Device != "" {
		return fmt.Errorf("%w: view devices lists every device; cve_id and device belong to the other views", ErrInvalidInput)
	}
	return nil
}
