package skills_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

// A CLI endpoint profile says which commands Forward runs on the endpoints that use it (a predefined set such as UNIX, plus custom commands). It is organization-wide, so it is an edit-platform
// area. A custom command only runs if the organization approved it, which the dry run reads from Forward and says.
func profileRoutes(created *[]map[string]any, existing []any) map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		"GET /api/endpoint-profiles": func(*http.Request, []byte) (int, any) {
			out := append([]any{}, existing...)
			for _, c := range *created {
				out = append(out, c)
			}
			return 200, out
		},
		"POST /api/endpoint-profiles": func(_ *http.Request, body []byte) (int, any) {
			var b map[string]any
			_ = json.Unmarshal(body, &b)
			*created = append(*created, b)
			b["id"] = "CLI-9"
			return 201, b
		},
		"POST /api/approved-cli-commands": func(_ *http.Request, body []byte) (int, any) {
			var cmds []string
			_ = json.Unmarshal(body, &cmds)
			un := []string{}
			for _, c := range cmds {
				if strings.Contains(c, "rm ") {
					un = append(un, c)
				}
			}
			return 200, map[string]any{"approved": []string{}, "unapproved": un}
		},
		"DELETE /api/endpoint-profiles/CLI-9": func(*http.Request, []byte) (int, any) { return 204, nil },
	}
}

func TestEditPlatformEndpointProfileCreatesACLIProfileAndSaysWhichCommandsAreNotApproved(t *testing.T) {
	var created []map[string]any
	in := `{"area":"endpoint_profiles","action":"create","definition":{"name":"containerlab-linux","command_sets":["unix"],"custom_commands":["uname -r","rm -rf /tmp/x"]}`
	r, _ := mustRun(t, "edit-platform", profileRoutes(&created, []any{}), in+`}`)
	if len(created) != 0 || r.Changes[0].Applied {
		t.Fatalf("a dry run created: %v", created)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "rm -rf /tmp/x") || !strings.Contains(strings.Join(r.Limits, " "), "not approved") {
		t.Errorf("the dry run should name the unapproved command: %v", r.Limits)
	}
	r, _ = mustRun(t, "edit-platform", profileRoutes(&created, []any{}), in+`,"apply":true}`)
	cs, _ := created[0]["commandSets"].([]any)
	if r.Status != result.OK || len(created) != 1 || created[0]["name"] != "containerlab-linux" || created[0]["type"] != "CLI" || len(cs) != 1 || cs[0] != "UNIX" {
		t.Fatalf("%s %s %v", r.Status, r.Finding, created)
	}
}

func TestEditPlatformEndpointProfileRefusesWhatForwardWouldReject(t *testing.T) {
	var created []map[string]any
	for name, def := range map[string]string{
		"name taken":       `{"name":"Linux","command_sets":["UNIX"]}`,
		"no name":          `{"command_sets":["UNIX"]}`,
		"no commands":      `{"name":"empty"}`,
		"unknown set":      `{"name":"x","command_sets":["WINDOWS"]}`,
		"unsupported type": `{"name":"x","type":"SNMP","command_sets":["UNIX"]}`,
	} {
		routes := profileRoutes(&created, []any{map[string]any{"id": "CLI-1", "name": "linux", "type": "CLI"}})
		if _, _, err := runSkill(t, "edit-platform", routes, `{"area":"endpoint_profiles","action":"create","definition":`+def+`,"apply":true}`); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if len(created) != 0 {
		t.Errorf("a refused call still created: %v", created)
	}
}
