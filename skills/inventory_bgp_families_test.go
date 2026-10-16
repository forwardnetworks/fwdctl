package skills_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

// kind bgp_families reads the BGP address families a network runs from the device CONFIG (bgpRib lists every family on every device, so it cannot say which are run). It counts devices by family and OS, and with name = a family
// lists the devices running it. Family strings are normalised: IOS-XE puts the VRF in the family ("ipv4 vrf X").
func bgpFamilyAnswers() map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		"router bgp": rowsOf(5,
			map[string]any{"device": "eos1", "os": "EOS", "model": "7050", "family": "ipv4"},
			map[string]any{"device": "eos1", "os": "EOS", "model": "7050", "family": "evpn"},
			map[string]any{"device": "eos2", "os": "EOS", "model": "7050", "family": "evpn"},
			map[string]any{"device": "xe1", "os": "IOS_XE", "model": "C9300", "family": "ipv4 vrf RED"},
			map[string]any{"device": "xe1", "os": "IOS_XE", "model": "C9300", "family": "l2vpn evpn"}),
		"group d as g by d.platform.os": rowsOf(3,
			map[string]any{"os": "EOS", "devices": 2},
			map[string]any{"os": "IOS_XE", "devices": 1},
			map[string]any{"os": "JUNOS", "devices": 4}),
	}
}

func TestBGPFamiliesCountsDevicesByFamilyAndOSFromConfig(t *testing.T) {
	r, srv := inv(t, bgpFamilyAnswers(), nil, `{"network_id":"n1","kind":"bgp_families"}`)
	b := jsonOf(r)
	if writes(srv) != 0 || r.Status != result.OK {
		t.Fatalf("%s %s", r.Status, b)
	}
	var d map[string]any
	for _, e := range r.Evidence {
		if v, ok := e.Detail["bgp_families"].(map[string]any); ok {
			d = v
		}
	}
	if d == nil {
		t.Fatalf("no detail: %s", b)
	}
	rows := d["by_family_and_os"].([]map[string]any)
	got := map[string]string{}
	for _, x := range rows {
		got[fmt.Sprint(x["family"])+"|"+fmt.Sprint(x["os"])] = fmt.Sprint(x["devices"])
	}
	if got["l2vpn evpn|EOS"] != "2" || got["l2vpn evpn|IOS_XE"] != "1" || got["ipv4 unicast|IOS_XE"] != "1" || got["ipv4 unicast|EOS"] != "1" {
		t.Errorf("counts: %v", got)
	}
	if d["evpn_devices"] != 3 {
		t.Errorf("EVPN is evpn and l2vpn evpn together: %v", d["evpn_devices"])
	}
	cov := d["os_coverage"].([]map[string]any)
	var junos map[string]any
	for _, c := range cov {
		if c["os"] == "JUNOS" {
			junos = c
		}
	}
	if junos == nil || fmt.Sprint(junos["devices_with_family"]) != "0" || junos["read"] != false {
		t.Errorf("JUNOS is not read by these patterns: %v", cov)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "config") || !strings.Contains(strings.Join(r.Limits, " "), "bgpRib") {
		t.Errorf("the limits say it is config-derived and why not bgpRib: %v", r.Limits)
	}
	r, _ = inv(t, bgpFamilyAnswers(), nil, `{"network_id":"n1","kind":"bgp_families","name":"evpn"}`)
	b = jsonOf(r)
	if !strings.Contains(b, "eos1") || !strings.Contains(b, "eos2") || !strings.Contains(b, "xe1") || strings.Contains(b, "ipv4 vrf RED") {
		t.Errorf("name = a family lists the devices running it (evpn and l2vpn evpn): %s", b)
	}
}
