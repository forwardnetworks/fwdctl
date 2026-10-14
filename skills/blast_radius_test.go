package skills_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

func blast(t *testing.T, f changeFix) (result.Result, *fwdtest.Server) {
	return mustRun(t, "analyze-blast-radius", f.routes(), csInput+"}")
}

func TestBlastRadiusReportsExtentFromAreasAndConnectivity(t *testing.T) {
	r, _ := blast(t, changeFix{counts: map[string]int{"acl": 4, "interfaces": 9}, conn: conn(false, 10, 3, 1)})
	if r.Status != result.OK || r.Confidence != result.Deterministic {
		t.Fatalf("got %s/%s", r.Status, r.Confidence)
	}
	if !strings.Contains(r.Finding, "interfaces (9)") || !strings.Contains(r.Finding, "3 subnet pair(s) lost connectivity") {
		t.Errorf("finding %q", r.Finding)
	}
	if strings.Index(r.Finding, "interfaces (9)") > strings.Index(r.Finding, "acl (4)") {
		t.Errorf("areas are not ordered by size: %q", r.Finding)
	}
	if r.NextActions[0] != "investigate-reachability" {
		t.Errorf("next %v", r.NextActions)
	}
}

func TestBlastRadiusACompleteZeroEverywhereIsARealAnswer(t *testing.T) {
	r, _ := blast(t, changeFix{})
	if r.Status != result.OK || !strings.Contains(r.Finding, "No observable difference") {
		t.Fatalf("got %s: %s", r.Status, r.Finding)
	}
}

func TestBlastRadiusIncompleteCountsAreNotCounts(t *testing.T) {
	r, _ := blast(t, changeFix{counts: map[string]int{"acl": 5}, incomplete: map[string]bool{"acl": true}})
	if !strings.Contains(strings.Join(r.Limits, " "), "not complete for: acl") {
		t.Errorf("limits %v", r.Limits)
	}
	if strings.Contains(r.Finding, "acl (5)") {
		t.Errorf("an incomplete count was reported as a difference: %q", r.Finding)
	}
}

func TestBlastRadiusNothingMeasuredIsUnknown(t *testing.T) {
	inc := map[string]bool{}
	for _, a := range diffAreas {
		inc[a] = true
	}
	r, _ := blast(t, changeFix{incomplete: inc, conn: conn(true, 0, 0, 0)})
	if r.Status != result.Unknown {
		t.Fatalf("status %s: %s", r.Status, r.Finding)
	}
}

func TestBlastRadiusUnsettledConnectivityIsALimitNotEvidence(t *testing.T) {
	r, _ := blast(t, changeFix{counts: map[string]int{"acl": 2}, conn: conn(true, 0, 0, 0)})
	if r.Status != result.OK || strings.Contains(r.Finding, "lost connectivity") ||
		!strings.Contains(strings.Join(r.Limits, " "), "did not settle") {
		t.Fatalf("got %s %q %v", r.Status, r.Finding, r.Limits)
	}
}

func TestBlastRadiusZeroPairsComparedIsUnmeasuredNotNoChange(t *testing.T) {
	r, _ := blast(t, changeFix{counts: map[string]int{"acl": 2}, conn: conn(false, 0, 0, 0)})
	if !strings.Contains(strings.Join(r.Limits, " "), "no subnet pairs") || strings.Contains(r.Finding, "lost connectivity") {
		t.Fatalf("limits %v finding %q", r.Limits, r.Finding)
	}
}

func TestBlastRadiusNoPredictionIsUnknownAndPredictIsNotRun(t *testing.T) {
	r, srv := blast(t, changeFix{predicted: []map[string]any{}})
	if r.Status != result.Unknown {
		t.Fatalf("status %s", r.Status)
	}
	for _, c := range srv.Calls() {
		if c.Method == "POST" {
			t.Errorf("unexpected %s %s", c.Method, c.Path)
		}
	}
}

func TestBlastRadiusUnavailableAreaIsAnIncompleteAreaNotAZero(t *testing.T) {
	f := changeFix{counts: map[string]int{"acl": 1}}
	routes := f.routes()
	routes["GET /api/diffs/b1/a1/l2"] = fwdtest.Const(404, map[string]any{"message": "not found"})
	r, _ := mustRun(t, "analyze-blast-radius", routes, csInput+"}")
	if !strings.Contains(strings.Join(r.Limits, " "), "l2") {
		t.Errorf("limits %v", r.Limits)
	}
}

func TestBlastRadiusMissingSelectorIsAnErrorResult(t *testing.T) {
	r, _ := mustRun(t, "analyze-blast-radius", map[string]fwdtest.Handler{}, `{"network_id":"n1"}`)
	if r.Status != result.Error {
		t.Fatalf("status %s", r.Status)
	}
}

func TestBlastRadiusAConnectivityFeatureSwitchedOffStillReportsTheAreaCounts(t *testing.T) {
	f := changeFix{counts: map[string]int{"acl": 2}}
	routes := f.routes()
	routes[connPath] = fwdtest.Const(403, map[string]any{"message": "LOCATION_CONNECTIVITY_DIFFS is off for your organization"})
	r, _ := mustRun(t, "analyze-blast-radius", routes, csInput+"}")
	if r.Status != result.OK || !strings.Contains(r.Finding, "acl (2)") || !strings.Contains(strings.Join(r.Limits, " "), "is off") {
		t.Fatalf("got %s %q %v", r.Status, r.Finding, r.Limits)
	}
}

// aclRoutes answers GET /api/diffs/b1/a1/acl with the count (?count) or the changed devices, and a device's changed rules, like Forward.
func aclRoutes(f changeFix, devices []any, rules map[string]any) map[string]fwdtest.Handler {
	r := f.routes()
	count := r["GET /api/diffs/b1/a1/acl"]
	r["GET /api/diffs/b1/a1/acl"] = func(req *http.Request, b []byte) (int, any) {
		if _, counting := req.URL.Query()["count"]; counting {
			return count(req, b)
		}
		return 200, map[string]any{"deviceInfos": devices}
	}
	for dev, body := range rules {
		r["GET /api/diffs/b1/a1/acl/devices/"+dev] = fwdtest.Const(200, body)
	}
	return r
}

// With the ACL area changed, the impact view names the devices whose ACLs changed, the most changed first, with their added, deleted and modified rule counts. It does not
// pull each device's rules: that is asked for with `device`.
func TestBlastRadiusNamesTheDevicesWhoseACLsChangedMostChangedFirst(t *testing.T) {
	devices := []any{
		map[string]any{"deviceName": "r2", "stats": map[string]any{"ADDED": 1, "DELETED": 0, "MODIFIED": 0}},
		map[string]any{"deviceName": "fw01", "stats": map[string]any{"ADDED": 2, "DELETED": 3, "MODIFIED": 4}}}
	r, srv := mustRun(t, "verify-change", aclRoutes(changeFix{counts: map[string]int{"acl": 10}}, devices, nil), csInput+`,"view":"impact"}`)
	var rows []map[string]any
	for _, e := range r.Evidence {
		if v, ok := e.Detail["acl_devices"].([]map[string]any); ok {
			rows = v
		}
	}
	if len(rows) != 2 || rows[0]["device"] != "fw01" || rows[0]["added"] != 2 || rows[0]["deleted"] != 3 || rows[0]["modified"] != 4 || rows[1]["device"] != "r2" {
		t.Fatalf("acl_devices = %v", rows)
	}
	if srv.Called("GET", "/api/diffs/b1/a1/acl/devices/fw01") {
		t.Error("the rules of every device were pulled without asking for one")
	}
}

// With `device` the changed rules are listed: each as added, deleted or modified, with its action, name, where it applies and the config lines it came from. A modified rule
// shows what it was.
func TestBlastRadiusListsADevicesChangedACLRulesWithActionAndConfigLines(t *testing.T) {
	rules := map[string]any{"fw01": map[string]any{"stats": map[string]any{"ADDED": 1, "DELETED": 1, "MODIFIED": 1}, "diffs": []any{
		map[string]any{"diffType": "ADDED", "b": map[string]any{"name": "outside-in", "action": "PERMIT", "aclContext": "INPUT", "inputInterfaces": []string{"e1"},
			"matches": []any{map[string]any{"field": "TP_DST", "values": []string{"443"}}}, "fileLines": map[string]any{"configuration.txt": []any{map[string]any{"start": 10, "end": 12}}}}},
		map[string]any{"diffType": "DELETED", "a": map[string]any{"name": "outside-in", "action": "DENY", "aclContext": "INPUT"}},
		map[string]any{"diffType": "MODIFIED", "a": map[string]any{"name": "mgmt", "action": "DENY"}, "b": map[string]any{"name": "mgmt", "action": "PERMIT"}}}}}
	r, _ := mustRun(t, "verify-change", aclRoutes(changeFix{counts: map[string]int{"acl": 3}}, []any{map[string]any{"deviceName": "fw01"}}, rules), csInput+`,"view":"impact","device":"fw01"}`)
	var detail map[string]any
	for _, e := range r.Evidence {
		if e.Detail["acl_changes"] != nil {
			detail = e.Detail
		}
	}
	if detail == nil {
		t.Fatalf("no acl_changes evidence: %+v", r.Evidence)
	}
	entries, _ := detail["acl_changes"].([]map[string]any)
	if len(entries) != 3 || entries[0]["change"] != "ADDED" || entries[0]["action"] != "PERMIT" || entries[0]["name"] != "outside-in" || entries[1]["change"] != "DELETED" || entries[1]["action"] != "DENY" {
		t.Fatalf("entries = %v", entries)
	}
	if lines, _ := entries[0]["file_lines"].(map[string]any); lines["configuration.txt"] != "10-12" {
		t.Errorf("config lines = %v", entries[0]["file_lines"])
	}
	if was, _ := entries[2]["was"].(map[string]any); entries[2]["change"] != "MODIFIED" || entries[2]["action"] != "PERMIT" || was["action"] != "DENY" {
		t.Errorf("a modified rule shows what it was: %v", entries[2])
	}
}

// A device the diff does not list is not evidence that it has no ACLs or does not exist: zero changed rules says so in the limits, and a device given while the acl area shows no
// difference says it was not looked up. A diff that carries neither side is skipped, not a crash.
func TestBlastRadiusADeviceWithNoChangedRulesIsALimitNotAFact(t *testing.T) {
	rules := map[string]any{"fw0": map[string]any{"stats": map[string]any{}, "diffs": []any{map[string]any{"diffType": "ADDED"}}}}
	r, _ := mustRun(t, "verify-change", aclRoutes(changeFix{counts: map[string]int{"acl": 3}}, []any{}, rules), csInput+`,"view":"impact","device":"fw0"}`)
	if !strings.Contains(strings.Join(r.Limits, "|"), "does not show the device exists") {
		t.Errorf("limits do not caveat an empty rule list: %v", r.Limits)
	}
	r, _ = mustRun(t, "verify-change", changeFix{}.routes(), csInput+`,"view":"impact","device":"fw01"}`)
	if !strings.Contains(strings.Join(r.Limits, "|"), "no ACL rules were listed for fw01") {
		t.Errorf("limits do not say the device was not looked up: %v", r.Limits)
	}
}
