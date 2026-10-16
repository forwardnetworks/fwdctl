package skills

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// bgpFamiliesQuery lists, per device, the BGP address families named in its config: the patterns of a live-checked query for EOS (address-family evpn / ipv4 / rt-membership), IOS, IOS-XE and NX-OS
// (address-family ipv4 unicast / l2vpn evpn, at the BGP level, under a neighbor, or under a vrf). network.devices.bgpRib.afiSafis is not used: it lists every family on every device whether or not it is run.
// Junos (protocols bgp), IOS-XR and FortiOS have other shapes and are not read.
const bgpFamiliesQuery = `p1 = ´´´
router bgp {as:string}
  address-family {fam:(string*)}
´´´;
p2 = ´´´
router bgp {as:string}
  neighbor {nbr:string}
    address-family {fam:(string*)}
´´´;
p3 = ´´´
router bgp {as:string}
  vrf {v:string}
    address-family {fam:(string*)}
´´´;
foreach d in network.devices
let fams = (foreach m in blockMatches(d.files.config, p1) select join(" ", m.data.fam))
         + (foreach m in blockMatches(d.files.config, p2) select join(" ", m.data.fam))
         + (foreach m in blockMatches(d.files.config, p3) select join(" ", m.data.fam))
foreach f in distinct(fams)
select {device: d.name, os: d.platform.os, model: d.platform.model, family: f}`

// bgpOSCountQuery counts the devices of each OS, to say which OSes the family patterns could not read.
const bgpOSCountQuery = `foreach d in network.devices
group d as g by d.platform.os as os
select {os: os, devices: length(g)}`

// normalizeBGPFamily turns a raw config family ("ipv4 vrf RED", "ipv4 unicast", "l2vpn evpn", "evpn") into the family and its VRF: the VRF is split off and the family is the first two words.
func normalizeBGPFamily(raw string) (family, vrf string) {
	var words []string
	f := strings.Fields(strings.ToLower(raw))
	for i := 0; i < len(f); i++ {
		if f[i] == "vrf" {
			if i+1 < len(f) {
				vrf = f[i+1]
			}
			break
		}
		if len(words) < 2 {
			words = append(words, f[i])
		}
	}
	return strings.Join(words, " "), vrf
}

func isEVPNFamily(family string) bool { return strings.Contains(family, "evpn") }

// familyMatches says whether a normalized family is the one asked for: "evpn" matches evpn and l2vpn evpn; any other name matches that family or the same name followed by a sub-family ("ipv4" matches "ipv4 unicast").
func familyMatches(family, want string) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	if want == "evpn" {
		return isEVPNFamily(family)
	}
	return family == want || strings.HasPrefix(family, want+" ")
}

// inventoryBGPFamilies is kind bgp_families: the BGP address families a network runs, read from device config. Without name it counts devices by family and OS; with name (a family) it lists the devices running it.
func inventoryBGPFamilies(ctx context.Context, s *fwd.Session, in inventoryInput, cx result.Context, limits []string) (result.Result, error) {
	sid := fwd.SnapshotID(cx)
	rows, _, trunc, err := s.RunNQEAll(ctx, in.NetworkID, sid, strings.ReplaceAll(bgpFamiliesQuery, "´´´", "```"), maxModelRows)
	if err != nil {
		return result.Result{}, err
	}
	if trunc {
		limits = append(limits, fmt.Sprintf("the family read hit its %d-row bound, so the counts are partial", maxModelRows))
	}
	type key struct{ family, os string }
	devsByKey := map[key]map[string]bool{}
	osWith := map[string]map[string]bool{}
	evpn := map[string]bool{}
	type devRow struct {
		device, os, model string
		families, vrfs    map[string]bool
	}
	devs := map[string]*devRow{}
	for _, r := range rows {
		dev, os := str(r["device"]), str(r["os"])
		fam, vrf := normalizeBGPFamily(str(r["family"]))
		if fam == "" {
			continue
		}
		k := key{fam, os}
		if devsByKey[k] == nil {
			devsByKey[k] = map[string]bool{}
		}
		devsByKey[k][dev] = true
		if osWith[os] == nil {
			osWith[os] = map[string]bool{}
		}
		osWith[os][dev] = true
		if isEVPNFamily(fam) {
			evpn[dev] = true
		}
		d := devs[dev]
		if d == nil {
			d = &devRow{device: dev, os: os, model: str(r["model"]), families: map[string]bool{}, vrfs: map[string]bool{}}
			devs[dev] = d
		}
		d.families[fam] = true
		if vrf != "" {
			d.vrfs[vrf] = true
		}
	}
	counts := make([]map[string]any, 0, len(devsByKey))
	for k, set := range devsByKey {
		counts = append(counts, map[string]any{"family": k.family, "os": k.os, "devices": len(set)})
	}
	sort.Slice(counts, func(i, j int) bool {
		if counts[i]["devices"].(int) != counts[j]["devices"].(int) {
			return counts[i]["devices"].(int) > counts[j]["devices"].(int)
		}
		return fmt.Sprint(counts[i]["family"], counts[i]["os"]) < fmt.Sprint(counts[j]["family"], counts[j]["os"])
	})
	var coverage []map[string]any
	if osRows, _, _, oerr := s.RunNQEAll(ctx, in.NetworkID, sid, bgpOSCountQuery, 1000); oerr != nil {
		limits = append(limits, "the device count by OS could not be read ("+oerr.Error()+"), so which OSes were not read is not shown")
	} else {
		for _, r := range osRows {
			os := str(r["os"])
			total, _ := num(r["devices"])
			with := len(osWith[os])
			coverage = append(coverage, map[string]any{"os": os, "devices": total, "devices_with_family": with, "read": with > 0})
		}
		sort.Slice(coverage, func(i, j int) bool { return fmt.Sprint(coverage[i]["os"]) < fmt.Sprint(coverage[j]["os"]) })
	}
	detail := map[string]any{"by_family_and_os": counts, "devices_with_a_family": len(devs), "evpn_devices": len(evpn), "os_coverage": coverage}
	finding := fmt.Sprintf("%d device(s) run a BGP address family in config (%d with EVPN)", len(devs), len(evpn))
	if in.Name != "" {
		var list []map[string]any
		for _, d := range devs {
			match := false
			for f := range d.families {
				if familyMatches(f, in.Name) {
					match = true
				}
			}
			if !match {
				continue
			}
			fl := make([]string, 0, len(d.families))
			for f := range d.families {
				fl = append(fl, f)
			}
			sort.Strings(fl)
			list = append(list, map[string]any{"device": d.device, "os": d.os, "model": d.model, "families": fl})
		}
		sort.Slice(list, func(i, j int) bool { return fmt.Sprint(list[i]["device"]) < fmt.Sprint(list[j]["device"]) })
		shown, omitted := result.Cap(list, defaultInventoryLimitFor(in.Limit), "devices running "+in.Name, "raise limit or use offset")
		detail["family"] = in.Name
		detail["devices"] = shown
		detail["devices_total"] = len(list)
		finding = fmt.Sprintf("%d device(s) run %s in config", len(list), in.Name)
		return result.Build(inventoryName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: bgpFamilyLimits(limits), Omitted: omitted, NextActions: []string{"inspect-bgp-neighbors"},
			Evidence: []result.Evidence{result.NewEvidence(result.EvState, "runNqeQuery", cx.SnapshotID, map[string]any{"bgp_families": detail}, finding)}})
	}
	return result.Build(inventoryName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: bgpFamilyLimits(limits), NextActions: []string{"inspect-bgp-neighbors"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvState, "runNqeQuery", cx.SnapshotID, map[string]any{"bgp_families": detail}, finding)}})
}

func defaultInventoryLimitFor(l int) int {
	if l <= 0 {
		return 100
	}
	return min(l, 1000)
}

func bgpFamilyLimits(limits []string) []string {
	return append(limits,
		"families are read from device CONFIG (router bgp / address-family, under a neighbor, or under a vrf), not from the routing model: network.devices.bgpRib.afiSafis lists every family on every device whether or not it is run, so it cannot answer which families a network runs",
		"the config patterns match the router bgp form of EOS, IOS, IOS-XE and NX-OS (checked on a live network for EOS, IOS-XE, NX-OS, ASA and IOS); Junos (protocols bgp), IOS-XR and FortiOS use other shapes and are not read: os_coverage shows read false for an OS with no family found, which is not proof it runs none",
		"a family is normalised to its first two words, with a VRF split off (IOS-XE writes ipv4 vrf X); a VXLAN-only signal (nve, Vxlan interfaces) is not used because it is not reliable across platforms")
}
