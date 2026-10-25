package skills_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

// Synthetic fixture of Forward's "devices need to be migrated" rule: the effective type is the configured type, else the one the last connectivity
// test discovered; a device counts when that type belongs to a controller-managed ACI or SD-WAN type (itself or a related type); a cisco_ios_xe type
// counts only when the test reported a controller (a managed edge), a standalone IOS-XE router is not a candidate.
func migrationRoutes() map[string]fwdtest.Handler {
	dev := func(name, typ, discovered string, controllers ...map[string]any) map[string]any {
		tr := map[string]any{"discoveredType": discovered}
		if len(controllers) > 0 {
			tr["controllerInfos"] = controllers
		}
		d := map[string]any{"name": name, "host": "h", "testResult": tr}
		if typ != "" {
			d["type"] = typ
		}
		return d
	}
	vm := map[string]any{"name": "ctl-a", "ipAddress": "192.0.2.10"}
	return map[string]fwdtest.Handler{
		"GET /api/deviceConnTypes": fwdtest.Const(200, map[string]any{
			"cisco_apic_api":     map[string]any{"vendor": "cisco", "collectionSourceType": "CONTROLLER_MANAGED", "relatedTypes": []string{"cisco_nxos_aci_ssh"}},
			"viptela":            map[string]any{"vendor": "viptela", "collectionSourceType": "CONTROLLER_MANAGED", "relatedTypes": []string{"cisco_sdwan_ssh", "cisco_ios_xe_ssh"}},
			"cisco_nxos_aci_ssh": map[string]any{"vendor": "cisco", "collectionSourceType": "CLASSIC"},
			"cisco_sdwan_ssh":    map[string]any{"vendor": "cisco", "collectionSourceType": "CLASSIC"},
			"cisco_ios_xe_ssh":   map[string]any{"vendor": "cisco_ios_xe", "collectionSourceType": "CLASSIC"},
			"arista_eos_ssh":     map[string]any{"vendor": "arista", "collectionSourceType": "CLASSIC"},
		}),
		"GET /api/networks/n1/classic-devices": fwdtest.Const(200, []any{
			dev("a1", "", "cisco_nxos_aci_ssh"),
			dev("s1", "", "cisco_ios_xe_ssh", vm),
			dev("s2", "cisco_ios_xe_ssh", "cisco_ios_xe_ssh"),
			dev("s3", "cisco_sdwan_ssh", "unknown"),
			dev("s4", "", "cisco_ios_xe_ssh", vm),
			dev("e1", "", "arista_eos_ssh"),
		}),
	}
}

func TestMigrationViewCountsByCategoryUsingTheEffectiveType(t *testing.T) {
	r, _ := mustRun(t, "inspect-collection", migrationRoutes(), `{"network_id":"n1","view":"migration"}`)
	var m map[string]any
	for _, e := range r.Evidence {
		if v, ok := e.Detail["migration"].(map[string]any); ok {
			m = v
		}
	}
	if m == nil {
		t.Fatalf("no migration detail: %+v", r.Evidence)
	}
	cat := m["by_category"].(map[string]int)
	if cat["ACI"] != 1 || cat["SD_WAN"] != 3 {
		t.Errorf("a1 is ACI; s1, s3 (configured type, discovery 'unknown') and s4 are SD-WAN; standalone s2 and e1 are not: %v", cat)
	}
	if m["devices_counted"] != 4 || m["devices_read"] != 6 {
		t.Errorf("counted %v of %v", m["devices_counted"], m["devices_read"])
	}
	ctl := m["controllers"].([]map[string]any)
	if len(ctl) != 1 || ctl[0]["address"] != "192.0.2.10" || ctl[0]["devices"] != 2 {
		t.Errorf("one controller serving two devices: %v", ctl)
	}
	if r.Status != result.OK && r.Status != result.Failed {
		t.Errorf("status %s", r.Status)
	}
	if !strings.Contains(strings.Join(r.Limits, " | "), "controller-managed") {
		t.Errorf("the limit says these devices belong in a controller-managed setup: %v", r.Limits)
	}
}

func TestMigrationViewIsUnknownNotAPassWhenTheTypesCannotBeRead(t *testing.T) {
	routes := migrationRoutes()
	routes["GET /api/deviceConnTypes"] = fwdtest.Const(500, map[string]any{"message": "boom"})
	r, _ := mustRun(t, "inspect-collection", routes, `{"network_id":"n1","view":"migration"}`)
	if r.Status != result.Unknown {
		t.Fatalf("an unreadable type list must be unknown, got %s %s", r.Status, r.Finding)
	}
}

// testResult does not record which login worked, so the view lists the logins that could have been tried (auto-associate CLI credentials) and says the working one cannot be inferred.
func TestMigrationViewListsTheCredentialCandidatesAndSaysTheWorkingLoginCannotBeInferred(t *testing.T) {
	routes := migrationRoutes()
	routes["GET /api/networks/n1/cli-credentials"] = fwdtest.Const(200, []any{
		map[string]any{"id": "L1", "name": "ops", "username": "admin", "password": "SECRET-1", "autoAssociate": true},
		map[string]any{"id": "L2", "name": "lab", "username": "lab", "password": "SECRET-2"},
	})
	r, _ := mustRun(t, "inspect-collection", routes, `{"network_id":"n1","view":"migration"}`)
	b := jsonOf(r)
	if strings.Contains(b, "SECRET") {
		t.Fatalf("a secret leaked: %s", b)
	}
	var m map[string]any
	for _, e := range r.Evidence {
		if v, ok := e.Detail["migration"].(map[string]any); ok {
			m = v
		}
	}
	cands := m["credential_candidates"].([]map[string]any)
	if len(cands) != 1 || cands[0]["id"] != "L1" || cands[0]["username"] != "admin" {
		t.Errorf("only auto-associate logins are candidates: %v", cands)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "cannot be inferred") || !strings.Contains(strings.Join(r.Limits, " "), "connectivity test") {
		t.Errorf("limits: %v", r.Limits)
	}
}

// The migration view names every counted device with the host, login, jump server and collector it uses now, groups them by controller with the controller's kind and the devices that point at it, finds the
// classic record that holds the controller's own login, and returns a ready migrate input for a pilot.
func TestMigrationViewNamesDevicesControllersAndSuggestsAMigrate(t *testing.T) {
	routes := migrationRoutes()
	tr := func(extra map[string]any) map[string]any {
		return extra
	}
	vm := map[string]any{"name": "", "ipAddress": "192.0.2.10"}
	routes["GET /api/networks/n1/classic-devices"] = fwdtest.Const(200, []any{
		tr(map[string]any{"name": "vmanage1", "type": "viptela", "host": "192.0.2.10", "cliCredentialId": "L-ctl", "jumpServerId": "J1", "collectorId": "C7", "testResult": map[string]any{"discoveredType": "viptela"}}),
		tr(map[string]any{"name": "edge1", "host": "198.51.100.1", "cliCredentialId": "L-edge", "jumpServerId": "J2", "collectorId": "C7", "collectBgpAdvertisements": true, "bgpTableType": "ADJ_RIB_IN", "paginationMode": "DISABLE_PAGINATION", "testResult": map[string]any{"discoveredType": "cisco_ios_xe_ssh", "controllerInfos": []any{vm}}}),
		tr(map[string]any{"name": "edge2", "host": "198.51.100.2", "cliCredentialId": "L-edge", "testResult": map[string]any{"discoveredType": "cisco_ios_xe_ssh", "controllerInfos": []any{vm}}}),
		tr(map[string]any{"name": "standalone", "host": "198.51.100.9", "testResult": map[string]any{"discoveredType": "arista_eos_ssh"}}),
	})
	r, _ := mustRun(t, "inspect-collection", routes, `{"network_id":"n1","view":"migration"}`)
	var m map[string]any
	for _, e := range r.Evidence {
		if v, ok := e.Detail["migration"].(map[string]any); ok {
			m = v
		}
	}
	devs := m["devices"].([]map[string]any)
	if len(devs) != 3 || devs[0]["name"] != "edge1" || devs[2]["name"] != "vmanage1" || devs[0]["cli_credential_id"] != "L-edge" || devs[0]["jump_server_id"] != "J2" || devs[0]["collector_id"] != "C7" || devs[0]["host"] != "198.51.100.1" {
		t.Fatalf("devices: %v", devs)
	}
	ctl := m["controllers"].([]map[string]any)
	if len(ctl) != 1 {
		t.Fatalf("controllers: %v", ctl)
	}
	c := ctl[0]
	rec, _ := c["classic_record"].(map[string]any)
	if c["address"] != "192.0.2.10" || c["controller_kind"] != "Cisco SD-WAN (vManage)" || c["address_confirmed_by_a_record"] != true || rec == nil || rec["cli_credential_id"] != "L-ctl" || rec["jump_server_id"] != "J1" {
		t.Errorf("controller: %v", c)
	}
	sm := c["suggested_migrate"].(map[string]any)
	def := sm["definition"].(map[string]any)
	cs := def["controllers"].([]map[string]any)
	md := def["managedDevices"].([]map[string]any)
	if sm["object"] != "controller_setup" || sm["action"] != "migrate" || cs[0]["cliCredentialId"] != "L-ctl" || cs[0]["name"] != "vmanage1" || len(md) != 2 || md[0]["cliCredentialId"] != "L-edge" || md[0]["collectBgpAdvertisements"] != true || md[0]["bgpTableType"] != "ADJ_RIB_IN" || md[0]["paginationMode"] != "DISABLE_PAGINATION" {
		t.Errorf("suggested migrate: %v", sm)
	}
}

// Devices whose test reported no controller (Versa edges) are linked to the network's one controller record of their category; the suggested migrate puts the setup name inside the definition, warns that it
// deletes the classic records, and offers the logins a device's same-subnet siblings use when it has none recorded.
func TestMigrationViewLinksEdgesWithoutAControllerAndHintsLogins(t *testing.T) {
	routes := migrationRoutes()
	routes["GET /api/deviceConnTypes"] = fwdtest.Const(200, map[string]any{
		"versa_director_api": map[string]any{"vendor": "versa_sase", "collectionSourceType": "CONTROLLER_MANAGED", "relatedTypes": []string{"versa_flexvnf_ssh"}},
		"versa_flexvnf_ssh":  map[string]any{"vendor": "versa_sase", "collectionSourceType": "CLASSIC"},
	})
	routes["GET /api/networks/n1/classic-devices"] = fwdtest.Const(200, []any{
		map[string]any{"name": "dir1", "type": "versa_director_api", "host": "10.10.12.6", "httpCredentialId": "H-1", "jumpServerId": "J1", "testResult": map[string]any{"discoveredType": "versa_director_api"}},
		map[string]any{"name": "vedge1", "type": "versa_flexvnf_ssh", "host": "10.20.0.1", "cliCredentialId": "L-9", "testResult": map[string]any{"discoveredType": "versa_flexvnf_ssh"}},
		map[string]any{"name": "vedge2", "type": "versa_flexvnf_ssh", "host": "10.20.0.2", "testResult": map[string]any{"discoveredType": "versa_flexvnf_ssh"}},
	})
	r, _ := mustRun(t, "inspect-collection", routes, `{"network_id":"n1","view":"migration"}`)
	var m map[string]any
	for _, e := range r.Evidence {
		if v, ok := e.Detail["migration"].(map[string]any); ok {
			m = v
		}
	}
	ctl := m["controllers"].([]map[string]any)
	if len(ctl) != 1 || ctl[0]["controller_kind"] != "Versa Director" || !strings.Contains(ctl[0]["found_by"].(string), "only SD_WAN controller record") {
		t.Fatalf("the edges link to the one Director record: %v", ctl)
	}
	rec := ctl[0]["classic_record"].(map[string]any)
	if rec["http_credential_id"] != "H-1" || rec["jump_server_id"] != "J1" {
		t.Errorf("the Director's HTTP login is shown: %v", rec)
	}
	sm := ctl[0]["suggested_migrate"].(map[string]any)
	if _, top := sm["name"]; top {
		t.Errorf("the setup name belongs inside definition")
	}
	cs0 := sm["definition"].(map[string]any)["controllers"].([]map[string]any)[0]
	if cs0["httpCredentialId"] != "H-1" || cs0["jumpServerId"] != "J1" || sm["controller_http_credential_not_carried"] != nil || !strings.Contains(sm["warning"].(string), "restates") {
		t.Errorf("a migrate carries nothing over: the controller entry restates the HTTP login and jump server of the record it deletes: %v", sm)
	}
	def := sm["definition"].(map[string]any)
	if def["name"] == nil || !strings.Contains(sm["warning"].(string), "DELETES") {
		t.Errorf("name in the definition and a delete warning: %v", sm)
	}
	h := sm["credential_hints"].(map[string]any)["devices_without_a_recorded_login"].(map[string]any)
	if sib, _ := h["vedge2"].(map[string]int); sib["L-9"] != 1 {
		t.Errorf("vedge2 has no login recorded; its same-subnet sibling uses L-9: %v", h)
	}
}

// Edge coverage: each IOS XE SD-WAN edge's control connections give the vSmarts it is up to (STATE found after PROXY, so a row without the trailing group id still reads), a greedy minimal cover, and the edges the wizard's
// controllers (the addresses the connectivity tests reported) do not reach.
func TestMigrationViewComputesEdgeCoverageFromControlConnections(t *testing.T) {
	routes := migrationRoutes()
	routes[snapsPath] = ready("s1")
	hdr := "PEER TYPE\nPROT SYSTEM IP\nTYPE\n--------------------------------\n"
	row := func(ip, state string, withGroup bool) string {
		g := " 0"
		if !withGroup {
			g = ""
		}
		return "vsmart  tls  " + ip + "      253        1      198.51.100.23     23556 203.0.113.114    23556 ExampleOrg - 1   biz-internet    No    " + state + "     0:03:09:42" + g + "\n"
	}
	routes["GET /api/networks/n1/devices/s1/files/sdwan_control_connections.txt"] = fwdtest.Const(200, []byte(hdr+row("192.0.2.10", "up", true)+row("192.0.2.11", "up", true)+"vbond  tls  192.0.2.99  1 1 a 1 b 1 Org x No up 0:00:00:01 0\n"))
	routes["GET /api/networks/n1/devices/s4/files/sdwan_control_connections.txt"] = fwdtest.Const(200, []byte(hdr+row("192.0.2.11", "up", false)+row("192.0.2.12", "tear_down", true)))
	r, _ := mustRun(t, "inspect-collection", routes, `{"network_id":"n1","view":"migration"}`)
	var m map[string]any
	for _, e := range r.Evidence {
		if v, ok := e.Detail["migration"].(map[string]any); ok {
			m = v
		}
	}
	cov, _ := m["edge_coverage"].(map[string]any)
	if cov == nil || cov["edges_read"] != 2 || cov["edges_with_a_vsmart_up"] != 2 || cov["distinct_vsmarts_up"] != 2 {
		t.Fatalf("coverage: %v", cov)
	}
	cover := cov["minimal_cover"].([]map[string]any)
	if len(cover) != 1 || cover[0]["vsmart_system_ip"] != "192.0.2.11" || cover[0]["newly_covered_edges"] != 2 {
		t.Errorf("one vSmart (192.0.2.11) covers both edges: %v", cover)
	}
	nr := cov["edges_the_wizard_controllers_do_not_reach"].(map[string]any)
	if nr["count"] != 1 || fmt.Sprint(nr["names"]) != "[s4]" {
		t.Errorf("the wizard's controller 192.0.2.10 reaches s1 but not s4: %v", nr)
	}
}

// device (view config) keeps the named devices and returns each one's full record: login ids by kind (never a value), jump server, collector, note and the collection options read off the record.
func TestCollectionConfigDeviceReturnsTheFullRecordWithLoginIDs(t *testing.T) {
	r, _ := mustRun(t, "inspect-collection", map[string]fwdtest.Handler{
		"GET /api/networks/n1/classic-devices": fwdtest.Const(200, []any{
			map[string]any{"name": "dir1", "type": "versa_director_api", "host": "10.10.12.6", "httpCredentialId": "H-25", "snmpCredentialId": "S-27", "jumpServerId": "J1", "collectorId": "C7", "note": "director", "collectBgpAdvertisements": true, "bgpTableType": "ADJ_RIB_IN"},
			map[string]any{"name": "edge1", "type": "x", "host": "10.20.0.1", "cliCredentialId": "L-9"},
		}),
	}, `{"network_id":"n1","view":"config","device":"dir1"}`)
	b := jsonOf(r)
	for _, want := range []string{`"http_credential_id":"H-25"`, `"snmp_credential_id":"S-27"`, `"jump_server_id":"J1"`, `"note":"director"`, `"collectBgpAdvertisements":true`, `"bgpTableType":"ADJ_RIB_IN"`} {
		if !strings.Contains(b, want) {
			t.Errorf("missing %s: %s", want, b)
		}
	}
	if strings.Contains(b, "edge1") {
		t.Errorf("only the named device: %s", b)
	}
}

// A controller's last saved test carries more than its outcome: the addresses reached, the hostnames the query phase found and which stored logins worked (ids only). The config view shows them.
func TestSetupTestDetailsShowAliasesAndTheWorkingLoginIDs(t *testing.T) {
	r, _ := mustRun(t, "inspect-collection", map[string]fwdtest.Handler{
		"GET /api/networks/n1/classic-devices": fwdtest.Const(200, []any{}),
		"GET /api/networks/n1/controller-managed-setups": fwdtest.Const(200, []any{map[string]any{"name": "s", "controllers": []any{map[string]any{"name": "ctl", "host": "192.0.2.10",
			"testResult": map[string]any{"discoveredType": "cisco_sdwan_vsmart_ssh", "hostIps": []any{"192.0.2.10"}, "nameAliases": []any{"vsmart-west"}, "discoveredCliCredentialId": "L-3", "discoveredHttpCredentialId": "H-2"}}}}}),
	}, `{"network_id":"n1","view":"config"}`)
	b := jsonOf(r)
	for _, want := range []string{`"name_aliases":["vsmart-west"]`, `"discovered_cli_credential":"L-3"`, `"discovered_http_credential":"H-2"`, `"host_ips":["192.0.2.10"]`} {
		if !strings.Contains(b, want) {
			t.Errorf("missing %s: %s", want, b)
		}
	}
}

// device (view config) also names controller entries: a matching controller returns EVERY device its saved test reported managing (name, address), paged by limit and offset, so the controller-to-edge tables can be built from saved data.
func TestConfigDeviceReturnsAControllersFullReportedManagedDevices(t *testing.T) {
	var reported []any
	for i := 0; i < 130; i++ {
		reported = append(reported, map[string]any{"name": fmt.Sprintf("edge%03d", i), "ipAddress": fmt.Sprintf("10.9.0.%d", i)})
	}
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/classic-devices": fwdtest.Const(200, []any{}),
		"GET /api/networks/n1/controller-managed-setups": fwdtest.Const(200, []any{map[string]any{"name": "s", "controllers": []any{map[string]any{"name": "ctl-a", "host": "192.0.2.10",
			"testResult": map[string]any{"discoveredType": "cisco_sdwan_vsmart_ssh", "managedDevices": reported}}, map[string]any{"name": "ctl-b", "host": "192.0.2.11", "testResult": map[string]any{"managedDevices": reported[:3]}}}}}),
	}
	r, _ := mustRun(t, "inspect-collection", routes, `{"network_id":"n1","view":"config","device":"ctl-a","limit":50,"offset":100}`)
	b := jsonOf(r)
	if !strings.Contains(b, `"managed_devices_reported_count":130`) || !strings.Contains(b, "edge100") || !strings.Contains(b, "edge129") || strings.Contains(b, "edge099") {
		t.Errorf("page 100 to 129 of ctl-a's 130 reported devices: %s", b)
	}
	if strings.Contains(strings.Join(r.Limits, "|"), "ctl-b") && strings.Contains(b, `"name":"ctl-b","role":"controller"`) && strings.Contains(b, "managed_devices_reported_page\":[{\"address\":\"10.9.0.0\"") {
		t.Errorf("only the named controller returns the full list")
	}
}

// The migration plan builds the migrate input for ALL counted devices with their logins, jump server and collection options restated, leaves the controllers empty for the person to fill, and lists the vEdges whose collect is not false.
func TestMigrationPlanCoversEveryDeviceAndFlagsVEdgesStillCollecting(t *testing.T) {
	routes := migrationRoutes()
	routes["GET /api/deviceConnTypes"] = fwdtest.Const(200, map[string]any{
		"viptela": map[string]any{"vendor": "viptela_smart", "collectionSourceType": "CONTROLLER_MANAGED", "relatedTypes": []string{"cisco_sdwan_ssh", "cisco_ios_xe_ssh"}},
		"cisco_sdwan_ssh": map[string]any{"vendor": "viptela", "collectionSourceType": "CLASSIC"}, "cisco_ios_xe_ssh": map[string]any{"vendor": "cisco_ios_xe", "collectionSourceType": "CLASSIC"},
	})
	routes["GET /api/networks/n1/classic-devices"] = fwdtest.Const(200, []any{
		map[string]any{"name": "vedge-off", "type": "cisco_sdwan_ssh", "host": "198.51.100.1", "cliCredentialId": "L-1", "collect": false, "testResult": map[string]any{"discoveredType": "cisco_sdwan_ssh"}},
		map[string]any{"name": "vedge-on", "type": "cisco_sdwan_ssh", "host": "198.51.100.2", "cliCredentialId": "L-1", "testResult": map[string]any{"discoveredType": "cisco_sdwan_ssh"}},
		map[string]any{"name": "edge-nologin", "type": "cisco_ios_xe_ssh", "host": "198.51.100.3", "testResult": map[string]any{"discoveredType": "cisco_ios_xe_ssh", "controllerInfos": []any{map[string]any{"ipAddress": "192.0.2.10"}}}},
	})
	r, _ := mustRun(t, "inspect-collection", routes, `{"network_id":"n1","view":"migration","limit":1}`)
	var m map[string]any
	for _, e := range r.Evidence {
		if v, ok := e.Detail["migration"].(map[string]any); ok {
			m = v
		}
	}
	plan, _ := m["migration_plan"].(map[string]any)
	if plan == nil || plan["devices_in_plan"] != 3 {
		t.Fatalf("all three devices are in the plan whatever limit says: %v", plan)
	}
	def := plan["definition"].(map[string]any)
	if len(def["controllers"].([]map[string]any)) != 0 || len(def["managedDevices"].([]map[string]any)) != 3 {
		t.Errorf("controllers left empty, three managed devices: %v", def)
	}
	rev := plan["review"].(map[string]any)
	if v := rev["vedges_whose_collect_is_not_false"].(map[string]any); v["count"] != 1 || fmt.Sprint(v["names"]) != "[vedge-on]" {
		t.Errorf("only vedge-on is still collecting: %v", v)
	}
	if v := rev["devices_with_no_login_recorded"].(map[string]any); v["count"] != 1 {
		t.Errorf("edge-nologin has no login: %v", v)
	}
}
