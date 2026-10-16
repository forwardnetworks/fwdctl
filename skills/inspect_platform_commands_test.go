package skills_test

import (
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

func commandGroupRoutes() map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/custom-command-groups": fwdtest.Const(200, []any{
			map[string]any{"name": "Change Validation MACsec", "deviceConnTypes": []string{"cisco_ios_xe_ssh"}, "type": "CLI", "commands": []string{"show mka session", "show mka summary"}, "disabled": false},
			map[string]any{"name": "Lab OIDs", "deviceConnTypes": []string{"arista_eos_ssh"}, "type": "SNMP", "oids": []any{map[string]any{"name": "x", "oid": "1.3.6.1.2.1.1"}}, "devices": []string{"lab-*"}, "disabled": true},
		}),
	}
}

// A custom command group is how a network adds commands to a device type's collection; it is not a Forward built-in. inspect-platform area custom_command_groups lists them, and `command` finds the groups that hold a command.
func TestPlatformCustomCommandGroupsListAndFindACommand(t *testing.T) {
	r, srv := mustRun(t, "inspect-platform", commandGroupRoutes(), `{"network_id":"n1","area":"custom_command_groups"}`)
	b := jsonOf(r)
	if writes(srv) != 0 || r.Status != result.OK || !strings.Contains(b, "Change Validation MACsec") || !strings.Contains(b, "show mka session") || !strings.Contains(b, `"disabled":true`) {
		t.Fatalf("%s %s", r.Status, b)
	}
	if !strings.Contains(b, "every device of those types") || !strings.Contains(b, "lab-*") {
		t.Errorf("an empty device list means every device of the types: %s", b)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "not a Forward built-in") || !strings.Contains(strings.Join(r.Limits, " "), "interactive") {
		t.Errorf("the limits should say what a custom group is and how the org-wide disable reaches it: %v", r.Limits)
	}
	r, _ = mustRun(t, "inspect-platform", commandGroupRoutes(), `{"network_id":"n1","area":"custom_command_groups","command":"MKA SESSION"}`)
	b = jsonOf(r)
	if !strings.Contains(b, "Change Validation MACsec") || strings.Contains(b, "Lab OIDs") || !strings.Contains(r.Finding, "1 ") {
		t.Errorf("only the group holding the command, matched ignoring case: %s %s", r.Finding, b)
	}
	if _, _, err := runSkill(t, "inspect-platform", commandGroupRoutes(), `{"network_id":"n1","area":"credentials","command":"x"}`); err == nil {
		t.Errorf("command belongs to area custom_command_groups")
	}
}
