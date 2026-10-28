package skills_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
)

func TestEditPlatformLicensingActivateIsDryRunNeedsTheOrgIDAndRefusesWhatForwardRefuses(t *testing.T) {
	var calls []string
	licenses := []any{
		map[string]any{"id": "L1", "license": map[string]any{"id": "L1"}},
		map[string]any{"id": "L2", "signed": true, "license": map[string]any{"id": "L2"}},
		map[string]any{"id": "L3", "invalidated": true, "license": map[string]any{"id": "L3"}},
	}
	routes := map[string]fwdtest.Handler{
		"GET /api/orgs/o1/licenses": fwdtest.Const(200, map[string]any{"licenses": licenses}),
		"POST /api/orgs/o1/licenses/L1": func(*http.Request, []byte) (int, any) {
			calls = append(calls, "activate")
			return 200, map[string]any{"id": "L1"}
		},
	}
	in := func(id, extra string) string {
		return `{"area":"licensing","action":"activate","name":"o1","definition":{"license_id":"` + id + `"}` + extra + `}`
	}
	r, _ := mustRun(t, "edit-platform", routes, in("L1", ""))
	if len(calls) != 0 || !strings.Contains(r.Finding, "Dry run") {
		t.Fatalf("dry run activated: %v %s", calls, r.Finding)
	}
	if _, _, err := runSkill(t, "edit-platform", routes, in("L1", `,"apply":true`)); err == nil || !strings.Contains(err.Error(), `"o1"`) || len(calls) != 0 {
		t.Fatalf("activate without confirm must be refused naming the org id: %v %v", calls, err)
	}
	mustRun(t, "edit-platform", routes, in("L1", `,"apply":true,"confirm":"o1"`))
	if len(calls) != 1 {
		t.Fatalf("activate with confirm should call once: %v", calls)
	}
	for _, id := range []string{"L2", "L3", "NOPE"} {
		if _, _, err := runSkill(t, "edit-platform", routes, in(id, "")); err == nil {
			t.Errorf("licence %s should be refused", id)
		}
	}
}
