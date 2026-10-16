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

// view effective of area custom_command_groups, with name = a device, lists the custom groups that apply to that one device: the group's device types include the device's type and its devices list is empty or matches the name.
func TestPlatformEffectiveCustomCommandsForOneDevice(t *testing.T) {
	routes := commandGroupRoutes()
	routes["GET /api/networks/n1/classic-devices"] = fwdtest.Const(200, []any{
		map[string]any{"name": "xe-1", "host": "h", "type": "cisco_ios_xe_ssh"},
		map[string]any{"name": "eos-1", "host": "h", "type": "arista_eos_ssh"},
		map[string]any{"name": "lab-7", "host": "h", "type": "arista_eos_ssh"},
	})
	routes["GET /api/networks/n1/custom-command-groups"] = fwdtest.Const(200, []any{
		map[string]any{"name": "Macsec", "deviceConnTypes": []string{"cisco_ios_xe_ssh"}, "type": "CLI", "commands": []string{"show mka session"}},
		map[string]any{"name": "Lab only", "deviceConnTypes": []string{"arista_eos_ssh"}, "type": "CLI", "commands": []string{"show x"}, "devices": []string{"LAB-*"}},
		map[string]any{"name": "Off", "deviceConnTypes": []string{"cisco_ios_xe_ssh"}, "type": "CLI", "commands": []string{"show y"}, "disabled": true},
	})
	for dev, want := range map[string]string{"xe-1": "Macsec", "lab-7": "Lab only", "eos-1": ""} {
		r, _ := mustRun(t, "inspect-platform", routes, `{"network_id":"n1","area":"custom_command_groups","view":"effective","name":"`+dev+`"}`)
		b := jsonOf(r)
		if want != "" && !strings.Contains(b, want) {
			t.Errorf("%s should get %q: %s", dev, want, b)
		}
		if strings.Contains(b, `"name":"Off"`) && !strings.Contains(b, `"applies":false`) {
			t.Errorf("a disabled group does not apply: %s", b)
		}
		if dev == "eos-1" && (strings.Contains(b, "Lab only") || strings.Contains(b, "Macsec")) && !strings.Contains(r.Finding, "0 ") {
			t.Errorf("eos-1 matches no custom group: %s %s", r.Finding, b)
		}
	}
	if _, _, err := runSkill(t, "inspect-platform", routes, `{"network_id":"n1","area":"custom_command_groups","view":"effective"}`); err == nil {
		t.Errorf("view effective needs name, the device")
	}
}
