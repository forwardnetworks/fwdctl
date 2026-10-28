package skills_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

// controller_setup action migrate is Forward's atomic move of classic devices into a controller-managed setup: it deletes the classic devices the setup names and creates the setup in one call. The dry run lists what
// would be deleted (name, type, host, collector, credentials), refuses a guest with no login, says it cannot be undone by update, and the apply needs the setup's name as confirm.
func migrateRoutes(posted *string) map[string]fwdtest.Handler {
	var setups []any
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/controller-managed-setups": func(*http.Request, []byte) (int, any) { return 200, setups },
		"GET /api/networks/n1/classic-devices": fwdtest.Const(200, []any{
			map[string]any{"name": "edge1", "host": "192.0.2.21", "type": "viptela", "collectorId": "C2", "cliCredentialId": "L1"},
			map[string]any{"name": "edge2", "host": "192.0.2.22", "type": "viptela"},
			map[string]any{"name": "other", "host": "192.0.2.99", "type": "arista_eos_ssh"},
		}),
		"POST /api/networks/n1/controller-managed-setups": func(r *http.Request, b []byte) (int, any) {
			*posted = r.URL.RawQuery + " " + string(b)
			setups = []any{map[string]any{"name": "sdwan", "controllers": []any{map[string]any{"name": "ctl"}}}}
			return 201, setups[0]
		},
	}
}

const migrateIn = `{"network_id":"n1","object":"controller_setup","action":"migrate","definition":{"name":"sdwan","controllers":[{"name":"ctl","host":"192.0.2.10","cliCredentialId":"L1"}],"managedDevices":[{"name":"edge1","cliCredentialId":"L1"},{"name":"EDGE2","cliCredentialId":"L1"},{"name":"new1","cliCredentialId":"L1"}]}`

func TestMigrateDryRunListsTheClassicDevicesItWouldDeleteAndWritesNothing(t *testing.T) {
	var posted string
	r, srv := mustRun(t, "edit-source", migrateRoutes(&posted), migrateIn+`}`)
	if r.Mode != result.ModeDryRun || writes(srv) != 0 || posted != "" {
		t.Fatalf("dry run: %s %s", r.Mode, r.Finding)
	}
	b := jsonOf(r)
	for _, want := range []string{"edge1", "edge2", "192.0.2.21", "viptela", "C2", "L1", "cannot be undone", "tags"} {
		if !strings.Contains(b, want) {
			t.Errorf("dry run should show %q: %s", want, b)
		}
	}
	if strings.Contains(b, "192.0.2.99") {
		t.Errorf("a device the setup does not name is not removed: %s", b)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "collectorId") {
		t.Errorf("the deleted devices were pinned to a collector (C2) and the setup names none: the limits must say a setup has one collector: %v", r.Limits)
	}
	if !strings.Contains(r.Finding, "2 classic device") {
		t.Errorf("finding: %s", r.Finding)
	}
}

func TestMigrateRefusesAGuestWithoutALoginAndNeedsTheSetupNameToApply(t *testing.T) {
	var posted string
	noLogin := strings.Replace(migrateIn, `{"name":"new1","cliCredentialId":"L1"}`, `{"name":"new1"}`, 1)
	if _, _, err := runSkill(t, "edit-source", migrateRoutes(&posted), noLogin+`}`); err == nil || !strings.Contains(err.Error(), "new1") || !strings.Contains(err.Error(), "cliCredentialId") {
		t.Errorf("a guest with no cliCredentialId cannot be logged into; it must be refused naming it: %v", err)
	}
	if _, _, err := runSkill(t, "edit-source", migrateRoutes(&posted), migrateIn+`,"apply":true}`); err == nil || posted != "" {
		t.Errorf("apply without confirm must be refused: %v %q", err, posted)
	}
	r, _ := mustRun(t, "edit-source", migrateRoutes(&posted), migrateIn+`,"apply":true,"confirm":"sdwan"}`)
	if r.Status != result.OK || !strings.HasPrefix(posted, "action=migrate ") {
		t.Errorf("apply: %s %s posted %q", r.Status, r.Finding, posted)
	}
}

// A controller entry takes the HTTP login of an API-collected controller (Versa Director, APIC), a port and the privileged-mode login, and sends them: a migrate carries nothing over from the record it deletes.
func TestMigrateControllerEntryTakesTheHTTPLoginPortAndExtraCLILogins(t *testing.T) {
	var posted string
	in := `{"network_id":"n1","object":"controller_setup","action":"migrate","apply":true,"confirm":"sdwan","definition":{"name":"sdwan","controllers":[{"name":"ctl","type":"versa_director_api","host":"192.0.2.10","port":9182,"httpCredentialId":"H-25","cliCredential2Id":"P-2","jumpServerId":"J1"}],"managedDevices":[{"name":"edge1","cliCredentialId":"L1"}]}}`
	r, _ := mustRun(t, "edit-source", migrateRoutes(&posted), in)
	if r.Status != result.OK || !strings.Contains(posted, `"httpCredentialId":"H-25"`) || !strings.Contains(posted, `"port":9182`) || !strings.Contains(posted, `"cliCredential2Id":"P-2"`) || !strings.Contains(posted, `"jumpServerId":"J1"`) {
		t.Fatalf("the migrate body must restate the controller's logins and port: %s %s", r.Finding, posted)
	}
}

// A managed entry restates the collection options of the classic record it replaces (SDK v0.7.0 sends them flat, as Forward reads them).
func TestMigrateManagedEntryCarriesTheCollectionOptions(t *testing.T) {
	var posted string
	in := `{"network_id":"n1","object":"controller_setup","action":"migrate","apply":true,"confirm":"sdwan","definition":{"name":"sdwan","controllers":[{"name":"ctl","host":"192.0.2.10","cliCredentialId":"L1"}],"managedDevices":[{"name":"edge1","cliCredentialId":"L1","collectBgpAdvertisements":true,"bgpTableType":"ADJ_RIB_IN","bgpPeerType":"EBGP","paginationMode":"DISABLE_PAGINATION","note":"pilot"}]}}`
	r, _ := mustRun(t, "edit-source", migrateRoutes(&posted), in)
	for _, want := range []string{`"collectBgpAdvertisements":true`, `"bgpTableType":"ADJ_RIB_IN"`, `"paginationMode":"DISABLE_PAGINATION"`, `"note":"pilot"`} {
		if r.Status != result.OK || !strings.Contains(posted, want) {
			t.Fatalf("the body must carry %s: %s %s", want, r.Finding, posted)
		}
	}
}

// A managed entry restates collect:false (a device the customer switched off); the migrate body must carry it, since Forward reads it from the entry.
func TestMigrateManagedEntryCarriesCollectFalse(t *testing.T) {
	var posted string
	in := `{"network_id":"n1","object":"controller_setup","action":"migrate","apply":true,"confirm":"sdwan","definition":{"name":"sdwan","controllers":[{"name":"ctl","host":"192.0.2.10","cliCredentialId":"L1"}],"managedDevices":[{"name":"edge1","cliCredentialId":"L1","collect":false},{"name":"edge2","cliCredentialId":"L1"}]}}`
	r, _ := mustRun(t, "edit-source", migrateRoutes(&posted), in)
	if r.Status != result.OK || !strings.Contains(posted, `"collect":false`) {
		t.Fatalf("the migrate body must carry collect:false: %s %s", r.Finding, posted)
	}
}

func memberRoutes(patched *string) map[string]fwdtest.Handler {
	setup := map[string]any{"name": "sdwan", "controllers": []any{map[string]any{"name": "vsmart-a", "host": "192.0.2.10"}}, "managedDevices": []any{map[string]any{"name": "edge1"}}}
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/controller-managed-setups": fwdtest.Const(200, []any{setup}),
		"PATCH /api/networks/n1/controller-managed-setups/sdwan/controllers/vsmart-a": func(r *http.Request, b []byte) (int, any) {
			*patched = r.URL.Path + " " + string(b)
			return 200, map[string]any{"name": "vsmart-b", "host": "192.0.2.10"}
		},
		"PATCH /api/networks/n1/controller-managed-setups/sdwan/managed-devices/edge1": func(r *http.Request, b []byte) (int, any) {
			*patched = r.URL.Path + " " + string(b)
			return 200, map[string]any{"name": "edge1", "collect": false}
		},
	}
}

func TestControllerSetupUpdateRenamesOneControllerInPlace(t *testing.T) {
	var patched string
	in := `{"network_id":"n1","object":"controller_setup","action":"update","name":"sdwan","definition":{"controller":"vsmart-a","set":{"name":"vsmart-b"}}`
	r, srv := mustRun(t, "edit-source", memberRoutes(&patched), in+`}`)
	if r.Mode != result.ModeDryRun || patched != "" || writes(srv) != 0 {
		t.Fatalf("a dry run must write nothing: %s %q", r.Mode, patched)
	}
	mustRun(t, "edit-source", memberRoutes(&patched), in+`,"apply":true}`)
	if !strings.Contains(patched, "/controllers/vsmart-a") || !strings.Contains(patched, `"name":"vsmart-b"`) {
		t.Fatalf("the rename must be one PATCH on the controller: %q", patched)
	}
}

func TestControllerSetupUpdateSetsCollectOnOneManagedDevice(t *testing.T) {
	var patched string
	in := `{"network_id":"n1","object":"controller_setup","action":"update","name":"sdwan","apply":true,"definition":{"managed_device":"edge1","set":{"collect":false}}}`
	mustRun(t, "edit-source", memberRoutes(&patched), in)
	if !strings.Contains(patched, "/managed-devices/edge1") || !strings.Contains(patched, `"collect":false`) {
		t.Fatalf("collect must be one PATCH on the managed device: %q", patched)
	}
}

func TestControllerSetupUpdateRefusesAnEmptyControllerListAndAMixedChange(t *testing.T) {
	var patched string
	for _, def := range []string{`{"controllers":[]}`, `{"controller":"vsmart-a","set":{"host":"192.0.2.9"},"managedDevices":[]}`, `{"controller":"nope","set":{"host":"192.0.2.9"}}`} {
		if _, _, err := runSkill(t, "edit-source", memberRoutes(&patched), `{"network_id":"n1","object":"controller_setup","action":"update","name":"sdwan","definition":`+def+`}`); err == nil {
			t.Errorf("%s must be refused", def)
		}
	}
}
