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
