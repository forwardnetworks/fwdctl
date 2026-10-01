package skills_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

func ready1() fwdtest.Handler {
	return fwdtest.Snapshots(fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z"))
}

func TestEnvironmentReportsBuildAndSaysWhenTheIndexWasNeverUpdated(t *testing.T) {
	r, _ := mustRun(t, "inspect-environment", map[string]fwdtest.Handler{
		"GET /api/version":       fwdtest.Const(200, map[string]any{"version": "26.9.0", "release": "26.9", "build": "abc"}),
		"GET /api/orgs/current":  fwdtest.Const(200, map[string]any{"id": "o1", "name": "Acme"}),
		"GET /api/users/current": fwdtest.Const(200, map[string]any{"id": "u1", "username": "me", "email": "me@example.com"}),
		"GET /api/cve-index":     fwdtest.Const(200, map[string]any{"digest": "d", "indexCreatedAt": "2026-08-01T00:00:00Z"}),
		"GET /api/networks":      fwdtest.Const(200, []any{map[string]any{"id": "n1", "name": "a"}}),
	}, `{}`)
	b, _ := json.Marshal(r)
	if r.Status != result.OK || r.Context.Scope != "account" || !strings.Contains(r.Finding, "26.9.0") {
		t.Fatalf("%s %s %+v", r.Status, r.Finding, r.Context)
	}
	if !strings.Contains(strings.Join(r.Limits, "|"), "never updated") || strings.Contains(string(b), "me@example.com") {
		t.Errorf("limits %v; the email must not be copied out: %s", r.Limits, b)
	}
}

func TestEnvironmentIsUnknownWhenNothingCanBeRead(t *testing.T) {
	r, _ := mustRun(t, "inspect-environment", map[string]fwdtest.Handler{}, `{}`)
	if r.Status != result.Unknown {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
}

func TestExternalConnectivitySaysWhenNoInternetNodeIsModelled(t *testing.T) {
	r, _ := mustRun(t, "inspect-external-connectivity", map[string]fwdtest.Handler{
		snapsPath:                                  ready1(),
		"GET /api/networks/n1/internet-node":       fwdtest.Const(404, map[string]any{"message": "none"}),
		"GET /api/networks/n1/intranet-nodes":      fwdtest.Const(200, []any{map[string]any{"name": "partner", "connections": []any{map[string]any{"uplinkPort": map[string]any{"device": "r1", "port": "eth1"}, "subnets": []any{"10.9.0.0/16"}}}}}),
		"GET /api/networks/n1/l3-vpns":             fwdtest.Const(200, []any{}),
		"GET /api/snapshots/s1/topology/overrides": fwdtest.Const(200, map[string]any{"present": []any{map[string]any{"port1": "r1 eth2", "port2": "r2 eth2"}}, "absent": []any{}}),
	}, `{"network_id":"n1"}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "internet node not modelled") || !strings.Contains(r.Finding, "1 intranet") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	if !strings.Contains(strings.Join(r.Limits, "|"), "no internet node") {
		t.Errorf("%v", r.Limits)
	}
	if !strings.Contains(jsonOf(r), "r1 eth2") || !strings.Contains(jsonOf(r), "r1 eth1") {
		t.Errorf("%s", jsonOf(r))
	}
}

func jsonOf(r result.Result) string { b, _ := json.Marshal(r); return string(b) }

func checkList(cs ...map[string]any) fwdtest.Handler {
	return fwdtest.Const(200, cs)
}

func TestChecksFailedListsFailingFirstAndAnEmptyListIsUnknown(t *testing.T) {
	c := func(id, status string, v int) map[string]any {
		m := map[string]any{"id": id, "name": "check " + id, "status": status, "priority": "HIGH"}
		if status == "FAIL" {
			m["numViolations"] = v
		}
		return m
	}
	r, _ := mustRun(t, "inspect-checks", map[string]fwdtest.Handler{snapsPath: ready1(),
		"GET /api/snapshots/s1/checks": checkList(c("a", "PASS", 0), c("b", "FAIL", 4))}, `{"network_id":"n1"}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "1 of 2 checks are failing") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	if !strings.Contains(jsonOf(r), `"checks":[{"id":"b"`) {
		t.Errorf("failing first: %s", jsonOf(r))
	}
	r, _ = mustRun(t, "inspect-checks", map[string]fwdtest.Handler{snapsPath: ready1(), "GET /api/snapshots/s1/checks": checkList()}, `{"network_id":"n1"}`)
	if r.Status != result.Unknown {
		t.Fatalf("an empty list is not health: %s", r.Status)
	}
}

func TestChecksCatalogueAndMissingCheck(t *testing.T) {
	r, _ := mustRun(t, "inspect-checks", map[string]fwdtest.Handler{
		"GET /api/predefinedChecks": fwdtest.Const(200, []any{map[string]any{"name": "Loops", "predefinedCheckType": "LOOP", "description": "d"}})}, `{"network_id":"n1","catalogue":true}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "1 predefined") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	r, _ = mustRun(t, "inspect-checks", map[string]fwdtest.Handler{snapsPath: ready1(), "GET /api/snapshots/s1/checks/zz": fwdtest.Const(404, map[string]any{})},
		`{"network_id":"n1","check_id":"zz"}`)
	if r.Status != result.Unknown {
		t.Fatalf("%s", r.Status)
	}
}

func TestCollectionConfigNeverCopiesACredentialOut(t *testing.T) {
	r, _ := mustRun(t, "inspect-collection-config", map[string]fwdtest.Handler{
		"GET /api/networks/n1/classic-devices": fwdtest.Const(200, []any{
			map[string]any{"name": "r1", "host": "10.0.0.1", "type": "CISCO_IOS", "cliCredentialId": "cred-SECRET-1", "collect": false, "note": "pw is hunter2"}}),
		"GET /api/networks/n1/jumpServers": fwdtest.Const(200, []any{map[string]any{"id": "j1", "host": "jump", "username": "admin-user"}}),
	}, `{"network_id":"n1"}`)
	b := jsonOf(r)
	for _, leak := range []string{"cred-SECRET-1", "hunter2", "admin-user"} {
		if strings.Contains(b, leak) {
			t.Errorf("%q leaked into the result: %s", leak, b)
		}
	}
	if r.Status != result.OK || !strings.Contains(b, `"cli_credential_set":true`) || !strings.Contains(b, `"devices_not_collected":1`) {
		t.Fatalf("%s %s", r.Status, b)
	}
}

func TestCollectionConfigWithNothingConfiguredIsUnknown(t *testing.T) {
	r, _ := mustRun(t, "inspect-collection-config", map[string]fwdtest.Handler{
		"GET /api/networks/n1/classic-devices": fwdtest.Const(200, []any{})}, `{"network_id":"n1"}`)
	if r.Status != result.Unknown {
		t.Fatalf("%s", r.Status)
	}
}

func TestEnvironmentExplainsNonDefaultPropertiesAndNamesTheUnexplained(t *testing.T) {
	r, _ := mustRun(t, "inspect-environment", map[string]fwdtest.Handler{
		"GET /api/version": fwdtest.Const(200, map[string]any{"version": "1.0.0", "release": "r", "build": "b"}),
		"GET /api/config":  fwdtest.Const(200, map[string]any{"background_snapshot_reprocess": "LOW_PRIORITY", "advanced_reachability_analysis": "ASYNC", "some_new_property": "1"}),
	}, `{"include_properties":true}`)
	b, _ := json.Marshal(r.Evidence)
	text := string(b)
	for _, want := range []string{`"explanations"`, `"default":"HIGH_PRIORITY"`, "inspect-vulnerabilities cannot give internet_addressable"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %s", want, text)
		}
	}
	if !strings.Contains(strings.Join(r.Limits, "|"), "no explanation is held for these properties (read Forward's documentation): some_new_property") {
		t.Errorf("an unexplained property is named, not guessed at: %v", r.Limits)
	}
}

// An endpoint shows the profile it uses and what that profile asks for; HTTP header values (which may hold a token) are never copied out.
func TestCollectionConfigShowsTheEndpointProfilesInUseAndHidesHeaderValues(t *testing.T) {
	r, _ := mustRun(t, "inspect-collection-config", map[string]fwdtest.Handler{
		"GET /api/networks/n1/endpoints": fwdtest.Const(200, []any{
			map[string]any{"type": "SNMP", "name": "e1", "host": "10.1.1.1", "profileId": "SNMP-5"},
			map[string]any{"type": "HTTP", "name": "e2", "host": "10.1.1.2", "profileId": "HTTP-4"}}),
		"GET /api/endpoint-profiles": fwdtest.Const(200, map[string]any{"profiles": []any{
			map[string]any{"id": "SNMP-5", "name": "snmp-x", "type": "SNMP", "oidSets": []string{"STANDARD"}, "customOids": []any{map[string]any{"name": "serial", "oid": "1.3.6.1.4.1.1.2"}}},
			map[string]any{"id": "HTTP-4", "name": "http-x", "type": "HTTP", "https": true, "authType": "NONE", "headers": map[string]any{"X-Api-Key": "TOKEN-SECRET"}, "endpoints": []any{map[string]any{"name": "info", "path": "/info"}}},
			map[string]any{"id": "CLI-1", "name": "unused", "type": "CLI"}}}),
	}, `{"network_id":"n1"}`)
	b := jsonOf(r)
	if r.Status != result.OK || !strings.Contains(b, `"serial"`) || !strings.Contains(b, `"X-Api-Key"`) || strings.Contains(b, "TOKEN-SECRET") || strings.Contains(b, "unused") {
		t.Fatalf("%s %s", r.Status, b)
	}
}
