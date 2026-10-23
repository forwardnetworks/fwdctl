package skills_test

import (
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
		tr(map[string]any{"name": "edge1", "host": "198.51.100.1", "cliCredentialId": "L-edge", "jumpServerId": "J2", "collectorId": "C7", "testResult": map[string]any{"discoveredType": "cisco_ios_xe_ssh", "controllerInfos": []any{vm}}}),
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
	if sm["object"] != "controller_setup" || sm["action"] != "migrate" || cs[0]["cliCredentialId"] != "L-ctl" || cs[0]["name"] != "vmanage1" || len(md) != 2 || md[0]["cliCredentialId"] != "L-edge" {
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
	def := sm["definition"].(map[string]any)
	if def["name"] == nil || !strings.Contains(sm["warning"].(string), "DELETES") {
		t.Errorf("name in the definition and a delete warning: %v", sm)
	}
	h := sm["credential_hints"].(map[string]any)["devices_without_a_recorded_login"].(map[string]any)
	if sib, _ := h["vedge2"].(map[string]int); sib["L-9"] != 1 {
		t.Errorf("vedge2 has no login recorded; its same-subnet sibling uses L-9: %v", h)
	}
}
