package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const vulnerabilitiesName = "inspect-vulnerabilities"

func init() { Register(vulnerabilitiesName, investigateVulnerabilities) }

type vulnInput struct {
	NetworkID           string `json:"network_id"`
	SnapshotID          string `json:"snapshot_id"`
	CVEID               string `json:"cve_id"`
	Device              string `json:"device"`
	MinSeverity         string `json:"min_severity"`
	KnownExploitedOnly  bool   `json:"known_exploited_only"`
	InternetAddressable *bool  `json:"internet_addressable"`
	Limit               int    `json:"limit"`
	// View is "cves" (the default: the CVEs, worst first) or "devices" (one row per device, with its internet_addressable flag).
	View string `json:"view"`

	// adv is the snapshot's advanced reachability state, read from the snapshot (not an input)
	adv string
	// flow is what the organization properties say about flow computation (read only when internet exposure is unavailable)
	flow flowFacts
}

func (in vulnInput) keep(c fwd.CVE) bool {
	if in.MinSeverity != "" && fwd.SeverityRank(c.Severity) < fwd.SeverityRank(in.MinSeverity) {
		return false
	}
	return !in.KnownExploitedOnly || c.KnownExploit
}

// addressableCaveat is said wherever internet_addressable is read: the flag is one yes or no per device, so it must not be read as "open".
const addressableCaveat = "internet_addressable is per DEVICE, not per address or service: Forward flags a device when a valid path from the internet ends on any one of its interfaces (it keeps one arbitrary qualifying interface and does not say which), so a flagged load balancer or firewall can still deny a particular VIP or port. Read it as 'can receive some traffic from the internet', not as 'open': investigate-reachability from the internet to the exact address and port says whether that flow is delivered or denied"

const (
	listedDescChars = 130
	singleDescChars = 300
)

// cveDetail is one CVE as evidence. A list of them is the largest result any skill returns, so a listed CVE keeps only
// the start of its description (enough to recognise it); a single CVE keeps more.
func cveDetail(c fwd.CVE, descChars int) map[string]any {
	m := map[string]any{"cve": c.ID, "severity": c.Severity, "known_exploit": c.KnownExploit, "results": c.Results,
		"exposed_devices": c.ExposedDevices(), "unsettled_devices": c.PotentialDevices()}
	if c.TopScore != nil {
		m["top_score"] = *c.TopScore
	}
	if c.Description != "" {
		d := c.Description
		if len(d) > descChars {
			d = d[:descChars] + "..."
		}
		m["description"] = strings.TrimSpace(d)
	}
	return m
}

func investigateVulnerabilities(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in vulnInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if in.CVEID != "" && in.Device != "" {
		return result.NewError(vulnerabilitiesName, "give cve_id or device, not both", fwd.Context(in.NetworkID, nil)), nil
	}
	switch in.View {
	case "", "cves":
	case "devices":
		if err := noDevicesViewInputs(in); err != nil {
			return result.Result{}, err
		}
	default:
		return result.Result{}, fmt.Errorf("%w: view is cves or devices", ErrInvalidInput)
	}
	if in.Limit <= 0 {
		in.Limit = 25
	}
	snap, err := resolveSnapshot(ctx, s, in.NetworkID, in.SnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	cx := fwd.Context(in.NetworkID, snap)
	if !fwd.IsReady(snap) {
		return result.NewUnknown(vulnerabilitiesName, "No processed snapshot is available to analyse", cx,
			[]string{"no processed snapshot; nothing was analysed"}, result.Options{NextActions: []string{"investigate-collection-failure"}})
	}
	snapID := fwd.SnapshotID(cx)
	sid := fwd.SnapshotIDPtr(snap)
	in.adv = advancedState(*snap)
	var limits []string
	if cx.State == "predicted" {
		limits = append(limits, "analysed a predicted snapshot, not collected state")
	}
	switch {
	case in.View == "devices":
		return vulnDevices(ctx, s, in, cx, sid, snapID, limits)
	case in.CVEID != "":
		return vulnOneCVE(ctx, s, in, cx, sid, snapID, limits)
	case in.Device != "":
		return vulnOneDevice(ctx, s, in, cx, sid, snapID, limits)
	}
	return vulnNetwork(ctx, s, in, cx, sid, snapID, limits)
}

func vulnNetwork(ctx context.Context, s *fwd.Session, in vulnInput, cx result.Context, sid *string, snapID string, limits []string) (result.Result, error) {
	all, index, err := s.NetworkVulnerabilities(ctx, in.NetworkID, snapID, in.InternetAddressable)
	if code, ok := exposureUnavailable(err); ok {
		in.flow = readFlowFacts(ctx, s)
		return exposureUnknown(code, in, cx, limits), nil
	}
	if err != nil {
		return result.Result{}, err
	}
	if len(all) == 0 {
		limits = append(limits, "Forward returned no CVEs; that is either a clean network or a missing CVE index, and this skill cannot tell which")
		return result.NewUnknown(vulnerabilitiesName, "No CVE analysis was returned, so exposure is undetermined", cx, limits, result.Options{})
	}
	var kept []fwd.CVE
	for _, c := range all {
		if in.keep(c) {
			kept = append(kept, c)
		}
	}
	exposed, unsettled := 0, 0
	for _, c := range kept {
		if c.ExposedDevices() > 0 {
			exposed++
		} else if c.PotentialDevices() > 0 {
			unsettled++
		}
	}
	if index != "" {
		limits = append(limits, "CVE index created "+index)
	}
	if in.InternetAddressable != nil && *in.InternetAddressable {
		limits = append(limits, "only CVE results on internet-addressable devices are counted here; "+addressableCaveat)
		if l := inertInternetNodeLimit(ctx, s, in.NetworkID); l != "" {
			limits = append(limits, l)
		}
	}
	shown := kept
	if len(shown) > in.Limit {
		shown = shown[:in.Limit]
		limits = append(limits, fmt.Sprintf("%d CVEs match; the %d worst are shown", len(kept), in.Limit))
		s.Annotate(nil, true)
	}
	var ev []result.Evidence
	for _, c := range shown {
		if c.ExposedDevices() == 0 && c.PotentialDevices() == 0 && exposed > 0 {
			continue
		}
		ev = append(ev, result.NewEvidence(result.EvState, "getVulnerabilities", sid, cveDetail(c, listedDescChars),
			fmt.Sprintf("%s %s: %d exposed, %d not settled", c.ID, c.Severity, c.ExposedDevices(), c.PotentialDevices())))
	}
	if len(ev) == 0 && len(kept) > 0 {
		ev = append(ev, result.NewEvidence(result.EvState, "getVulnerabilities", sid, cveDetail(kept[0], singleDescChars), kept[0].ID))
	}
	if len(kept) == 0 {
		return result.NewUnknown(vulnerabilitiesName, "No CVE matches the filters, so nothing was assessed", cx,
			append(limits, fmt.Sprintf("%d CVEs were returned; none met the filters", len(all))), result.Options{})
	}
	switch {
	case exposed > 0:
		names := make([]string, 0, 3)
		for _, c := range kept {
			if c.ExposedDevices() > 0 && len(names) < 3 {
				names = append(names, fmt.Sprintf("%s (%s, %d devices)", c.ID, c.Severity, c.ExposedDevices()))
			}
		}
		return result.Build(vulnerabilitiesName, result.Failed,
			fmt.Sprintf("%d CVE(s) expose devices; worst: %s", exposed, strings.Join(names, ", ")),
			result.Deterministic, cx, result.Options{Limits: limits, Evidence: ev, NextActions: []string{"plan-vulnerability-response", "investigate-reachability", "verify-change"}})
	case unsettled > 0:
		return result.NewUnknown(vulnerabilitiesName, fmt.Sprintf("%d CVE(s) may affect devices but Forward could not settle them", unsettled), cx,
			append(limits, "UNCONFIRMED and UNIMPLEMENTED results are not exposure and not a pass: they depend on configuration Forward could not confirm or has no analysis for"),
			result.Options{Evidence: ev})
	}
	return result.Build(vulnerabilitiesName, result.OK, fmt.Sprintf("No device is exposed to any of the %d CVE(s) assessed", len(kept)),
		result.Deterministic, cx, result.Options{Limits: limits, Evidence: ev})
}

func vulnOneCVE(ctx context.Context, s *fwd.Session, in vulnInput, cx result.Context, sid *string, snapID string, limits []string) (result.Result, error) {
	d, found, err := s.CVEDetail(ctx, in.NetworkID, in.CVEID, snapID)
	if err != nil {
		return result.Result{}, err
	}
	if !found {
		return result.NewUnknown(vulnerabilitiesName, in.CVEID+" is not among the CVEs Forward found that may affect this network", cx,
			append(limits, "Forward answers not-found both for a CVE that does not apply and for one absent from its CVE index; this skill cannot tell which"), result.Options{})
	}
	exposed, unsettled := 0, 0
	devs := d.Devices
	nilRows, addressable := 0, 0
	for _, dv := range d.Devices {
		if dv.InternetAddressable == nil {
			nilRows++
		} else if *dv.InternetAddressable {
			addressable++
		}
	}
	if in.InternetAddressable != nil && len(d.Devices) > 0 && nilRows == len(d.Devices) {
		in.flow = readFlowFacts(ctx, s)
		return exposureUnknown("", in, cx, limits), nil
	}
	if nilRows > 0 {
		limits = append(limits, fmt.Sprintf("internet_addressable is null on %d of %d device rows: Forward gave no value, which is not 'no' (%s)", nilRows, len(d.Devices), exposureWhy("", in.adv)))
	}
	if in.InternetAddressable != nil {
		kept := devs[:0:0]
		for _, dv := range devs {
			if dv.InternetAddressable != nil && *dv.InternetAddressable == *in.InternetAddressable {
				kept = append(kept, dv)
			}
		}
		devs = kept
	}
	for _, dv := range devs {
		if fwd.Exposed(dv.Result) {
			exposed++
		} else if fwd.Potential(dv.Result) {
			unsettled++
		}
	}
	sort.SliceStable(devs, func(i, j int) bool { return fwd.Exposed(devs[i].Result) && !fwd.Exposed(devs[j].Result) })
	shown := devs
	if len(shown) > in.Limit {
		shown = shown[:in.Limit]
		limits = append(limits, fmt.Sprintf("%d devices are affected; %d shown", len(devs), in.Limit))
		if in.InternetAddressable == nil && nilRows < len(d.Devices) {
			limits = append(limits, fmt.Sprintf("the %d shown are ordered by vulnerability verdict, not by internet exposure, so their internet_addressable flags say nothing about the rest: %d of the %d affected devices are internet addressable; give internet_addressable: true to list those", in.Limit, addressable, len(d.Devices)))
		}
	}
	if addressable > 0 || (in.InternetAddressable != nil && *in.InternetAddressable) {
		limits = append(limits, addressableCaveat)
	}
	detail := cveDetail(d.CVE, singleDescChars)
	if nilRows < len(d.Devices) {
		detail["internet_addressable_devices"] = addressable
	}
	rows := make([]map[string]any, 0, len(shown))
	for _, dv := range shown {
		rows = append(rows, map[string]any{"device": dv.Name, "os": dv.OS, "os_version": dv.OSVersion, "result": dv.Result,
			"status": dv.Status, "internet_addressable": dv.InternetAddressable, "config_evidence": dv.Config})
	}
	detail["devices"] = rows
	ev := []result.Evidence{result.NewEvidence(result.EvState, "getVulnerability", sid, detail,
		fmt.Sprintf("%s: %d exposed, %d not settled of %d devices", d.CVE.ID, exposed, unsettled, len(devs)))}
	switch {
	case len(devs) == 0:
		return result.NewUnknown(vulnerabilitiesName, "Forward lists no affected devices for "+in.CVEID, cx,
			append(limits, "the CVE has no device verdicts (or none after the filters); nothing was assessed"), result.Options{Evidence: ev})
	case exposed > 0:
		return result.Build(vulnerabilitiesName, result.Failed,
			fmt.Sprintf("%s (%s) exposes %d device(s)", d.CVE.ID, d.CVE.Severity, exposed), result.Deterministic, cx,
			result.Options{Limits: limits, Evidence: ev, NextActions: []string{"verify-change"}})
	case unsettled > 0:
		return result.NewUnknown(vulnerabilitiesName, fmt.Sprintf("%s may affect %d device(s) but Forward could not settle it", d.CVE.ID, unsettled), cx,
			append(limits, "UNCONFIRMED and UNIMPLEMENTED are not exposure and not a pass"), result.Options{Evidence: ev})
	}
	return result.Build(vulnerabilitiesName, result.OK, fmt.Sprintf("No device is exposed to %s (%d assessed)", d.CVE.ID, len(devs)),
		result.Deterministic, cx, result.Options{Limits: limits, Evidence: ev})
}

const deviceCVEsSummary = `@query
query(deviceName: String) =
foreach device in network.devices
where device.name == deviceName
let vulnerable = (foreach finding in device.cveFindings where finding.isVulnerable select finding.cveId)
select {
  Device: device.name,
  Vendor: device.platform.vendor,
  OS: device.platform.os,
  Findings: length(device.cveFindings),
  Vulnerable: length(vulnerable)
};
`

const deviceCVEsDetail = `import "@fwd/Security/CVEs/CVE Utilities";

@query
query(deviceName: String) =
foreach device in network.devices
where device.name == deviceName
foreach finding in device.cveFindings
where finding.isVulnerable
let cve = getCve(finding.cveId)
select {
  CVE: finding.cveId,
  Basis: finding.basis,
  Severity: cve.severity,
  KnownExploit: hasKnownExploit(cve),
  Description: cve.description
};
`

func asString(r map[string]json.RawMessage, k string) string {
	var s string
	_ = json.Unmarshal(r[k], &s)
	return s
}

func asInt(r map[string]json.RawMessage, k string) int {
	var n int
	_ = json.Unmarshal(r[k], &n)
	return n
}

func vulnOneDevice(ctx context.Context, s *fwd.Session, in vulnInput, cx result.Context, sid *string, snapID string, limits []string) (result.Result, error) {
	if in.InternetAddressable != nil {
		limits = append(limits, "internet_addressable is not applied to the single-device view: it is read in the network view and per device in the cve_id view")
	}
	params := map[string]any{"deviceName": in.Device}
	sum, err := s.RunNQE(ctx, in.NetworkID, fwd.NQERun{Query: deviceCVEsSummary, Parameters: params, SnapshotID: snapID, Limit: 1})
	if errors.Is(err, fwd.ErrSnapshotNotReady) {
		return result.NewUnknown(vulnerabilitiesName, "The snapshot became unavailable; nothing was analysed", cx, []string{err.Error()}, result.Options{})
	}
	if err != nil {
		return result.Result{}, err
	}
	if len(sum.Items) == 0 {
		return result.NewUnknown(vulnerabilitiesName, "No device named "+in.Device+" is in this snapshot", cx,
			append(limits, "the device was not found in the snapshot; nothing was assessed"), result.Options{})
	}
	row := sum.Items[0]
	findings, vulnerable := asInt(row, "Findings"), asInt(row, "Vulnerable")
	head := map[string]any{"device": in.Device, "vendor": asString(row, "Vendor"), "os": asString(row, "OS"), "findings": findings, "vulnerable": vulnerable}
	if findings == 0 {
		return result.NewUnknown(vulnerabilitiesName, in.Device+" has no CVE findings, so its exposure is undetermined", cx,
			append(limits, "Forward recorded no CVE finding for this device, which can mean no CVE analysis exists for its platform; it is not evidence that the device is clean"),
			result.Options{Evidence: []result.Evidence{result.NewEvidence(result.EvNQE, "runNqeQuery", sid, head, "no findings")}})
	}
	if vulnerable == 0 {
		return result.Build(vulnerabilitiesName, result.OK, fmt.Sprintf("%s is not vulnerable to any of the %d CVE(s) Forward assessed", in.Device, findings),
			result.Deterministic, cx, result.Options{Limits: limits, Evidence: []result.Evidence{result.NewEvidence(result.EvNQE, "runNqeQuery", sid, head, "0 vulnerable")}})
	}
	det, err := s.RunNQE(ctx, in.NetworkID, fwd.NQERun{Query: deviceCVEsDetail, Parameters: params, SnapshotID: snapID, Limit: 1000})
	if err != nil {
		return result.Result{}, err
	}
	type row2 struct {
		cve, basis, sev, desc string
		kev                   bool
	}
	var rows []row2
	for _, r := range det.Items {
		x := row2{cve: asString(r, "CVE"), basis: asString(r, "Basis"), sev: asString(r, "Severity"), desc: asString(r, "Description")}
		_ = json.Unmarshal(r["KnownExploit"], &x.kev)
		if in.MinSeverity != "" && fwd.SeverityRank(x.sev) < fwd.SeverityRank(in.MinSeverity) {
			continue
		}
		if in.KnownExploitedOnly && !x.kev {
			continue
		}
		rows = append(rows, x)
	}
	if det.Truncated {
		limits = append(limits, fmt.Sprintf("%d findings; the first %d were read", det.Total, len(det.Items)))
	}
	if len(rows) == 0 {
		return result.NewUnknown(vulnerabilitiesName, fmt.Sprintf("%s has %d vulnerable CVE(s), none matching the filters", in.Device, vulnerable), cx,
			append(limits, "the filters excluded every vulnerable CVE; the device is not clean"), result.Options{Evidence: []result.Evidence{result.NewEvidence(result.EvNQE, "runNqeQuery", sid, head, "")}})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if a, b := fwd.SeverityRank(rows[i].sev), fwd.SeverityRank(rows[j].sev); a != b {
			return a > b
		}
		return rows[i].kev && !rows[j].kev
	})
	shown := rows
	if len(shown) > in.Limit {
		shown = shown[:in.Limit]
		limits = append(limits, fmt.Sprintf("%d vulnerable CVEs match; the %d worst are shown", len(rows), in.Limit))
	}
	out := make([]map[string]any, 0, len(shown))
	for _, r := range shown {
		d := r.desc
		if len(d) > 200 {
			d = d[:200] + "..."
		}
		out = append(out, map[string]any{"cve": r.cve, "basis": r.basis, "severity": r.sev, "known_exploit": r.kev, "description": strings.TrimSpace(d)})
	}
	head["cves"] = out
	names := make([]string, 0, 3)
	for _, r := range rows {
		if len(names) < 3 {
			names = append(names, fmt.Sprintf("%s (%s)", r.cve, r.sev))
		}
	}
	return result.Build(vulnerabilitiesName, result.Failed,
		fmt.Sprintf("%s is vulnerable to %d CVE(s); worst: %s", in.Device, len(rows), strings.Join(names, ", ")),
		result.Deterministic, cx, result.Options{Limits: limits, NextActions: []string{"verify-change"},
			Evidence: []result.Evidence{result.NewEvidence(result.EvNQE, "runNqeQuery", sid, head, fmt.Sprintf("%d vulnerable CVE(s)", len(rows)))}})
}

// Forward's own reasons internet exposure can be unavailable (InternetExposureError in its source).
var exposureCodes = []string{"PENDING_ADVANCED_REACHABILITY", "INTERNET_NODE_NOT_DEFINED", "REACHABILITY_COMPUTATION_DISABLED"}

// exposureUnavailable recognises Forward's refusal to filter by internetAddressable (a 400 that names the reason) and returns the reason code.
func exposureUnavailable(err error) (code string, ok bool) {
	if err == nil || !strings.Contains(err.Error(), "Internet exposure analysis is unavailable") {
		return "", false
	}
	for _, c := range exposureCodes {
		if strings.Contains(err.Error(), c) {
			return c, true
		}
	}
	return "", true
}

// exposureWhy says why internet_addressable has no value for a snapshot, from Forward's reason code when it gave one and the snapshot's advanced reachability state otherwise.
func exposureWhy(code, adv string) string { return exposureWhyFlow(code, adv, flowFacts{}) }

// exposureWhyFlow is exposureWhy with what the organization properties say about flow computation. The reasons are distinct: no internet node; flow computation disabled
// (and what is and is not known about the cause); advanced reachability blocked by disabled flow computation; never asked for (on demand); should have started itself (async) and
// did not; still computing; ended in a final state. UNKNOWN is said where the API cannot tell.
func exposureWhyFlow(code, adv string, flow flowFacts) string {
	switch code {
	case "INTERNET_NODE_NOT_DEFINED":
		return "the snapshot has no internet node, so exposure cannot be computed"
	case "REACHABILITY_COMPUTATION_DISABLED":
		return "flow computation is disabled for this network, so exposure cannot be computed: " + flow.disabledCause()
	}
	switch adv {
	case "UNPROCESSED":
		return flow.unprocessedWhy()
	case "PROCESSING":
		return "advanced reachability is still computing for this snapshot (state PROCESSING); exposure follows once it is PROCESSED"
	case "FAILED", "CANCELED", "TIMED_OUT":
		return "advanced reachability ended " + adv + " for this snapshot, so exposure was not computed"
	case "PROCESSED":
		return "advanced reachability is PROCESSED, so Forward's own reason (an internet node must exist and flow computation must be enabled) applies; read inspect-topology kind external and inspect-environment"
	}
	if code == "PENDING_ADVANCED_REACHABILITY" {
		return "Forward reports advanced reachability as pending (the DAG merge stage has not finished), and the snapshot carries no state to say why; " + flow.unprocessedWhy()
	}
	return "Forward did not say why internet exposure is unavailable"
}

// exposureUnknown is the answer when the internet_addressable view cannot be had: unknown with the reason and the next step, never an error and never zero. The unfiltered view works.
func exposureUnknown(code string, in vulnInput, cx result.Context, limits []string) result.Result {
	why := exposureWhyFlow(code, in.adv, in.flow)
	var next []string
	switch {
	case code == "INTERNET_NODE_NOT_DEFINED":
		next = []string{"inspect-topology", "plan-synthetic-device"}
	case code == "REACHABILITY_COMPUTATION_DISABLED":
		next = []string{"inspect-environment"}
	case in.adv == "UNPROCESSED" && in.flow.Read && in.flow.Disabled:
		next = []string{"inspect-environment"} // blocked: starting it would not help
	case in.adv == "UNPROCESSED" && in.flow.Read && in.flow.Mode == "ASYNC":
		next = []string{"inspect-environment", "edit-advanced-reachability", "inspect-snapshots"}
	case in.adv == "UNPROCESSED":
		next = []string{"edit-advanced-reachability", "inspect-snapshots"}
	case in.adv == "FAILED" || in.adv == "CANCELED" || in.adv == "TIMED_OUT":
		next = []string{"edit-snapshot", "edit-advanced-reachability"}
	default:
		next = []string{"inspect-snapshots"}
	}
	limits = append(limits, "Forward refused the internet_addressable view: "+why,
		"the unfiltered view still works: run again without internet_addressable. The order is: internet node modelled, advanced reachability computed (snapshot state PROCESSED), then internet_addressable is available per device (Forward computes exposure itself once advanced reachability finishes); an exclusion on the internet node matters only after that. Advanced reachability is on demand by default (organization property ADVANCED_REACHABILITY_ANALYSIS), asynchronous and compute-heavy, and a reprocess clears it",
		"exposure is per device and per snapshot; no internet_addressable value is not the same as 'not exposed'",
		"Forward reports PENDING_ADVANCED_REACHABILITY whenever the DAG merge stage has not finished, and marks the stages after reachability as not run when flow computation is disabled, so a pending or UNPROCESSED reading alone does not rule disabled flow computation out; a network-level override of DISABLE_FLOW_COMPUTATION and the license tier are returned by no API (inspect-environment shows the organization properties)")
	r, err := result.NewUnknown(vulnerabilitiesName, "Internet exposure is unavailable: "+why, cx, limits, result.Options{NextActions: next})
	if err != nil {
		return result.NewError(vulnerabilitiesName, err.Error(), cx)
	}
	return r
}
