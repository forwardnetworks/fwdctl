package skills_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

// custom_command_group is how a network's added commands are created, disabled or removed (the org-wide disable_commands is for built-ins). Create and update are checked with Forward's own validation first, so a
// command the device type refuses is reported in the dry run; delete needs confirm.
func commandGroupEditRoutes(calls *[]string, refuse bool) map[string]fwdtest.Handler {
	groups := []any{map[string]any{"name": "Macsec", "deviceConnTypes": []string{"cisco_ios_xe_ssh"}, "type": "CLI", "commands": []string{"show mka session"}, "disabled": false}}
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/custom-command-groups": func(*http.Request, []byte) (int, any) { return 200, groups },
		"POST /api/networks/n1/custom-command-groups": func(r *http.Request, b []byte) (int, any) {
			if r.URL.Query().Get("action") == "validate" {
				if refuse {
					return 200, map[string]any{"unsupported": []any{map[string]any{"command": "reload", "unsupportedConnTypes": []string{"cisco_ios_xe_ssh"}}}}
				}
				return 200, map[string]any{"unsupported": []any{}}
			}
			*calls = append(*calls, "create "+string(b))
			groups = append(groups, map[string]any{"name": "New group"})
			return 201, nil
		},
		"PATCH /api/networks/n1/custom-command-groups/Macsec": func(_ *http.Request, b []byte) (int, any) {
			*calls = append(*calls, "update "+string(b))
			return 204, nil
		},
		"DELETE /api/networks/n1/custom-command-groups/Macsec": func(*http.Request, []byte) (int, any) {
			*calls = append(*calls, "delete")
			groups = nil
			return 204, nil
		},
	}
}

func TestCustomCommandGroupCreateIsValidatedFirstAndAppliesOnlyWithApply(t *testing.T) {
	var calls []string
	in := `{"network_id":"n1","object":"custom_command_group","action":"create","definition":{"name":"New group","deviceConnTypes":["cisco_ios_xe_ssh"],"commands":["show version"]}`
	r, srv := mustRun(t, "edit-source", commandGroupEditRoutes(&calls, false), in+`}`)
	if r.Mode != result.ModeDryRun || writes(srv) != 1 || len(calls) != 0 { // the validate call is a POST but changes nothing
		t.Fatalf("dry run: %s writes %d calls %v", r.Mode, writes(srv), calls)
	}
	r, _ = mustRun(t, "edit-source", commandGroupEditRoutes(&calls, false), in+`,"apply":true}`)
	if r.Status != result.OK || len(calls) != 1 || !strings.Contains(calls[0], `"name":"New group"`) || !strings.Contains(calls[0], "show version") {
		t.Errorf("apply: %s %v", r.Status, calls)
	}
	calls = nil
	if _, _, err := runSkill(t, "edit-source", commandGroupEditRoutes(&calls, true), in+`,"apply":true}`); err == nil || !strings.Contains(err.Error(), "reload") || len(calls) != 0 {
		t.Errorf("a command the device type refuses must be named and nothing created: %v %v", err, calls)
	}
	if _, _, err := runSkill(t, "edit-source", commandGroupEditRoutes(&calls, false), `{"network_id":"n1","object":"custom_command_group","action":"create","definition":{"name":"Macsec","deviceConnTypes":["x"],"commands":["show version"]}}`); err == nil {
		t.Errorf("a group of that name already exists")
	}
}

func TestCustomCommandGroupUpdateDisablesAndDeleteNeedsConfirm(t *testing.T) {
	var calls []string
	up := `{"network_id":"n1","object":"custom_command_group","action":"update","name":"Macsec","definition":{"disabled":true}`
	r, _ := mustRun(t, "edit-source", commandGroupEditRoutes(&calls, false), up+`}`)
	b := jsonOf(r)
	if r.Mode != result.ModeDryRun || len(calls) != 0 || !strings.Contains(b, "show mka session") || !strings.Contains(b, "undo") {
		t.Fatalf("dry run shows the group before the change: %s", b)
	}
	r, _ = mustRun(t, "edit-source", commandGroupEditRoutes(&calls, false), up+`,"apply":true}`)
	if r.Status != result.OK || len(calls) != 1 || !strings.Contains(calls[0], `"disabled":true`) {
		t.Errorf("update: %s %v", r.Status, calls)
	}
	calls = nil
	del := `{"network_id":"n1","object":"custom_command_group","action":"delete","name":"Macsec","apply":true`
	if _, _, err := runSkill(t, "edit-source", commandGroupEditRoutes(&calls, false), del+`}`); err == nil || len(calls) != 0 {
		t.Errorf("delete without confirm: %v %v", err, calls)
	}
	r, _ = mustRun(t, "edit-source", commandGroupEditRoutes(&calls, false), del+`,"confirm":"Macsec"}`)
	if r.Status != result.OK || len(calls) != 1 || calls[0] != "delete" {
		t.Errorf("delete: %s %v", r.Status, calls)
	}
}
