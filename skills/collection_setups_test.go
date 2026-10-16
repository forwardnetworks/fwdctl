package skills_test

import (
	"fmt"
	"github.com/forwardnetworks/fwdctl/skills"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

func setupsRoutes(queued *string) map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/controller-managed-setups": func(r *http.Request, _ []byte) (int, any) {
			return 200, map[string]any{"setups": []any{map[string]any{
				"name": "sdwan", "collectorId": "C7",
				"controllers": []any{map[string]any{"name": "ctl", "host": "192.0.2.10", "testResult": map[string]any{"error": "HOST_NOT_REPORTING_ANY_GUEST", "errorPhase": "SETUP", "startTime": "2026-10-16T09:59:00Z", "endTime": "2026-10-16T10:00:00Z",
					"hostIps": []string{"192.0.2.10"}, "unauthorizedCommands": []string{"show x"}, "discoveredCliCredentialId": "L1", "managedDevices": []any{map[string]any{"name": "edge1", "ipAddress": "192.0.2.21"}}}}},
				"managedDevices": []any{
					map[string]any{"name": "edge1", "cliCredentialId": "L1", "collect": true, "testResult": map[string]any{"error": "AUTHENTICATION_FAILED", "errorPhase": "LOGIN", "endTime": "2026-10-16T10:01:00Z"}},
					map[string]any{"name": "edge2", "cliCredentialId": "L1"},
				}}}}
		},
		"POST /api/networks/n1/controller-managed-setups": func(r *http.Request, b []byte) (int, any) {
			*queued = r.URL.RawQuery + " " + string(b)
			return 204, nil
		},
	}
}

// After a migrate the setup is read back: name, collector, controllers and managed devices with the collect flag and each device's last saved connectivity test. An untested device and a device whose result has
// no end time are not passes.
func TestConfigViewReadsControllerSetupsBackWithTheirTestResults(t *testing.T) {
	var q string
	r, _ := mustRun(t, "inspect-collection-config", setupsRoutes(&q), `{"network_id":"n1"}`)
	var setups []map[string]any
	for _, e := range r.Evidence {
		if v, ok := e.Detail["controller_setups"].([]map[string]any); ok {
			setups = v
		}
	}
	if len(setups) != 1 || setups[0]["name"] != "sdwan" || setups[0]["collector_id"] != "C7" {
		t.Fatalf("setups: %v", setups)
	}
	devs := setups[0]["devices"].([]map[string]any)
	by := map[string]map[string]any{}
	for _, d := range devs {
		by[d["name"].(string)] = d
	}
	if by["edge1"]["role"] != "managed" || by["edge1"]["test"] != "failed: AUTHENTICATION_FAILED (LOGIN)" || by["edge1"]["tested_at"] != "2026-10-16T10:01:00Z" {
		t.Errorf("edge1: %v", by["edge1"])
	}
	if by["edge2"]["test"] != "never tested" || by["ctl"]["role"] != "controller" || by["ctl"]["test"] != "failed: HOST_NOT_REPORTING_ANY_GUEST (SETUP)" {
		t.Errorf("edge2 %v ctl %v", by["edge2"], by["ctl"])
	}
	ctl := by["ctl"]
	if ctl["discovered_cli_credential"] != "L1" || ctl["started_at"] != "2026-10-16T09:59:00Z" || fmt.Sprint(ctl["host_ips"]) != "[192.0.2.10]" || ctl["unauthorized_commands"] == nil {
		t.Errorf("the test's own details are shown: %v", ctl)
	}
	if rep, _ := ctl["managed_devices_reported"].([]map[string]any); len(rep) != 1 || rep[0]["name"] != "edge1" || !strings.Contains(fmt.Sprint(ctl["meaning"]), "no guests") || !strings.Contains(fmt.Sprint(ctl["meaning"]), "not a login failure") {
		t.Errorf("the controller's reported devices and what the error means: %v", ctl)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "last saved") {
		t.Errorf("the limits should say the result is the last saved one, so compare its time with when the test was queued: %v", r.Limits)
	}
}

// controller_setup action test queues Forward's connectivity test of every controller and managed device of a setup. It sends nothing in a dry run, and the result is read with inspect-collection-config.
func TestControllerSetupTestQueuesTheTestsOnlyWhenApplied(t *testing.T) {
	var q string
	in := `{"network_id":"n1","object":"controller_setup","action":"test","name":"sdwan"`
	r, srv := mustRun(t, "edit-source", setupsRoutes(&q), in+`}`)
	if r.Mode != result.ModeDryRun || writes(srv) != 0 || q != "" || !strings.Contains(jsonOf(r), "3") {
		t.Fatalf("dry run: %s %s q=%q", r.Mode, r.Finding, q)
	}
	r, _ = mustRun(t, "edit-source", setupsRoutes(&q), in+`,"apply":true}`)
	if r.Status != result.OK || !strings.HasPrefix(q, "action=test ") || !strings.Contains(q, `"setupNames":["sdwan"]`) {
		t.Errorf("apply: %s %s q=%q", r.Status, r.Finding, q)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "inspect-collection-config") {
		t.Errorf("limits should point to the read-back: %v", r.Limits)
	}
	if r, _, err := runSkill(t, "edit-source", setupsRoutes(&q), `{"network_id":"n1","object":"controller_setup","action":"test","name":"nope","apply":true}`); err == nil && r.Status == result.OK {
		t.Errorf("an unknown setup must not be reported as tested: %s", r.Finding)
	}
}

// With wait_seconds the test action waits for every controller and managed device to have a saved result newer than the moment the tests were queued, and returns those results; a result that is not newer is
// the previous one and is not reported as the outcome.
func TestControllerSetupTestWaitsForResultsNewerThanTheQueueTime(t *testing.T) {
	defer skills.SetupTestPollHook(5 * time.Millisecond)()
	reads := 0
	var queuedAt time.Time
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/controller-managed-setups": func(*http.Request, []byte) (int, any) {
			reads++
			end := "2020-01-01T00:00:00Z" // the previous result
			if reads >= 4 && !queuedAt.IsZero() {
				end = queuedAt.Add(2 * time.Second).UTC().Format(time.RFC3339)
			}
			return 200, map[string]any{"setups": []any{map[string]any{"name": "sdwan", "controllers": []any{map[string]any{"name": "ctl", "testResult": map[string]any{"endTime": end}}},
				"managedDevices": []any{map[string]any{"name": "edge1", "cliCredentialId": "L1", "testResult": map[string]any{"endTime": end}}}}}}
		},
		"POST /api/networks/n1/controller-managed-setups": func(*http.Request, []byte) (int, any) { queuedAt = time.Now(); return 204, nil },
	}
	in := `{"network_id":"n1","object":"controller_setup","action":"test","name":"sdwan","apply":true,"definition":{"wait_seconds":30}}`
	r, _ := mustRun(t, "edit-source", routes, in)
	b := jsonOf(r)
	if r.Status != result.OK || !strings.Contains(b, "no error recorded") || reads < 4 {
		t.Fatalf("it should wait for fresh results: %s %s reads %d", r.Status, b, reads)
	}
	// results that never get newer within the wait are not an outcome
	stale := map[string]fwdtest.Handler{
		"GET /api/networks/n1/controller-managed-setups":  routes["GET /api/networks/n1/controller-managed-setups"],
		"POST /api/networks/n1/controller-managed-setups": func(*http.Request, []byte) (int, any) { reads = -1000; queuedAt = time.Time{}; return 204, nil },
	}
	r2, _ := mustRun(t, "edit-source", stale, strings.Replace(in, `"wait_seconds":30`, `"wait_seconds":1`, 1))
	if r2.Status == result.OK {
		t.Errorf("stale results after the wait must not be reported as done: %s %s", r2.Status, r2.Finding)
	}
}
