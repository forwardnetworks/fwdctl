package skills_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

// discovery start scans the network from a collector, so it is a dry run that says so (with the scan rate limit), refuses when a discovery is already running, and applies only with confirm = the discovery type.
func discoveryWriteRoutes(running bool, calls *[]string) map[string]fwdtest.Handler {
	var classic []any
	jobs := []any{map[string]any{"id": "J1", "type": "SUBNET_SCAN", "collectorId": "C7", "startedAt": "2026-10-16T01:00:00Z", "endedAt": "2026-10-16T01:30:00Z"}}
	if running {
		jobs = []any{map[string]any{"id": "J2", "type": "SUBNET_SCAN", "collectorId": "C7", "startedAt": "2026-10-16T02:00:00Z"}}
	}
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/discovered-devices": fwdtest.Const(200, map[string]any{"jobs": jobs, "devices": []any{
			map[string]any{"name": "new1", "host": "192.0.2.2", "type": "arista_eos_ssh", "cliCredentialId": "L1", "jumpServerId": "JS1", "jobId": "J1"},
			map[string]any{"name": "mystery", "host": "192.0.2.3", "type": "unknown", "jobId": "J1"},
			map[string]any{"name": "nohost", "type": "arista_eos_ssh", "jobId": "J1"},
		}}),
		"GET /api/collection-settings": fwdtest.Const(200, map[string]any{"maxScanConnectionsPerSecond": 500}),
		"POST /api/networks/n1/deviceDiscovery": func(r *http.Request, b []byte) (int, any) {
			*calls = append(*calls, "discovery "+r.URL.RawQuery+" "+string(b))
			return 200, map[string]any{"taskId": "P8781"}
		},
		"GET /api/networks/n1/classic-devices": func(*http.Request, []byte) (int, any) { return 200, classic },
		"POST /api/networks/n1/classic-devices": func(_ *http.Request, b []byte) (int, any) {
			*calls = append(*calls, "add "+string(b))
			classic = append(classic, map[string]any{"name": "new1", "host": "192.0.2.2"})
			return 201, map[string]any{"name": "new1"}
		},
	}
}

func TestDiscoveryStartIsADryRunThatSaysItScansAndNeedsTheTypeToConfirm(t *testing.T) {
	var calls []string
	in := `{"network_id":"n1","object":"discovery","action":"start","definition":{"type":"SUBNET_SCAN","collector_id":"C7","subnets":["192.0.2.0/24"],"protocols":["SSH"]}`
	r, srv := mustRun(t, "edit-source", discoveryWriteRoutes(false, &calls), in+`}`)
	b := jsonOf(r)
	if r.Mode != result.ModeDryRun || writes(srv) != 0 || len(calls) != 0 {
		t.Fatalf("dry run wrote: %s %v", r.Mode, calls)
	}
	for _, want := range []string{"192.0.2.0/24", "C7", "scans", "500", "MANAGE_COLLECTION_SOURCES"} {
		if !strings.Contains(b, want) {
			t.Errorf("dry run should show %q: %s", want, b)
		}
	}
	if _, _, err := runSkill(t, "edit-source", discoveryWriteRoutes(false, &calls), in+`,"apply":true}`); err == nil || len(calls) != 0 {
		t.Errorf("apply without confirm: %v %v", err, calls)
	}
	r, _ = mustRun(t, "edit-source", discoveryWriteRoutes(false, &calls), in+`,"apply":true,"confirm":"SUBNET_SCAN"}`)
	if r.Status != result.OK || len(calls) != 1 || !strings.Contains(calls[0], "op=start") || !strings.Contains(calls[0], "collectorId=C7") || !strings.Contains(calls[0], `"type":"SUBNET_SCAN"`) {
		t.Errorf("apply: %s %v", r.Status, calls)
	}
	calls = nil
	if _, _, err := runSkill(t, "edit-source", discoveryWriteRoutes(true, &calls), in+`,"apply":true,"confirm":"SUBNET_SCAN"}`); err == nil || !strings.Contains(err.Error(), "already running") || len(calls) != 0 {
		t.Errorf("a running discovery refuses a second: %v %v", err, calls)
	}
	if _, _, err := runSkill(t, "edit-source", discoveryWriteRoutes(false, &calls), `{"network_id":"n1","object":"discovery","action":"start","definition":{"type":"SUBNET_SCAN"}}`); err == nil {
		t.Errorf("a scan needs subnets or hosts")
	}
}

func TestDiscoveryStopAndAddDiscovered(t *testing.T) {
	var calls []string
	r, _ := mustRun(t, "edit-source", discoveryWriteRoutes(true, &calls), `{"network_id":"n1","object":"discovery","action":"stop"}`)
	if r.Mode != result.ModeDryRun || !strings.Contains(jsonOf(r), "J2") || len(calls) != 0 {
		t.Fatalf("stop dry run names the running job: %s", jsonOf(r))
	}
	add := `{"network_id":"n1","object":"discovery","action":"add_discovered","definition":{"names":["new1"]}`
	r, _ = mustRun(t, "edit-source", discoveryWriteRoutes(false, &calls), add+`}`)
	b := jsonOf(r)
	for _, want := range []string{"new1", "192.0.2.2", "arista_eos_ssh", "C7", "L1", "JS1"} {
		if !strings.Contains(b, want) {
			t.Errorf("dry run should list %q: %s", want, b)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("dry run added: %v", calls)
	}
	r, _ = mustRun(t, "edit-source", discoveryWriteRoutes(false, &calls), add+`,"apply":true,"confirm":"1"}`)
	if r.Status != result.OK || len(calls) != 1 || !strings.Contains(calls[0], `"collectorId":"C7"`) || !strings.Contains(calls[0], `"name":"new1"`) {
		t.Errorf("apply: %s %v", r.Status, calls)
	}
	for name, names := range map[string]string{"unknown type": `["mystery"]`, "no host": `["nohost"]`, "not discovered": `["ghost"]`} {
		if _, _, err := runSkill(t, "edit-source", discoveryWriteRoutes(false, &calls), `{"network_id":"n1","object":"discovery","action":"add_discovered","definition":{"names":`+names+`}}`); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
}

// A scheduled discovery scans again and again from the network's default collector: creating an enabled one is a confirmed, dry-run-first action that says so; deleting needs confirm too.
func TestScheduledDiscoveryCreateAndDeleteAreConfirmed(t *testing.T) {
	var calls []string
	routes := discoveryWriteRoutes(false, &calls)
	routes["POST /api/networks/n1/scheduled-discoveries"] = func(_ *http.Request, b []byte) (int, any) {
		calls = append(calls, "schedule "+string(b))
		return 200, map[string]any{"id": "S1", "name": "nightly", "enabled": true}
	}
	routes["GET /api/networks/n1/scheduled-discoveries"] = fwdtest.Const(200, []any{map[string]any{"id": "S1", "enabled": true, "name": "nightly", "params": map[string]any{"type": "SUBNET_SCAN", "subnets": []string{"192.0.2.0/24"}}}})
	routes["DELETE /api/networks/n1/scheduled-discoveries/S1"] = func(*http.Request, []byte) (int, any) { calls = append(calls, "delete S1"); return 204, nil }
	create := `{"network_id":"n1","object":"discovery","action":"schedule_create","definition":{"name":"nightly","enabled":true,"type":"SUBNET_SCAN","subnets":["192.0.2.0/24"],"schedule":{"days_of_the_week":[1,3],"times":["02:00"]}}`
	r, _ := mustRun(t, "edit-source", routes, create+`}`)
	b := jsonOf(r)
	if r.Mode != result.ModeDryRun || len(calls) != 0 || !strings.Contains(b, "default collector") || !strings.Contains(b, "scans") {
		t.Fatalf("dry run: %s %v %s", r.Mode, calls, b)
	}
	if _, _, err := runSkill(t, "edit-source", routes, create+`,"apply":true}`); err == nil || len(calls) != 0 {
		t.Errorf("apply without confirm: %v %v", err, calls)
	}
	r, _ = mustRun(t, "edit-source", routes, create+`,"apply":true,"confirm":"nightly"}`)
	if r.Status != result.OK || len(calls) != 1 || !strings.Contains(calls[0], `"daysOfTheWeek":[1,3]`) || !strings.Contains(calls[0], `"type":"SUBNET_SCAN"`) {
		t.Errorf("create: %s %v", r.Status, calls)
	}
	calls = nil
	del := `{"network_id":"n1","object":"discovery","action":"schedule_delete","name":"S1","apply":true`
	if _, _, err := runSkill(t, "edit-source", routes, del+`}`); err == nil || len(calls) != 0 {
		t.Errorf("delete without confirm: %v %v", err, calls)
	}
	r, _ = mustRun(t, "edit-source", routes, del+`,"confirm":"S1"}`)
	if len(calls) != 1 || calls[0] != "delete S1" {
		t.Errorf("delete: %s %v", r.Status, calls)
	}
}
