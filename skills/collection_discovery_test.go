package skills_test

import (
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

// view discovery reads Forward's device discovery: the jobs (running or finished, with counts), the devices they found that are not yet collection devices, grouped by type and vendor with why each was not added,
// and the scheduled discoveries. It scans nothing.
func discoveryRoutes() map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/discovered-devices": fwdtest.Const(200, map[string]any{
			"jobs": []any{
				map[string]any{"id": "J1", "type": "SUBNET_SCAN", "collectorId": "C7", "startedAt": "2026-10-16T01:00:00Z", "counts": map[string]any{"queued": 5, "discovered": 3, "inaccessible": 1}, "error": "NONE"},
				map[string]any{"id": "J2", "type": "SEEDED", "collectorId": "C7", "startedAt": "2026-10-16T02:00:00Z", "endedAt": "2026-10-16T02:10:00Z", "counts": map[string]any{"queued": 0, "discovered": 1, "inaccessible": 0}},
			},
			"devices": []any{
				map[string]any{"name": "known1", "host": "192.0.2.1", "type": "cisco_ios_xe_ssh", "vendor": "cisco", "cliCredentialId": "L1", "jobId": "J1"},
				map[string]any{"name": "new1", "host": "192.0.2.2", "type": "arista_eos_ssh", "vendor": "arista", "cliCredentialId": "L1", "jobId": "J1"},
				map[string]any{"name": "new2", "host": "192.0.2.3", "type": "unknown", "jobId": "J1"},
				map[string]any{"name": "new3", "type": "juniper_junos_ssh", "vendor": "juniper", "cliCredentialId": "L1", "jobId": "J2"},
			}}),
		"GET /api/networks/n1/classic-devices": fwdtest.Const(200, []any{map[string]any{"name": "known1", "host": "192.0.2.1"}}),
		"GET /api/networks/n1/scheduled-discoveries": fwdtest.Const(200, []any{
			map[string]any{"id": "S1", "enabled": true, "name": "nightly", "params": map[string]any{"type": "SUBNET_SCAN", "subnets": []string{"192.0.2.0/24"}}, "schedule": map[string]any{"daysOfTheWeek": []int{1, 3}, "times": []string{"02:00"}}},
		}),
	}
}

func TestDiscoveryViewListsJobsDevicesNotYetAddedAndSchedules(t *testing.T) {
	r, srv := mustRun(t, "inspect-collection", discoveryRoutes(), `{"network_id":"n1","view":"discovery"}`)
	if writes(srv) != 0 {
		t.Errorf("a read must not write")
	}
	var d map[string]any
	for _, e := range r.Evidence {
		if v, ok := e.Detail["discovery"].(map[string]any); ok {
			d = v
		}
	}
	if d == nil {
		t.Fatalf("no discovery detail: %s", jsonOf(r))
	}
	jobs := d["jobs"].([]map[string]any)
	if len(jobs) != 2 || jobs[0]["status"] != "running" || jobs[1]["status"] != "finished" {
		t.Errorf("jobs: %v", jobs)
	}
	if d["devices_found"] != 4 || d["not_yet_added"] != 3 {
		t.Errorf("one of four devices is already a collection device: %v", d)
	}
	rows := d["not_added"].([]map[string]any)
	why := map[string]string{}
	for _, x := range rows {
		why[x["name"].(string)] = strings.Join(x["why_not_added"].([]string), ",")
	}
	if why["new1"] != "found, not added" || !strings.Contains(why["new2"], "type unknown") || !strings.Contains(why["new2"], "no credential") || !strings.Contains(why["new3"], "no host") {
		t.Errorf("reasons: %v", why)
	}
	if by := d["by_type"].(map[string]int); by["arista_eos_ssh"] != 1 || by["unknown"] != 1 {
		t.Errorf("by_type: %v", by)
	}
	sch := d["schedules"].([]map[string]any)
	if len(sch) != 1 || sch[0]["name"] != "nightly" || sch[0]["enabled"] != true || sch[0]["type"] != "SUBNET_SCAN" {
		t.Errorf("schedules: %v", sch)
	}
	if r.Status != result.OK || !strings.Contains(r.Finding, "3 discovered device") {
		t.Errorf("%s %s", r.Status, r.Finding)
	}
}
