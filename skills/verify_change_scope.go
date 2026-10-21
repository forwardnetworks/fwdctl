package skills

import (
	"context"
	"fmt"
	"sort"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/knowledge"
	"github.com/forwardnetworks/fwdctl/result"
)

// predictFullPlatforms are Forward's OS names for the platforms on which Predict does full routing re-synthesis (prediction-scope.md, "Supported Platforms", 26.9 and 26.10: Cisco IOS, IOS-XE, IOS-XR, NX-OS and ACI, ASA, Juniper
// Junos and SRX, Arista EOS, Fortinet, Palo Alto PAN-OS). An exact match on the uppercased OS name: any other platform is in the model, but its routing table stays as collected and routes do not propagate through it.
var predictFullPlatforms = map[string]bool{
	"IOS": true, "IOS_XE": true, "IOS_XR": true, "NXOS": true, "ACI": true, "ASA": true, "JUNOS": true, "SRX": true, "ARISTA_EOS": true, "EOS": true, "FORTINET": true, "PAN_OS": true,
}

// predictOutOfScopeFamilies are the BGP families the 26.9 table says Predict does not model: a normalized config family containing the marker belongs to the class.
var predictOutOfScopeFamilies = []struct{ class, marker string }{
	{"evpn", "evpn"}, {"vpls", "vpls"}, {"multicast", "multicast"}, {"bgp-ls", "link-state"}, {"sr policy", "sr-policy"}, {"sr policy", "srte"}, {"flowspec", "flowspec"},
}

// predictScope is verify-change view scope: which of a network Predict can model on this release. It joins the license facet, the BGP families in config that are out of scope, the platforms that stop propagation, and the
// OSPF and static re-propagation facts. It judges no change.
func predictScope(ctx context.Context, s *fwd.Session, networkID, snapshotID string) (result.Result, error) {
	sc, err := buildScope(ctx, s, networkID, snapshotID)
	if err != nil {
		return result.Result{}, err
	}
	if sc.notReady != nil {
		return *sc.notReady, nil
	}
	return result.Build(verifyChangeName, sc.status, sc.finding, result.Deterministic, sc.cx, result.Options{Limits: sc.limits, NextActions: []string{"inspect-inventory", "edit-change-set"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvPredict, "predictScope", sc.cx.SnapshotID, map[string]any{"view": "scope", "scope": sc.detail}, sc.finding)}})
}

// scopeResult is what the scope read produced, for the scope view and the preflight that builds on it.
type scopeResult struct {
	detail   map[string]any
	limits   []string
	status   result.Status
	finding  string
	cx       result.Context
	snap     *forward.Snapshot
	notReady *result.Result
	bgpDevs  map[string]bool // the devices that run BGP in config (any family found)
}

// predictFlagOrder is the order the flags are reported in.
var predictFlagOrder = []string{"predict_modeling", "predict_model_bgp", "predict_model_ospf", "predict_model_config", "predict_model_cross_protocol", "predict_model_isis", "predict_model_omp", "predict_model_ext_adv", "firewall_predict"}

// releaseOf takes "26.9" from a Forward version or release string such as "26.9.4-02".
func releaseOf(v string) string {
	parts := strings.SplitN(strings.TrimSpace(v), ".", 3)
	if len(parts) < 2 {
		return ""
	}
	for _, p := range []string{parts[0], parts[1]} {
		if p == "" || strings.Trim(p, "0123456789") != "" {
			return ""
		}
	}
	return parts[0] + "." + parts[1]
}

func buildScope(ctx context.Context, s *fwd.Session, networkID, snapshotID string) (scopeResult, error) {
	snap, err := resolveSnapshot(ctx, s, networkID, snapshotID)
	if err != nil {
		return scopeResult{}, err
	}
	cx := fwd.Context(networkID, snap)
	if !fwd.IsReady(snap) {
		r, _ := result.NewUnknown(verifyChangeName, "No processed snapshot is available to read the network from", cx, []string{"no processed snapshot; nothing was measured"}, result.Options{NextActions: []string{"inspect-snapshots"}})
		return scopeResult{notReady: &r, cx: cx}, nil
	}
	sid := fwd.SnapshotID(cx)
	var limits []string
	d := map[string]any{}

	lic := map[string]any{}
	if ts, _, lerr := s.Client.Licensing.TierAndStatus(ctx); lerr == nil && ts != nil {
		lic["tier"], lic["status"] = ts.LicenseTier, ts.LicenseStatus
		if f, known := licenseFacets(ts.LicenseTier); known {
			lic["predict"] = f["predict"]
		} else {
			lic["predict"] = nil
			limits = append(limits, "the license tier "+ts.LicenseTier+" is not one whose Predict facet is known here")
		}
	} else {
		limits = append(limits, "the license could not be read, so whether Predict is licensed is not known")
	}
	d["license"] = lic

	cfg := s.OrgConfig(ctx)
	release := ""
	if v, verr := s.Version(ctx); verr == nil && v != nil {
		release = firstNonEmpty(releaseOf(v.Release), releaseOf(v.Version))
		d["version"] = firstNonEmpty(v.Release, v.Version)
		d["build"] = v.Build
	}
	d["release"] = release
	flagRows := []map[string]any{}
	flagVal := map[string]any{}
	for _, name := range predictFlagOrder {
		row := map[string]any{"property": name}
		if v, ok := cfg.Effective[name]; ok {
			row["value"], row["source"] = v == "true", "read from the tenant"
		} else if dv, defined, ok := knowledge.OrgPropertyDefault(name, release); ok && !defined {
			row["value"], row["source"] = nil, "this property does not exist in release "+release+" (so it is neither on nor settable)"
		} else if ok {
			row["value"], row["source"] = dv == "true", "default for release "+release+", not read from the tenant"
		} else {
			row["value"], row["source"] = nil, "not readable here, and no default is held for this release"
		}
		flagVal[name] = row["value"]
		flagRows = append(flagRows, row)
	}
	d["flags"] = flagRows
	d["predict_modeling"], d["predict_model_ospf"] = flagVal["predict_modeling"], flagVal["predict_model_ospf"]
	d["ospf_repropagated"] = flagVal["predict_model_ospf"]
	d["static_repropagated"] = flagVal["predict_model_config"]
	if release == "" {
		limits = append(limits, "the Forward release could not be read, so a flag that cannot be read from the tenant has no default here")
	}

	rows, _, _, ferr := s.RunNQEAll(ctx, networkID, sid, strings.ReplaceAll(bgpFamiliesQuery, "´´´", "```"), maxModelRows)
	classDevs := map[string]map[string]bool{}
	bgpDevs := map[string]bool{}
	if ferr != nil {
		limits = append(limits, "the BGP families could not be read ("+ferr.Error()+"), so out-of-scope families are not shown")
	}
	for _, r := range rows {
		bgpDevs[str(r["device"])] = true
		fam, _ := normalizeBGPFamily(str(r["family"]))
		for _, oos := range predictOutOfScopeFamilies {
			if strings.Contains(fam, oos.marker) {
				if classDevs[oos.class] == nil {
					classDevs[oos.class] = map[string]bool{}
				}
				classDevs[oos.class][str(r["device"])] = true
			}
		}
	}
	oos := []map[string]any{}
	for class, set := range classDevs {
		oos = append(oos, map[string]any{"family": class, "devices": len(set)})
	}
	sort.Slice(oos, func(i, j int) bool { return fmt.Sprint(oos[i]["family"]) < fmt.Sprint(oos[j]["family"]) })
	d["out_of_scope_families"] = oos

	stops := []map[string]any{}
	if osRows, _, _, oerr := s.RunNQEAll(ctx, networkID, sid, bgpOSCountQuery, 1000); oerr != nil {
		limits = append(limits, "the device count by OS could not be read ("+oerr.Error()+"), so the platforms that stop propagation are not shown")
	} else {
		for _, r := range osRows {
			if !predictFullPlatforms[strings.ToUpper(strings.TrimSpace(str(r["os"])))] {
				n, _ := num(r["devices"])
				stops = append(stops, map[string]any{"os": str(r["os"]), "devices": n})
			}
		}
		sort.Slice(stops, func(i, j int) bool { return fmt.Sprint(stops[i]["os"]) < fmt.Sprint(stops[j]["os"]) })
	}
	d["propagation_stops"] = stops

	var notes []string
	if lic["predict"] == false {
		notes = append(notes, "the license (tier "+fmt.Sprint(lic["tier"])+") has no Predict facet, so change sets cannot be used")
	}
	for _, o := range oos {
		notes = append(notes, fmt.Sprintf("%v on %v device(s) is not modelled", o["family"], o["devices"]))
	}
	if len(stops) > 0 {
		n := int64(0)
		for _, x := range stops {
			if v, ok := x["devices"].(int64); ok {
				n += v
			}
		}
		notes = append(notes, fmt.Sprintf("%d device(s) on platforms where propagation stops", n))
	}
	if d["ospf_repropagated"] == false {
		notes = append(notes, "OSPF changes are not re-propagated")
	}
	if d["static_repropagated"] == false {
		notes = append(notes, "static and connected changes are not re-propagated")
	}
	if flagVal["predict_model_cross_protocol"] == false {
		notes = append(notes, "redistribution into BGP is not re-propagated")
	}
	finding := "Predict fit: " + strings.Join(notes, "; ")
	status := result.OK
	if lic["predict"] == false {
		status = result.Failed // Predict cannot be used on this license whatever the network looks like
	}
	limits = append(limits,
		"the scope is prediction-scope.md of the 26.9 and 26.10 documentation (read from the documentation by a peer session, not verified on a live Forward): BGP unicast IPv4/IPv6, VPNv4/VPNv6 and BGP-LU are re-propagated; EVPN, VPLS, multicast/MVPN, BGP-LS, SR policy and flowspec are not; OSPF, static, SD-WAN, IS-IS, EIGRP and RIP are not re-propagated on 26.9. Master's platform-support page adds BGP rows for Meraki MX, AWS Transit Gateway and VeloCloud Edge and turns predict_model_ospf on by default",
		"full routing re-synthesis is documented for Cisco IOS, IOS-XE, IOS-XR, NX-OS, ASA, Juniper Junos and SRX, Arista EOS, Fortinet and Palo Alto PAN-OS; any other platform stays as collected and propagation stops at it. The platform match is an exact match on Forward's OS name (IOS, IOS_XE, IOS_XR, NXOS, ACI, ASA, JUNOS, SRX, ARISTA_EOS, FORTINET, PAN_OS); an OS name not in that list is reported as a propagation stop, including OTHER and UNKNOWN",
		"BGP families are read from device config (see inspect-inventory kind bgp_families): EOS, IOS, IOS-XE and NX-OS shapes only; Junos, IOS-XR and FortiOS are not read, so their out-of-scope families, if any, are missing here")
	limits = append(limits, "the flag defaults by release are Forward's OrgProperty.java on release/26.9.0-18, release/26.10.0-02 and master (read from source, not run on a live Forward; sealed in the release binary); a value read from the tenant is the effective organization property. These flags are on-premises-settable only: on SaaS Forward staff set them")
	return scopeResult{detail: d, limits: limits, status: status, finding: finding, cx: cx, snap: snap, bgpDevs: bgpDevs}, nil
}
