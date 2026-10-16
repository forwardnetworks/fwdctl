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
