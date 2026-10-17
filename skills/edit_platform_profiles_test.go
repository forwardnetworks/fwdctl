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

// An existing CLI profile's commands can be changed in place (every endpoint using it follows at its next collection); the dry run shows the before and after and how many endpoints of this network use it,
// and an SNMP or HTTP profile is not edited here.
func TestEditPlatformEndpointProfileUpdateChangesCLICommandsInPlace(t *testing.T) {
	cur := map[string]any{"id": "CLI-1", "name": "linux", "type": "CLI", "commandSets": []any{"UNIX"}, "customCommands": []any{"uname -r"}}
	var patch map[string]any
	routes := map[string]fwdtest.Handler{
		"GET /api/endpoint-profiles": func(*http.Request, []byte) (int, any) {
			return 200, []any{cur, map[string]any{"id": "SNMP-2", "name": "snmp", "type": "SNMP"}}
		},
		"PATCH /api/endpoint-profiles/CLI-1": func(_ *http.Request, b []byte) (int, any) {
			_ = json.Unmarshal(b, &patch)
			if cc, ok := patch["customCommands"]; ok {
				cur["customCommands"] = cc
			}
			return 200, cur
		},
		"POST /api/approved-cli-commands": fwdtest.Const(200, map[string]any{"approved": []any{}, "unapproved": []any{}}),
	}
	in := `{"area":"endpoint_profiles","action":"update","name":"linux","definition":{"custom_commands":["uname -r","hostname -f"]}`
	r, srv := mustRun(t, "edit-platform", routes, in+`}`)
	if srv.Called("PATCH", "/api/endpoint-profiles/CLI-1") || patch != nil || !r.Changes[0].Reversible {
		t.Fatalf("dry run wrote or is not reversible: %v", patch)
	}
	r, _ = mustRun(t, "edit-platform", routes, in+`,"apply":true}`)
	cc, _ := patch["customCommands"].([]any)
	if r.Status != result.OK || len(cc) != 2 || cc[1] != "hostname -f" {
		t.Fatalf("%s %s %v", r.Status, r.Finding, patch)
	}
	if _, _, err := runSkill(t, "edit-platform", routes, `{"area":"endpoint_profiles","action":"update","name":"snmp","definition":{"custom_commands":["x"]},"apply":true}`); err == nil {
		t.Errorf("an SNMP profile was edited as a CLI profile")
	}
	if _, _, err := runSkill(t, "edit-platform", routes, `{"area":"endpoint_profiles","action":"update","name":"linux","definition":{}}`); err == nil {
		t.Errorf("an update that changes nothing was accepted")
	}
}

// SNMP and HTTP endpoint profiles are created and updated here too, from the fields Forward documents (detector OID and patterns, name detector, custom OIDs, timeout; HTTP: https, auth, headers, detector URI, endpoints).
func TestEditPlatformEndpointProfileCreatesAndUpdatesSNMPAndHTTPProfiles(t *testing.T) {
	var created []map[string]any
	routes := profileRoutes(&created, []any{map[string]any{"id": "SNMP-3", "name": "entity", "type": "SNMP", "detectorOid": "1.3.6.1.2.1.1.2.0", "detectorPatterns": []any{"x"}}})
	routes["POST /api/endpoint-profiles"] = func(r *http.Request, body []byte) (int, any) {
		var b map[string]any
		_ = json.Unmarshal(body, &b)
		created = append(created, b)
		b["id"] = r.URL.Query().Get("type") + "-9"
		return 201, b
	}
	var patched []byte
	routes["PATCH /api/endpoint-profiles/SNMP-3"] = func(_ *http.Request, body []byte) (int, any) {
		patched = body
		return 200, map[string]any{"id": "SNMP-3", "name": "entity", "type": "SNMP", "detectorOid": "1.3.6.1.2.1.1.2.0", "detectorPatterns": []any{"x"}, "customOids": []any{map[string]any{"name": "model", "oid": "1.3.6.1.2.1.47.1.1.1.1.13"}}}
	}
	snmp := `{"area":"endpoint_profiles","action":"create","definition":{"name":"entity-mib","type":"SNMP","detector_oid":"1.3.6.1.2.1.1.1.0","detector_patterns":["Acme.*"],"name_detector_oid":"1.3.6.1.2.1.1.5.0","name_detector_patterns":["(.*)"],"custom_oids":[{"name":"model","oid":"1.3.6.1.2.1.47.1.1.1.1.13"}],"response_timeout_sec":10}`
	r, _ := mustRun(t, "edit-platform", routes, snmp+`}`)
	if len(created) != 0 || r.Mode != result.ModeDryRun || !strings.Contains(r.Finding, "SNMP") {
		t.Fatalf("dry run: %v %s", created, r.Finding)
	}
	r, _ = mustRun(t, "edit-platform", routes, snmp+`,"apply":true}`)
	if r.Status != result.OK || len(created) != 1 || created[0]["type"] != "SNMP" || created[0]["detectorOid"] != "1.3.6.1.2.1.1.1.0" || created[0]["customOids"] == nil {
		t.Fatalf("snmp create: %s %s %v", r.Status, r.Finding, created)
	}
	created = nil
	http := `{"area":"endpoint_profiles","action":"create","definition":{"name":"inventory-api","type":"HTTP","https":true,"auth_type":"BASIC_AUTH","detector_uri":"/api/version","detector_patterns":["ok"],"endpoints":[{"name":"devices","path":"/api/devices"}]},"apply":true}`
	r, _ = mustRun(t, "edit-platform", routes, http)
	if r.Status != result.OK || len(created) != 1 || created[0]["type"] != "HTTP" || created[0]["authType"] != "BASIC_AUTH" || created[0]["https"] != true {
		t.Fatalf("http create: %s %s %v", r.Status, r.Finding, created)
	}
	// an HTTP profile without an endpoint, and a type that is not CLI, SNMP or HTTP, are refused
	if _, _, err := runSkill(t, "edit-platform", routes, `{"area":"endpoint_profiles","action":"create","definition":{"name":"bad","type":"HTTP","https":true,"auth_type":"NONE"}}`); err == nil {
		t.Errorf("an HTTP profile needs an endpoint")
	}
	if _, _, err := runSkill(t, "edit-platform", routes, `{"area":"endpoint_profiles","action":"create","definition":{"name":"bad","type":"FTP"}}`); err == nil {
		t.Errorf("an unknown type is refused")
	}
	// update an SNMP profile: the given fields replace the current ones
	r, _ = mustRun(t, "edit-platform", routes, `{"area":"endpoint_profiles","action":"update","name":"SNMP-3","definition":{"custom_oids":[{"name":"model","oid":"1.3.6.1.2.1.47.1.1.1.1.13"}]},"apply":true}`)
	if r.Status != result.OK || !strings.Contains(string(patched), "customOids") || strings.Contains(string(patched), "detectorOid") {
		t.Fatalf("snmp update: %s %s %s", r.Status, r.Finding, patched)
	}
}

// create takes the profile name at the top level (where update and delete take it) as well as in the definition; a name in both must agree.
func TestEditPlatformEndpointProfileCreateAcceptsATopLevelName(t *testing.T) {
	var created []map[string]any
	routes := profileRoutes(&created, []any{})
	routes["POST /api/endpoint-profiles"] = func(r *http.Request, body []byte) (int, any) {
		var b map[string]any
		_ = json.Unmarshal(body, &b)
		created = append(created, b)
		b["id"] = r.URL.Query().Get("type") + "-9"
		return 201, b
	}
	r, _ := mustRun(t, "edit-platform", routes, `{"area":"endpoint_profiles","action":"create","name":"entity-mib","definition":{"type":"SNMP","detector_oid":"1.3.6.1.2.1.1.1.0","detector_patterns":["Acme.*"]},"apply":true}`)
	if r.Status != result.OK || len(created) != 1 || created[0]["name"] != "entity-mib" {
		t.Fatalf("top-level name: %s %s %v", r.Status, r.Finding, created)
	}
	created = nil
	r, _ = mustRun(t, "edit-platform", routes, `{"area":"endpoint_profiles","action":"create","name":"cli-x","definition":{"command_sets":["UNIX"]},"apply":true}`)
	if r.Status != result.OK || len(created) != 1 || created[0]["name"] != "cli-x" {
		t.Fatalf("cli top-level name: %s %s %v", r.Status, r.Finding, created)
	}
	if _, _, err := runSkill(t, "edit-platform", routes, `{"area":"endpoint_profiles","action":"create","name":"a","definition":{"name":"b","type":"SNMP"}}`); err == nil || !strings.Contains(err.Error(), "agree") {
		t.Errorf("two different names must be refused: %v", err)
	}
	if _, _, err := runSkill(t, "edit-platform", routes, `{"area":"endpoint_profiles","action":"create","definition":{"type":"SNMP"}}`); err == nil || !strings.Contains(err.Error(), "name") {
		t.Errorf("no name must say where to put it: %v", err)
	}
}
