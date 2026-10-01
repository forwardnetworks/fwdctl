package skills_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

const (
	vulnList = "GET /api/networks/n1/vulnerabilities"
	vulnGet  = "GET /api/networks/n1/vulnerabilities/CVE-2024-0001"
)

func osInfo(sev string, counts map[string]int, kev bool) map[string]any {
	var rc []map[string]any
	total := 0
	for r, n := range counts {
		rc = append(rc, map[string]any{"result": r, "deviceCount": n})
		total += n
	}
	return map[string]any{"vendor": "JUNIPER", "os": "JUNOS", "severity": sev, "v3Score": 8.1, "advisoryMentionsExploit": kev,
		"deviceCount": total, "resultCounts": rc}
}

func cve(id, sev string, counts map[string]int, kev bool) map[string]any {
	return map[string]any{"id": id, "description": "desc of " + id, "hasCisaKevEntry": kev, "osInfos": []map[string]any{osInfo(sev, counts, kev)}}
}

func vulnRoutes(list []map[string]any) map[string]fwdtest.Handler {
	if list == nil {
		list = []map[string]any{}
	}
	return map[string]fwdtest.Handler{
		snapsPath: ready("s1"),
		vulnList:  fwdtest.Const(200, map[string]any{"vulnerabilities": list, "indexCreatedAt": "2026-09-20T00:00:00Z"}),
	}
}

func vuln(t *testing.T, routes map[string]fwdtest.Handler, in string) (result.Result, *fwdtest.Server) {
	return mustRun(t, "inspect-vulnerabilities", routes, in)
}

const netIn = `{"network_id":"n1"`

func TestVulnerabilitiesExposedDevicesFailAndTheWorstCVEIsNamedFirst(t *testing.T) {
	r, _ := vuln(t, vulnRoutes([]map[string]any{
		cve("CVE-2024-0002", "MEDIUM", map[string]int{"VULNERABLE": 9}, false),
		cve("CVE-2024-0001", "CRITICAL", map[string]int{"OS_VULNERABLE": 2, "NOT_VULNERABLE": 5}, true),
	}), netIn+"}")
	if r.Status != result.Failed || r.Confidence != result.Deterministic {
		t.Fatalf("got %s/%s", r.Status, r.Confidence)
	}
	if !strings.Contains(r.Finding, "worst: CVE-2024-0001 (CRITICAL, 2 devices)") {
		t.Errorf("finding %q", r.Finding)
	}
	if r.Evidence[0].Detail["cve"] != "CVE-2024-0001" || r.Evidence[0].Detail["known_exploit"] != true {
		t.Errorf("first evidence %v", r.Evidence[0].Detail)
	}
}

func TestVulnerabilitiesOnlyUnsettledResultsAreUnknownNeverAPass(t *testing.T) {
	r, _ := vuln(t, vulnRoutes([]map[string]any{cve("CVE-2024-0001", "HIGH", map[string]int{"UNCONFIRMED": 3, "UNIMPLEMENTED": 1}, false)}), netIn+"}")
	if r.Status != result.Unknown || !strings.Contains(strings.Join(r.Limits, " "), "not a pass") {
		t.Fatalf("got %s %v", r.Status, r.Limits)
	}
}

func TestVulnerabilitiesAllNotVulnerableIsOK(t *testing.T) {
	r, _ := vuln(t, vulnRoutes([]map[string]any{cve("CVE-2024-0001", "HIGH", map[string]int{"NOT_VULNERABLE": 8}, false)}), netIn+"}")
	if r.Status != result.OK {
		t.Fatalf("status %s: %s", r.Status, r.Finding)
	}
}

func TestVulnerabilitiesAnEmptyListIsUnknownNeverClean(t *testing.T) {
	r, _ := vuln(t, vulnRoutes(nil), netIn+"}")
	if r.Status != result.Unknown || !strings.Contains(strings.Join(r.Limits, " "), "missing CVE index") {
		t.Fatalf("got %s %v", r.Status, r.Limits)
	}
}

func TestVulnerabilitiesFiltersNarrowTheAnswer(t *testing.T) {
	list := []map[string]any{
		cve("CVE-2024-0001", "LOW", map[string]int{"VULNERABLE": 4}, false),
		cve("CVE-2024-0002", "CRITICAL", map[string]int{"VULNERABLE": 1}, true),
	}
	r, _ := vuln(t, vulnRoutes(list), netIn+`,"min_severity":"CRITICAL"}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "1 CVE(s) expose") || strings.Contains(r.Finding, "0001") {
		t.Fatalf("min_severity: %s", r.Finding)
	}
	r, _ = vuln(t, vulnRoutes(list[:1]), netIn+`,"known_exploited_only":true}`)
	if r.Status != result.Unknown || !strings.Contains(strings.Join(r.Limits, " "), "none met the filters") {
		t.Fatalf("kev filter matched nothing but got %s %v", r.Status, r.Limits)
	}
}

func TestVulnerabilitiesLimitBoundsWhatIsShownAndSaysSo(t *testing.T) {
	var list []map[string]any
	for i := 0; i < 6; i++ {
		list = append(list, cve("CVE-2024-000"+string(rune('1'+i)), "HIGH", map[string]int{"VULNERABLE": 1}, false))
	}
	r, _ := vuln(t, vulnRoutes(list), netIn+`,"limit":3}`)
	if len(r.Evidence) != 3 || !strings.Contains(strings.Join(r.Limits, " "), "6 CVEs match; the 3 worst are shown") {
		t.Fatalf("evidence %d limits %v", len(r.Evidence), r.Limits)
	}
}

func TestVulnerabilitiesUnprocessedSnapshotAsksForNothing(t *testing.T) {
	routes := vulnRoutes(nil)
	routes[snapsPath] = fwdtest.Snapshots(fwdtest.Snap("s1", "UNPROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z"))
	r, srv := vuln(t, routes, netIn+"}")
	if r.Status != result.Unknown || srv.Called("GET", "/api/networks/n1/vulnerabilities") {
		t.Fatalf("status %s", r.Status)
	}
}

func gotCVE(devs ...map[string]any) fwdtest.Handler {
	return fwdtest.Const(200, map[string]any{"id": "CVE-2024-0001", "hasCisaKevEntry": true, "osInfos": []map[string]any{{
		"vendor": "JUNIPER", "os": "JUNOS", "severity": "CRITICAL", "v3Score": 9.8, "description": "advisory text", "devices": devs}}})
}

func dev(name, result, status string) map[string]any {
	return map[string]any{"name": name, "osVersion": "23.4", "result": result, "status": status, "internetAddressable": true,
		"fileRanges": map[string]any{"config": []map[string]any{{"start": 1, "end": 3}}}}
}

func cveIn(extra string) string { return netIn + `,"cve_id":"CVE-2024-0001"` + extra + "}" }

func TestVulnerabilitiesOneCVEListsExposedDevicesFirstWithTheirVerdicts(t *testing.T) {
	routes := vulnRoutes(nil)
	routes[vulnGet] = gotCVE(dev("r-unsettled", "UNCONFIRMED", "POTENTIALLY_VULNERABLE"), dev("r-hit", "VULNERABLE", "VULNERABLE"), dev("r-ok", "NOT_VULNERABLE", "NOT_VULNERABLE"))
	r, _ := vuln(t, routes, cveIn(""))
	if r.Status != result.Failed || !strings.Contains(r.Finding, "exposes 1 device(s)") {
		t.Fatalf("got %s: %s", r.Status, r.Finding)
	}
	rows := r.Evidence[0].Detail["devices"].([]map[string]any)
	if rows[0]["device"] != "r-hit" || rows[0]["config_evidence"] != true || len(rows) != 3 {
		t.Errorf("rows %v", rows)
	}
}

func TestVulnerabilitiesOSVulnerableCountsAsExposedButUnimplementedDoesNot(t *testing.T) {
	routes := vulnRoutes(nil)
	routes[vulnGet] = gotCVE(dev("a", "OS_VULNERABLE", "VULNERABLE"))
	if r, _ := vuln(t, routes, cveIn("")); r.Status != result.Failed {
		t.Errorf("OS_VULNERABLE -> %s", r.Status)
	}
	routes[vulnGet] = gotCVE(dev("a", "UNIMPLEMENTED", "POTENTIALLY_VULNERABLE"))
	if r, _ := vuln(t, routes, cveIn("")); r.Status != result.Unknown {
		t.Errorf("UNIMPLEMENTED -> %s", r.Status)
	}
}

func TestVulnerabilitiesACVEForwardHasNoRecordOfIsUnknownNotClean(t *testing.T) {
	routes := vulnRoutes(nil)
	routes[vulnGet] = fwdtest.Const(404, map[string]any{"message": "not found"})
	r, _ := vuln(t, routes, cveIn(""))
	if r.Status != result.Unknown {
		t.Fatalf("status %s", r.Status)
	}
}

func TestVulnerabilitiesCVEAndDeviceTogetherIsAnError(t *testing.T) {
	r, _ := vuln(t, vulnRoutes(nil), netIn+`,"cve_id":"CVE-2024-0001","device":"r1"}`)
	if r.Status != result.Error {
		t.Fatalf("status %s", r.Status)
	}
}

func TestVulnerabilitiesBadCVEIdIsRejectedBeforeAnyCall(t *testing.T) {
	_, srv, err := runSkill(t, "inspect-vulnerabilities", vulnRoutes(nil), netIn+`,"cve_id":"not-a-cve"}`)
	if err == nil || len(srv.Calls()) != 0 {
		t.Fatalf("err %v calls %d", err, len(srv.Calls()))
	}
}

// nqeDeviceRoutes answers the summary query (its text has "Findings:") and the detail query separately.
func nqeDeviceRoutes(summary []map[string]any, detail []map[string]any) map[string]fwdtest.Handler {
	routes := vulnRoutes(nil)
	routes[nqePath] = func(_ *http.Request, b []byte) (int, any) {
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		if q, _ := body["query"].(string); strings.Contains(q, "Findings:") {
			return 200, map[string]any{"items": summary, "totalNumItems": len(summary)}
		}
		return 200, map[string]any{"items": detail, "totalNumItems": len(detail)}
	}
	return routes
}

const devIn = `{"network_id":"n1","device":"atl-core-pe01"`

func drow(cve, sev string, kev bool) map[string]any {
	return map[string]any{"CVE": cve, "Basis": "PLATFORM", "Severity": sev, "KnownExploit": kev, "Description": "d"}
}

func TestVulnerabilitiesADeviceWithFindingsIsFailedWithTheWorstFirst(t *testing.T) {
	routes := nqeDeviceRoutes([]map[string]any{{"Device": "atl-core-pe01", "Vendor": "JUNIPER", "OS": "JUNOS", "Findings": 80, "Vulnerable": 3}},
		[]map[string]any{drow("CVE-1", "LOW", false), drow("CVE-2", "CRITICAL", false), drow("CVE-3", "CRITICAL", true)})
	r, srv := vuln(t, routes, devIn+"}")
	if r.Status != result.Failed || !strings.Contains(r.Finding, "worst: CVE-3 (CRITICAL), CVE-2 (CRITICAL), CVE-1 (LOW)") {
		t.Fatalf("got %s: %s", r.Status, r.Finding)
	}
	for _, c := range srv.Calls() {
		if c.Path == "/api/nqe" {
			p, _ := c.Body["parameters"].(map[string]any)
			if p["deviceName"] != "atl-core-pe01" {
				t.Errorf("device sent as %v, want a query parameter", c.Body["parameters"])
			}
			if q, _ := c.Body["query"].(string); strings.Contains(q, "atl-core-pe01") {
				t.Errorf("the device name was spliced into the query text")
			}
		}
	}
}

func TestVulnerabilitiesADeviceNotInTheSnapshotIsUnknown(t *testing.T) {
	r, _ := vuln(t, nqeDeviceRoutes(nil, nil), devIn+"}")
	if r.Status != result.Unknown || !strings.Contains(r.Finding, "No device named") {
		t.Fatalf("got %s: %s", r.Status, r.Finding)
	}
}

func TestVulnerabilitiesADeviceWithNoFindingsAtAllIsUnknownNotClean(t *testing.T) {
	r, _ := vuln(t, nqeDeviceRoutes([]map[string]any{{"Device": "x", "Findings": 0, "Vulnerable": 0}}, nil), devIn+"}")
	if r.Status != result.Unknown || !strings.Contains(strings.Join(r.Limits, " "), "no CVE analysis") {
		t.Fatalf("got %s %v", r.Status, r.Limits)
	}
}

func TestVulnerabilitiesAssessedDeviceWithNothingVulnerableIsOK(t *testing.T) {
	r, _ := vuln(t, nqeDeviceRoutes([]map[string]any{{"Device": "x", "Findings": 12, "Vulnerable": 0}}, nil), devIn+"}")
	if r.Status != result.OK {
		t.Fatalf("got %s: %s", r.Status, r.Finding)
	}
}

func TestVulnerabilitiesFiltersThatHideEveryDeviceFindingAreUnknownNotClean(t *testing.T) {
	routes := nqeDeviceRoutes([]map[string]any{{"Device": "x", "Findings": 5, "Vulnerable": 1}}, []map[string]any{drow("CVE-1", "LOW", false)})
	r, _ := vuln(t, routes, devIn+`,"min_severity":"CRITICAL"}`)
	if r.Status != result.Unknown || !strings.Contains(strings.Join(r.Limits, " "), "not clean") {
		t.Fatalf("got %s %v", r.Status, r.Limits)
	}
}
