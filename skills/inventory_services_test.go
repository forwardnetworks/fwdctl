package skills_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/nqelint"
	"github.com/forwardnetworks/fwdctl/nqeschema"
	"github.com/forwardnetworks/fwdctl/result"
	"github.com/forwardnetworks/fwdctl/skills"
)

func srv(host, vrf string) string { return host + "@" + vrf }

func svcRow(dev, typ string, dns, log, tacacs, ntp []any) map[string]any {
	n := func(v []any) []any {
		if v == nil {
			return []any{}
		}
		return v
	}
	return map[string]any{"Device": dev, "Physical": dev, "Vendor": "CISCO", "OS": "IOS", "Type": typ, "Dns": n(dns), "Log": n(log), "Tacacs": n(tacacs), "Ntp": n(ntp)}
}

func svcDetail(t *testing.T, r result.Result) map[string]any {
	t.Helper()
	return r.Evidence[0].Detail["services"].(map[string]any)
}

func TestInventoryServicesCountsDevicesWithAndWithoutEachServiceAndTheServerSetsInUse(t *testing.T) {
	rows := []any{
		svcRow("r1", "ROUTER", []any{srv("10.0.0.53", "default")}, []any{srv("10.0.0.9", "default")}, []any{srv("10.0.0.7", "default")}, []any{srv("10.0.0.1", "default"), srv("10.0.0.2", "default")}),
		svcRow("r2", "ROUTER", []any{srv("10.0.0.53", "default")}, nil, []any{srv("10.0.0.7", "default")}, []any{srv("10.0.0.2", "default"), srv("10.0.0.1", "default")}), // the same NTP set in another order
		svcRow("sw1", "SWITCH", nil, nil, nil, []any{srv("pool.example", "mgmt")}),
		svcRow("sw2", "SWITCH", nil, nil, nil, nil),
	}
	r, _ := mustRun(t, "inspect-inventory", coverageRoutes(rows, []any{}), `{"network_id":"n1","kind":"services"}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "Of 4 devices, the number with at least one server: DNS 2, syslog 1, TACACS+ 2, NTP 3") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	sv := svcDetail(t, r)
	ntp := sv["ntp"].(map[string]any)
	if ntp["configured"] != 3 || ntp["none_configured"] != 1 || ntp["distinct_server_sets"] != 2 {
		t.Errorf("NTP: two devices list the same servers in another order, so there are two distinct sets: %v", ntp)
	}
	sets := ntp["most_common_server_sets"].([]map[string]any)
	if sets[0]["devices"] != 2 || sets[0]["servers"] != "10.0.0.1,10.0.0.2" || !strings.Contains(sets[1]["servers"].(string), "pool.example@mgmt") {
		t.Errorf("most common first, a non-default VRF named: %v", sets)
	}
	logS := sv["syslog"].(map[string]any)
	if logS["none_configured"] != 3 || logS["none_by_device_type"].(map[string]int)["SWITCH"] != 2 {
		t.Errorf("syslog gaps by device type: %v", logS)
	}
	lacking := sv["tacacs"].(map[string]any)["devices_with_none_shown"].([]map[string]any)
	if len(lacking) != 2 || lacking[0]["device"] != "sw1" || lacking[1]["device"] != "sw2" {
		t.Errorf("the devices without TACACS+ are listed in name order: %v", lacking)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "not by itself a defect") {
		t.Errorf("limits: %v", r.Limits)
	}
}

func TestInventoryServicesIsUnknownWhenNoDeviceHasAnyServerAnywhere(t *testing.T) {
	r, _ := mustRun(t, "inspect-inventory", coverageRoutes([]any{svcRow("a", "ROUTER", nil, nil, nil, nil), svcRow("b", "SWITCH", nil, nil, nil, nil)}, []any{}), `{"network_id":"n1","kind":"services"}`)
	if r.Status != result.Unknown || !strings.Contains(strings.Join(r.Limits, " "), "may not read them for these platforms") || r.Evidence[0].Detail["services"] == nil {
		t.Errorf("all zeros prove nothing, and the detail is kept: %s %v", r.Status, r.Limits)
	}
}

func TestInventoryServicesIsUnknownWhenTheFieldOrTheDevicesAreMissing(t *testing.T) {
	routes := coverageRoutes([]any{}, []any{})
	r, _ := mustRun(t, "inspect-inventory", routes, `{"network_id":"n1","kind":"services"}`)
	if r.Status != result.Unknown || !strings.Contains(r.Finding, "No physical device completed collection") {
		t.Errorf("no devices: %s %s", r.Status, r.Finding)
	}
	routes[nqePath] = fwdtest.Const(400, map[string]any{"message": "Error encountered while executing the NQE query", "errors": []any{map[string]any{"message": "Record does not have field: services"}}})
	r, _ = mustRun(t, "inspect-inventory", routes, `{"network_id":"n1","kind":"services"}`)
	if r.Status != result.Unknown || !strings.Contains(r.Finding, "does not offer device services in NQE") || !strings.Contains(strings.Join(r.NextActions, " "), "inspect-environment") {
		t.Errorf("a Forward without device.services: %s %s %v", r.Status, r.Finding, r.NextActions)
	}
}

func TestInventoryServicesTakesNoFilters(t *testing.T) {
	if _, _, err := runSkill(t, "inspect-inventory", coverageRoutes([]any{}, []any{}), `{"network_id":"n1","kind":"services","device":"r1"}`); err == nil {
		t.Errorf("a filter the kind does not take is refused")
	}
}

func TestInventoryServicesQueryLintsClean(t *testing.T) {
	if !nqeschema.Available() {
		t.Skip("this build carries no schema")
	}
	if ds := nqelint.LintWith(skills.InventoryServicesQuery(), nqelint.Modules); len(ds) != 0 {
		t.Errorf("services query: %v", ds)
	}
}

func TestInventoryServicesComparesServersIgnoringCaseAndCountsABlankEntryAsNone(t *testing.T) {
	rows := []any{
		svcRow("a", "ROUTER", nil, nil, nil, []any{"NTP.Example@default"}),
		svcRow("b", "ROUTER", nil, nil, nil, []any{"ntp.example@default"}),
		svcRow("c", "ROUTER", nil, nil, nil, []any{"@default", ""}),
	}
	r, _ := mustRun(t, "inspect-inventory", coverageRoutes(rows, []any{}), `{"network_id":"n1","kind":"services"}`)
	ntp := svcDetail(t, r)["ntp"].(map[string]any)
	if ntp["distinct_server_sets"] != 1 || ntp["configured"] != 2 || ntp["none_configured"] != 1 {
		t.Errorf("one set, whatever the case; an entry with no host is not a server: %v", ntp)
	}
}

// A failure that is not the missing field comes back as an error: it is not reported as "this Forward lacks device services".
func TestInventoryServicesPassesOtherFailuresThrough(t *testing.T) {
	for name, h := range map[string]fwdtest.Handler{
		"server error":    fwdtest.Const(500, map[string]any{"message": "boom: it-services backend failed"}),
		"other query err": fwdtest.Const(400, map[string]any{"message": "Error encountered while executing the NQE query", "errors": []any{map[string]any{"message": "Record does not have field: somethingElse"}}}),
		"services text":   fwdtest.Const(400, map[string]any{"message": "Error encountered while executing the NQE query", "errors": []any{map[string]any{"message": "timed out calling the services endpoint"}}}),
	} {
		routes := coverageRoutes([]any{}, []any{})
		routes[nqePath] = h
		if _, _, err := runSkill(t, "inspect-inventory", routes, `{"network_id":"n1","kind":"services"}`); err == nil {
			t.Errorf("%s: must not be reported as a missing field", name)
		}
	}
}

func TestInventoryServicesCountsVirtualContextsOnceAndSaysSo(t *testing.T) {
	chassis := svcRow("fw", "FIREWALL", nil, []any{srv("10.0.0.9", "default")}, nil, nil)
	vs1 := svcRow("fw_vsys1", "FIREWALL", nil, nil, nil, nil)
	vs1["Physical"] = "FW" // the chassis, in another case
	orphan := svcRow("lone_vsys", "FIREWALL", nil, []any{srv("10.0.0.9", "default")}, nil, nil)
	orphan["Physical"] = "gone-chassis" // its chassis is not a row: it is counted
	r, _ := mustRun(t, "inspect-inventory", coverageRoutes([]any{chassis, vs1, orphan, svcRow("sw", "SWITCH", nil, nil, nil, nil)}, []any{}), `{"network_id":"n1","kind":"services"}`)
	d := r.Evidence[0].Detail
	if d["devices"] != 3 || d["virtual_contexts_not_counted"] != 1 || d["virtual_contexts_whose_chassis_is_not_a_row_counted"] != 1 {
		t.Errorf("the context whose chassis is a row is not counted twice, an orphan is counted: %v", d)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "1 virtual context(s) whose chassis is also a row are not counted separately") {
		t.Errorf("limits: %v", r.Limits)
	}
}

func TestInventoryServicesFlagsAPlatformWithNoServerAnywhereWhileOthersHaveSome(t *testing.T) {
	good := svcRow("r1", "ROUTER", nil, nil, nil, []any{srv("10.0.0.1", "default")})
	arista := svcRow("a1", "SWITCH", nil, nil, nil, nil)
	arista["Vendor"], arista["OS"] = "ARISTA", "ARISTA_EOS"
	r, _ := mustRun(t, "inspect-inventory", coverageRoutes([]any{good, arista}, []any{}), `{"network_id":"n1","kind":"services"}`)
	if r.Status != result.OK || !strings.Contains(strings.Join(r.Limits, " "), "ARISTA ARISTA_EOS (1)") {
		t.Errorf("a platform that shows nothing at all is flagged as possibly unparsed: %s %v", r.Status, r.Limits)
	}
}

func TestInventoryServicesCountsADuplicatedServerOnceAndDeclaresCutLists(t *testing.T) {
	rows := []any{svcRow("d1", "ROUTER", nil, nil, nil, []any{srv("a", "default"), srv("a", "default")}), svcRow("d2", "ROUTER", nil, nil, nil, []any{srv("a", "default")})}
	r, _ := mustRun(t, "inspect-inventory", coverageRoutes(rows, []any{}), `{"network_id":"n1","kind":"services"}`)
	if ntp := svcDetail(t, r)["ntp"].(map[string]any); ntp["distinct_server_sets"] != 1 {
		t.Errorf("[a,a] and [a] are one set: %v", ntp)
	}
	var many []any
	for i := 0; i < 30; i++ {
		many = append(many, svcRow(fmt.Sprintf("d%02d", i), "ROUTER", nil, nil, nil, []any{srv(fmt.Sprintf("ntp%02d", i), "default")}))
	}
	r, _ = mustRun(t, "inspect-inventory", coverageRoutes(many, []any{}), `{"network_id":"n1","kind":"services"}`)
	ntp := svcDetail(t, r)["ntp"].(map[string]any)
	if ntp["distinct_server_sets"] != 30 || len(ntp["most_common_server_sets"].([]map[string]any)) != 5 {
		t.Errorf("five most common sets of thirty: %v", ntp["distinct_server_sets"])
	}
	dns := svcDetail(t, r)["dns"].(map[string]any)
	if len(dns["devices_with_none_shown"].([]map[string]any)) != 25 || dns["none_configured"] != 30 {
		t.Errorf("25 shown of 30: %v", dns["none_configured"])
	}
	var what []string
	for _, o := range r.Omitted {
		what = append(what, o.What)
	}
	if j := strings.Join(what, "|"); !strings.Contains(j, "devices with no DNS server") || !strings.Contains(j, "server sets for NTP") {
		t.Errorf("both cut lists are declared as omissions: %v", what)
	}
}
