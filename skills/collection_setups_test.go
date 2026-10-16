package skills_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

func setupsRoutes(queued *string) map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/controller-managed-setups": func(r *http.Request, _ []byte) (int, any) {
			return 200, map[string]any{"setups": []any{map[string]any{
				"name": "sdwan", "collectorId": "C7",
				"controllers": []any{map[string]any{"name": "ctl", "host": "192.0.2.10", "testResult": map[string]any{"endTime": "2026-10-16T10:00:00Z"}}},
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
	if by["edge2"]["test"] != "never tested" || by["ctl"]["role"] != "controller" || by["ctl"]["test"] != "no error recorded" {
		t.Errorf("edge2 %v ctl %v", by["edge2"], by["ctl"])
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
