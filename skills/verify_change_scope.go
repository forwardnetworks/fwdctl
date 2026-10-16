package skills

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// predictFullPlatforms are the platforms on which Predict does full routing re-synthesis (prediction-scope.md, "Supported Platforms", 26.9 and 26.10): matched as substrings of the lowercased OS name. Any other platform is in the model, but its routing
// table stays as collected and routes do not propagate through it.
var predictFullPlatforms = []string{"ios", "nxos", "nx-os", "nx_os", "asa", "junos", "eos", "forti", "pan"}

// predictOutOfScopeFamilies are the BGP families the 26.9 table says Predict does not model: a normalized config family containing the marker belongs to the class.
var predictOutOfScopeFamilies = []struct{ class, marker string }{
	{"evpn", "evpn"}, {"vpls", "vpls"}, {"multicast", "multicast"}, {"bgp-ls", "link-state"}, {"sr policy", "sr-policy"}, {"sr policy", "srte"}, {"flowspec", "flowspec"},
}

// predictScope is verify-change view scope: which of a network Predict can model on this release. It joins the license facet, the BGP families in config that are out of scope, the platforms that stop propagation, and the
// OSPF and static re-propagation facts. It judges no change.
func predictScope(ctx context.Context, s *fwd.Session, networkID, snapshotID string) (result.Result, error) {
	snap, err := resolveSnapshot(ctx, s, networkID, snapshotID)
	if err != nil {
		return result.Result{}, err
	}
	cx := fwd.Context(networkID, snap)
	if !fwd.IsReady(snap) {
		return result.NewUnknown(verifyChangeName, "No processed snapshot is available to read the network from", cx, []string{"no processed snapshot; nothing was measured"}, result.Options{NextActions: []string{"inspect-snapshots"}})
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
	prop := func(name string) any {
		if v, ok := cfg.Effective[name]; ok {
			return v == "true"
		}
		return nil
	}
	d["predict_modeling"], d["predict_model_ospf"] = prop("predict_modeling"), prop("predict_model_ospf")
	d["ospf_repropagated"] = d["predict_model_ospf"] == true
	d["static_repropagated"] = false

	rows, _, _, ferr := s.RunNQEAll(ctx, networkID, sid, strings.ReplaceAll(bgpFamiliesQuery, "´´´", "```"), maxModelRows)
	classDevs := map[string]map[string]bool{}
	if ferr != nil {
		limits = append(limits, "the BGP families could not be read ("+ferr.Error()+"), so out-of-scope families are not shown")
	}
	for _, r := range rows {
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
			os := strings.ToLower(str(r["os"]))
			full := false
			for _, p := range predictFullPlatforms {
				if strings.Contains(os, p) {
					full = true
				}
			}
			if !full {
				n, _ := num(r["devices"])
				stops = append(stops, map[string]any{"os": str(r["os"]), "devices": n})
			}
		}
		sort.Slice(stops, func(i, j int) bool { return fmt.Sprint(stops[i]["os"]) < fmt.Sprint(stops[j]["os"]) })
	}
	d["propagation_stops"] = stops

	var notes []string
	if lic["predict"] == false {
		notes = append(notes, "the license has no Predict facet")
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
	notes = append(notes, "static routes are not re-propagated")
	finding := "Predict fit: " + strings.Join(notes, "; ")
	limits = append(limits,
		"the scope is prediction-scope.md of the 26.9 and 26.10 documentation (read from the documentation by a peer session, not verified on a live Forward): BGP unicast IPv4/IPv6, VPNv4/VPNv6 and BGP-LU are re-propagated; EVPN, VPLS, multicast/MVPN, BGP-LS, SR policy and flowspec are not; OSPF, static, SD-WAN, IS-IS, EIGRP and RIP are not re-propagated on 26.9. Master's platform-support page adds BGP rows for Meraki MX, AWS Transit Gateway and VeloCloud Edge and turns predict_model_ospf on by default",
		"full routing re-synthesis is documented for Cisco IOS, IOS-XE, IOS-XR, NX-OS, ASA, Juniper Junos and SRX, Arista EOS, Fortinet and Palo Alto PAN-OS; any other platform stays as collected and propagation stops at it. The platform match is by OS name and may misplace an unusual OS name",
		"BGP families are read from device config (see inspect-inventory kind bgp_families): EOS, IOS, IOS-XE and NX-OS shapes only; Junos, IOS-XR and FortiOS are not read, so their out-of-scope families, if any, are missing here")
	return result.Build(verifyChangeName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, NextActions: []string{"inspect-inventory", "edit-change-set"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvPredict, "predictScope", cx.SnapshotID, map[string]any{"view": "scope", "scope": d}, finding)}})
}
