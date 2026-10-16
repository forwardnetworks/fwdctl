package skills_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

// view connectivity counts the last connectivity-test result of every classic device by error and phase, with the devices (capped) and their types per error. A failed test is not a failed collection, so errors that never
// reach the snapshot (for example an authorization failure of a custom command) are listed here.
func connectivityRoutes() map[string]fwdtest.Handler {
	dev := func(name, typ, errName, phase string) map[string]any {
		tr := map[string]any{"discoveredType": typ}
		if errName != "" {
			tr["error"], tr["errorPhase"] = errName, phase
		}
		if errName != "" {
			tr["savedAt"] = "2026-10-1" + name[len(name)-1:] + "T00:00:00Z"
		}
		return map[string]any{"name": name, "host": "h", "type": typ, "testResult": tr}
	}
	devs := []any{dev("ok1", "arista_eos_ssh", "", ""), dev("ok2", "arista_eos_ssh", "", ""),
		dev("p1", "cisco_ios_xe_ssh", "PING_FAILED", "CONNECTION"), dev("p2", "cisco_ios_xe_ssh", "PING_FAILED", "CONNECTION"), dev("p3", "juniper_junos_ssh", "PING_FAILED", "CONNECTION"),
		dev("a1", "cisco_ios_xe_ssh", "CUSTOM_COMMAND_AUTHORIZATION_FAILED", "AUTHORIZATION"),
		map[string]any{"name": "never", "host": "h"}}
	return map[string]fwdtest.Handler{"GET /api/networks/n1/classic-devices": fwdtest.Const(200, devs)}
}

func TestConnectivityViewCountsTheLastTestResultsByErrorAndPhase(t *testing.T) {
	r, srv := mustRun(t, "inspect-collection", connectivityRoutes(), `{"network_id":"n1","view":"connectivity","limit":2}`)
	if writes(srv) != 0 {
		t.Errorf("a read must not write")
	}
	var d map[string]any
	for _, e := range r.Evidence {
		if v, ok := e.Detail["connectivity"].(map[string]any); ok {
			d = v
		}
	}
	if d == nil {
		t.Fatalf("no detail: %s", jsonOf(r))
	}
	if fmt.Sprint(d["devices_read"]) != "7" || fmt.Sprint(d["tested"]) != "6" || fmt.Sprint(d["no_error"]) != "2" || fmt.Sprint(d["never_tested"]) != "1" {
		t.Errorf("counts: %v", d)
	}
	groups := d["by_error"].([]map[string]any)
	if len(groups) != 2 || groups[0]["error"] != "PING_FAILED" || fmt.Sprint(groups[0]["count"]) != "3" || groups[0]["phase"] != "CONNECTION" {
		t.Fatalf("the largest error group first: %v", groups)
	}
	names := groups[0]["devices"].([]string)
	if len(names) != 2 || fmt.Sprint(groups[0]["devices_total"]) != "3" {
		t.Errorf("devices are capped by limit and carry their total: %v", groups[0])
	}
	if ty := groups[0]["by_type"].(map[string]int); ty["cisco_ios_xe_ssh"] != 2 || ty["juniper_junos_ssh"] != 1 {
		t.Errorf("by_type: %v", ty)
	}
	if groups[0]["tested_from"] != "2026-10-11T00:00:00Z" || groups[0]["tested_to"] != "2026-10-13T00:00:00Z" {
		t.Errorf("each group carries the range of its tests' times: %v", groups[0])
	}
	if groups[1]["error"] != "CUSTOM_COMMAND_AUTHORIZATION_FAILED" {
		t.Errorf("second group: %v", groups[1])
	}
	if r.Status != result.Failed || !strings.Contains(strings.Join(r.Limits, " "), "not a failed collection") {
		t.Errorf("%s %v", r.Status, r.Limits)
	}
}
