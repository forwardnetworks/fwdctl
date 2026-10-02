package skills_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

func cfRoutes(state string, metrics map[string]any, exceptions fwdtest.Handler) map[string]fwdtest.Handler {
	if metrics == nil {
		metrics = map[string]any{"numSuccessfulDevices": 12}
	}
	if exceptions == nil {
		exceptions = fwdtest.Const(200, map[string]any{"exceptions": []any{}})
	}
	return map[string]fwdtest.Handler{
		snapsPath:                              fwdtest.Snapshots(fwdtest.Snap("s1", state, "COLLECTION", "2026-09-01T00:00:00.000Z")),
		"GET /api/snapshots/s1/metrics":        fwdtest.Const(200, metrics),
		"GET /api/snapshots/s1/exceptions":     exceptions,
		"GET /api/networks/n1/missing-devices": fwdtest.Const(200, map[string]any{"devices": []any{}}),
	}
}

func collect(t *testing.T, routes map[string]fwdtest.Handler, in string) (result.Result, *fwdtest.Server) {
	if in == "" {
		in = `{"network_id":"n1"}`
	}
	return mustRun(t, "investigate-collection-failure", routes, in)
}

func TestCollectionHealthyIsOK(t *testing.T) {
	r, _ := collect(t, cfRoutes("PROCESSED", nil, nil), "")
	if r.Status != result.OK || r.Confidence != result.Deterministic || !strings.Contains(r.Finding, "12 devices") {
		t.Fatalf("got %s: %s", r.Status, r.Finding)
	}
}

func TestCollectionFailuresAreGroupedByCategoryAndCounted(t *testing.T) {
	m := map[string]any{"numSuccessfulDevices": 9, "deviceCollectionFailures": map[string]int{"AUTHENTICATION_FAILED": 3, "PRIV_PASSWORD_ERROR": 1, "CONNECTION_TIMEOUT": 2}}
	r, _ := collect(t, cfRoutes("PROCESSED", m, nil), "")
	if r.Status != result.Failed {
		t.Fatalf("status %s", r.Status)
	}
	got := map[string]float64{}
	for _, e := range r.Evidence {
		if c, ok := e.Detail["category"].(string); ok {
			switch d := e.Detail["devices"].(type) {
			case int:
				got[c] = float64(d)
			case float64:
				got[c] = d
			}
		}
	}
	if got["credentials"] != 4 || got["network_path"] != 2 {
		t.Errorf("categories %v", got)
	}
	if !strings.HasPrefix(r.Finding, "4 device collection failure(s): credentials") {
		t.Errorf("finding %q (largest category first)", r.Finding)
	}
}

func TestCollectionProcessingFailuresAreASeparateCategory(t *testing.T) {
	m := map[string]any{"numSuccessfulDevices": 9, "deviceProcessingFailures": map[string]int{"PARSE_ERROR": 2}}
	r, _ := collect(t, cfRoutes("PROCESSED", m, nil), "")
	if r.Status != result.Failed || r.Evidence[0].Detail["category"] != "processing" {
		t.Fatalf("got %s %v", r.Status, r.Evidence[0].Detail)
	}
}

func TestCollectionInProgressSnapshotIsUnknownAndMetricsAreNotRead(t *testing.T) {
	r, srv := collect(t, cfRoutes("PROCESSING", nil, nil), "")
	if r.Status != result.Unknown || srv.Called("GET", "/api/snapshots/s1/metrics") {
		t.Fatalf("status %s", r.Status)
	}
}

func TestCollectionFailedSnapshotIsAFailure(t *testing.T) {
	r, _ := collect(t, cfRoutes("FAILED", nil, nil), "")
	if r.Status != result.Failed || !strings.Contains(r.Finding, "FAILED") {
		t.Fatalf("got %s: %s", r.Status, r.Finding)
	}
}

func TestCollectionZeroDevicesAndZeroFailuresIsUnknownNeverOK(t *testing.T) {
	r, _ := collect(t, cfRoutes("PROCESSED", map[string]any{}, nil), "")
	if r.Status != result.Unknown || !strings.Contains(strings.Join(r.Limits, " "), "nothing was measured") {
		t.Fatalf("got %s %v", r.Status, r.Limits)
	}
}

func TestCollectionNoSnapshotsIsUnknown(t *testing.T) {
	r, _ := collect(t, map[string]fwdtest.Handler{snapsPath: fwdtest.Snapshots()}, "")
	if r.Status != result.Unknown {
		t.Fatalf("status %s", r.Status)
	}
}

func TestCollectionNewestSnapshotIsChosenInAnyStateButNeverAPrediction(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		snapsPath: fwdtest.Snapshots(
			fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z"),
			fwdtest.Snap("s2", "FAILED", "COLLECTION", "2026-09-02T00:00:00.000Z"),
			fwdtest.Snap("p1", "PROCESSED", "PREDICT", "2026-09-05T00:00:00.000Z")),
		"GET /api/snapshots/s2/metrics":        fwdtest.Const(200, map[string]any{"numSuccessfulDevices": 0}),
		"GET /api/snapshots/s2/exceptions":     fwdtest.Const(200, map[string]any{"exceptions": []any{}}),
		"GET /api/networks/n1/missing-devices": fwdtest.Const(200, map[string]any{"devices": []any{}}),
	}
	r, _ := collect(t, routes, "")
	if *r.Context.SnapshotID != "s2" || r.Status != result.Failed {
		t.Fatalf("read snapshot %s status %s", *r.Context.SnapshotID, r.Status)
	}
}

func TestCollectionRunningTaskIsUnknownAndFailedTaskIsAFailure(t *testing.T) {
	for status, want := range map[string]result.Status{"RUNNING": result.Unknown, "TIMED_OUT": result.Failed} {
		routes := cfRoutes("PROCESSED", nil, nil)
		routes["GET /api/collector-tasks/t1"] = fwdtest.Const(200, map[string]any{"id": "t1", "status": status, "type": "COLLECT"})
		r, _ := collect(t, routes, `{"network_id":"n1","collector_task_id":"t1"}`)
		if r.Status != want {
			t.Errorf("%s: status %s", status, r.Status)
		}
	}
}

func TestCollectionDeniedExceptionsAreNotReadNeverNone(t *testing.T) {
	m := map[string]any{"numSuccessfulDevices": 9, "deviceProcessingFailures": map[string]int{"PARSE_ERROR": 1}}
	r, _ := collect(t, cfRoutes("PROCESSED", m, fwdtest.Const(403, map[string]any{"message": "no DEBUG_SNAPSHOTS"})), "")
	if !strings.Contains(strings.Join(r.Limits, " "), "not read") {
		t.Errorf("limits %v", r.Limits)
	}
}

func TestCollectionReadableExceptionsAreCited(t *testing.T) {
	m := map[string]any{"numSuccessfulDevices": 9, "deviceProcessingFailures": map[string]int{"PARSE_ERROR": 1}}
	exc := fwdtest.Const(200, map[string]any{"exceptions": []map[string]any{{"exceptionType": "PARSING", "occurrences": 2, "stackTrace": "x", "devices": []string{"r1"}}}})
	r, _ := collect(t, cfRoutes("PROCESSED", m, exc), "")
	found := false
	for _, e := range r.Evidence {
		if strings.HasPrefix(e.Summary, "PARSING") {
			found = true
		}
	}
	if !found {
		t.Errorf("exception not cited: %+v", r.Evidence)
	}
}

func TestCollectionMissingNeighboursAreALimitAndEvidenceNotAFailure(t *testing.T) {
	r := cfRoutes("PROCESSED", nil, nil)
	r["GET /api/networks/n1/missing-devices"] = fwdtest.Const(200, map[string]any{"devices": []map[string]any{{"name": "edge9", "vendor": "cisco", "neighbors": []string{"r1"}}}})
	res, _ := collect(t, r, "")
	if res.Status != result.OK || !strings.Contains(strings.Join(res.Limits, " "), "seen but not modeled") {
		t.Fatalf("got %s %v", res.Status, res.Limits)
	}
	found := false
	for _, e := range res.Evidence {
		if e.Source.Operation == "getMissingDevices" {
			found = true
		}
	}
	if !found {
		t.Error("missing neighbours not cited")
	}
}

// failedRoutes adds the NQE the devices and neighbors views read: three failed devices (synthetic names) and two BGP sessions.
func failedRoutes(metrics map[string]any, exceptions fwdtest.Handler, missing []any) map[string]fwdtest.Handler {
	routes := cfRoutes("PROCESSED", metrics, exceptions)
	routes["GET /api/networks/n1/missing-devices"] = fwdtest.Const(200, map[string]any{"devices": missing})
	routes[nqePath] = func(_ *http.Request, body []byte) (int, any) {
		q := string(body)
		rows := func(r ...map[string]any) (int, any) { return 200, map[string]any{"items": r, "totalNumItems": len(r)} }
		switch {
		case strings.Contains(q, "collectionFailed(e)"):
			return rows(
				map[string]any{"device": "edge1", "stage": "collection", "reason": "DeviceCollectionError.CONNECTION_REFUSED", "vendor": "CISCO"},
				map[string]any{"device": "edge2", "stage": "collection", "reason": "DeviceCollectionError.AUTHENTICATION_FAILED"},
				map[string]any{"device": "core1", "stage": "processing", "reason": "DeviceProcessingError.PARSER_EXCEPTION", "vendor": "JUNIPER", "os": "JUNOS", "osVersion": "21.4"})
		case strings.Contains(q, "protocol.bgp.neighbors"):
			return rows(map[string]any{"device": "edge1", "vrf": "default", "peer": "192.0.2.9", "peerAS": 64500, "state": "ESTABLISHED"})
		}
		return 400, map[string]any{"message": "unexpected query: " + q}
	}
	return routes
}

func TestCollectionDevicesViewNamesTheFailedDevicesAndFilters(t *testing.T) {
	exc := fwdtest.Const(200, map[string]any{"exceptions": []any{map[string]any{"exceptionType": "PARSER_EXCEPTION", "occurrences": 1, "devices": []string{"core1"},
		"stackTrace": "com.example.ParseError: unexpected token at line 7\n\tat frame1\n\tat frame2"}}})
	metrics := map[string]any{"numSuccessfulDevices": 9, "deviceCollectionFailures": map[string]any{"CONNECTION_REFUSED": 1, "AUTHENTICATION_FAILED": 1}, "deviceProcessingFailures": map[string]any{"PARSER_EXCEPTION": 1}}
	r, _ := collect(t, failedRoutes(metrics, exc, nil), `{"network_id":"n1","view":"devices"}`)
	d := r.Evidence[0].Detail
	devs := d["devices"].([]map[string]any)
	if r.Status != result.Failed || len(devs) != 3 || d["total_failed"] != 3 {
		t.Fatalf("%s %s %v", r.Status, r.Finding, d)
	}
	byDev := map[string]map[string]any{}
	for _, x := range devs {
		byDev[x["device"].(string)] = x
	}
	if byDev["edge1"]["category"] != "network_path" || byDev["edge2"]["category"] != "credentials" || byDev["core1"]["category"] != "processing" || byDev["core1"]["vendor"] != "JUNIPER" {
		t.Errorf("rows: %v", byDev)
	}
	ex := d["exceptions"].([]map[string]any)
	if len(ex) != 1 || ex[0]["message"] != "com.example.ParseError: unexpected token at line 7" {
		t.Errorf("the parser message is the first line of the trace, without frames: %v", ex)
	}
	r, _ = collect(t, failedRoutes(metrics, exc, nil), `{"network_id":"n1","view":"devices","failure":"credentials"}`)
	if got := r.Evidence[0].Detail["devices"].([]map[string]any); len(got) != 1 || got[0]["device"] != "edge2" || strings.Contains(strings.Join(r.Limits, " "), "metrics count") {
		t.Errorf("filter by category: %v %v", got, r.Limits)
	}
	r, _ = collect(t, failedRoutes(metrics, exc, nil), `{"network_id":"n1","view":"devices","failure":"connection_refused"}`)
	if got := r.Evidence[0].Detail["devices"].([]map[string]any); len(got) != 1 || got[0]["device"] != "edge1" {
		t.Errorf("filter by type: %v", got)
	}
}

func TestCollectionDevicesViewSaysWhenTheMetricsCountMoreThanTheModelLists(t *testing.T) {
	metrics := map[string]any{"numSuccessfulDevices": 9, "deviceCollectionFailures": map[string]any{"CONNECTION_REFUSED": 5}}
	r, _ := collect(t, failedRoutes(metrics, nil, nil), `{"network_id":"n1","view":"devices","failure":"credentials"}`)
	if !strings.Contains(strings.Join(r.Limits, " "), "metrics count 5 failed device(s) but the device model lists 3") {
		t.Errorf("limits: %v", r.Limits)
	}
}

func TestCollectionNeighborsViewFlagsBGPPeersFirst(t *testing.T) {
	missing := []any{
		map[string]any{"name": "lldp-box", "ipAddresses": []string{"198.51.100.1"}, "discoveryMethod": "LLDP", "neighbors": []string{"edge1"}},
		map[string]any{"name": "upstream", "ipAddresses": []string{"192.0.2.9"}, "discoveryMethod": "IBGP", "neighbors": []string{"edge1"}},
	}
	r, _ := collect(t, failedRoutes(nil, nil, missing), `{"network_id":"n1","view":"neighbors"}`)
	d := r.Evidence[0].Detail
	rows := d["neighbors"].([]map[string]any)
	if r.Status != result.OK || d["bgp_peers"] != 1 || rows[0]["name"] != "upstream" || rows[0]["bgp_peer"] != true || rows[1]["bgp_peer"] != false {
		t.Fatalf("%s %s %v", r.Status, r.Finding, rows)
	}
}

func TestCollectionViewsRejectInputsOfOtherViews(t *testing.T) {
	if _, _, err := runSkill(t, "investigate-collection-failure", cfRoutes("PROCESSED", nil, nil), `{"network_id":"n1","failure":"credentials"}`); err == nil {
		t.Error("failure belongs to view devices")
	}
}

func TestCollectionSlowViewRanksDevicesByCollectionTime(t *testing.T) {
	routes := cfRoutes("PROCESSED", nil, nil)
	routes["GET /api/networks/n1/collection-metrics"] = fwdtest.Const(200, map[string]any{"snapshotId": "s1", "metrics": []any{
		map[string]any{"deviceName": "fast1", "collectionDuration": 4000, "slowestCommand": "show version", "slowestCommandDuration": 900},
		map[string]any{"deviceName": "slow1", "collectionDuration": 190000, "slowestCommand": "show tech", "slowestCommandDuration": 120000, "error": "CONNECTION_TIMEOUT"},
		map[string]any{"deviceName": "none1"}}})
	r, _ := collect(t, routes, `{"network_id":"n1","view":"slow"}`)
	d := r.Evidence[0].Detail
	devs := d["devices"].([]map[string]any)
	if r.Status != result.OK || devs[0]["device"] != "slow1" || devs[0]["slowest_command_ms"] != int64(120000) || devs[0]["error"] != "CONNECTION_TIMEOUT" || devs[2]["device"] != "none1" {
		t.Fatalf("%s %s %v", r.Status, r.Finding, devs)
	}
	if st := d["stats"].(map[string]any); st["with_error"] != 1 || st["with_duration"] != 2 {
		t.Errorf("stats %v", st)
	}
}

func TestCollectionLogsViewNeedsADeviceAndWindowsTheLines(t *testing.T) {
	routes := cfRoutes("PROCESSED", nil, nil)
	routes["GET /api/snapshots/s1/logs"] = func(r *http.Request, _ []byte) (int, any) {
		if r.URL.Query().Get("deviceName") != "r1" || r.URL.Query().Get("level") != "WARN" {
			return 200, []byte("")
		}
		return 200, []byte("2026-09-01 WARN slow prompt\n2026-09-01 ERROR timed out\n")
	}
	if _, _, err := runSkill(t, "investigate-collection-failure", routes, `{"network_id":"n1","view":"logs"}`); err == nil {
		t.Error("view logs needs device")
	}
	r, _ := collect(t, routes, `{"network_id":"n1","view":"logs","device":"r1"}`)
	if r.Status != result.OK || r.Evidence[0].Detail["lines_read"] != 2 {
		t.Fatalf("%s %s %v", r.Status, r.Finding, r.Evidence)
	}
	lg := r.Evidence[0].Detail["log"].(map[string]any)
	if lg["last_line"] != "2026-09-01 ERROR timed out" || lg["lines_in_log"] != 2 || len(lg["cancel_or_timeout_lines"].([]string)) != 1 {
		t.Errorf("the whole-log summary gives the last line and the timeout line: %v", lg)
	}
	r, _ = collect(t, routes, `{"network_id":"n1","view":"logs","device":"other"}`)
	if r.Status != result.Unknown {
		t.Fatalf("an empty log is not proof of a clean collection: %s", r.Status)
	}
}

func TestCollectionLogsViewTreatsA406AsNoLogNotAnError(t *testing.T) {
	routes := cfRoutes("PROCESSED", nil, nil)
	routes["GET /api/snapshots/s1/logs"] = fwdtest.Const(406, map[string]any{"message": "Acceptable representations: [application/json]"})
	r, _ := collect(t, routes, `{"network_id":"n1","view":"logs","device":"r1"}`)
	if r.Status != result.Unknown {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
}

// platformRoutes serves two processed snapshots (s1 older, s2 newest) and the all-devices NQE per snapshot: synthetic Juniper devices where, in s2 only, two
// on the upgraded version fail to parse.
func platformRoutes() map[string]fwdtest.Handler {
	dev := func(name, stage, reason, os, ver string) map[string]any {
		return map[string]any{"device": name, "stage": stage, "reason": reason, "vendor": "JUNIPER", "os": os, "osVersion": ver, "model": "M1"}
	}
	routes := cfRoutes("PROCESSED", map[string]any{"numSuccessfulDevices": 5}, nil)
	routes[snapsPath] = fwdtest.Snapshots(
		fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z"),
		fwdtest.Snap("s2", "PROCESSED", "COLLECTION", "2026-09-02T00:00:00.000Z"))
	routes["GET /api/snapshots/s2/metrics"] = fwdtest.Const(200, map[string]any{"numSuccessfulDevices": 3, "deviceProcessingFailures": map[string]any{"PARSER_EXCEPTION": 2}})
	routes["GET /api/snapshots/s1/metrics"] = fwdtest.Const(200, map[string]any{"numSuccessfulDevices": 5})
	routes[nqePath] = func(r *http.Request, body []byte) (int, any) {
		if !strings.Contains(string(body), "// all devices") {
			return 400, map[string]any{"message": "unexpected query"}
		}
		var rows []map[string]any
		if r.URL.Query().Get("snapshotId") == "s1" {
			rows = []map[string]any{dev("a1", "ok", "", "JUNOS", "21.4"), dev("a2", "ok", "", "JUNOS", "21.4"), dev("a3", "ok", "", "JUNOS", "21.4"),
				dev("b1", "ok", "", "JUNOS", "22.1"), dev("b2", "collection", "DeviceCollectionError.CONNECTION_REFUSED", "JUNOS", "22.1")}
		} else {
			rows = []map[string]any{dev("a1", "ok", "", "JUNOS", "21.4"), dev("a2", "processing", "DeviceProcessingError.PARSER_EXCEPTION", "JUNOS", "23.2"),
				dev("a3", "processing", "DeviceProcessingError.PARSER_EXCEPTION", "JUNOS", "23.2"), dev("b1", "ok", "", "JUNOS", "22.1"), dev("b2", "ok", "", "JUNOS", "22.1")}
		}
		return 200, map[string]any{"items": rows, "totalNumItems": len(rows)}
	}
	return routes
}

func TestCollectionDevicesRollsFailuresUpByPlatformWithRates(t *testing.T) {
	r, _ := collect(t, platformRoutes(), `{"network_id":"n1","view":"platforms"}`)
	groups := r.Evidence[0].Detail["groups"].([]map[string]any)
	if r.Status != result.Failed || len(groups) != 1 || groups[0]["group"] != "JUNIPER JUNOS 23.2" || groups[0]["failed"] != 2 || groups[0]["failure_rate"] != "100%" {
		t.Fatalf("%s %s %v", r.Status, r.Finding, groups)
	}
}

func TestCollectionDevicesComparesWithThePreviousSnapshot(t *testing.T) {
	r, _ := collect(t, platformRoutes(), `{"network_id":"n1","view":"changes"}`)
	if len(r.Evidence) == 0 {
		t.Fatalf("%s %s %v", r.Status, r.Finding, r.Limits)
	}
	d := r.Evidence[0].Detail
	if r.Status != result.Failed || d["new_failure_count"] != 2 || d["recovered_count"] != 1 || d["new_after_os_version_change"] != 2 || d["compared_with"] != "s1" {
		t.Fatalf("%s %s %v", r.Status, r.Finding, d)
	}
	if !strings.Contains(r.Finding, "2 of the new failures are on a device whose OS version changed") {
		t.Errorf("%s", r.Finding)
	}
}

func TestCollectionExceptionsViewShowsAnErrorTheCollectorIgnored(t *testing.T) {
	routes := cfRoutes("PROCESSED", map[string]any{"numSuccessfulDevices": 3}, nil)
	routes["GET /api/collector/exceptions"] = fwdtest.Const(200, map[string]any{"actualNumDedupedExceptions": 2, "dedupedExceptions": []any{
		map[string]any{"stackTrace": "com.example.GcpQuotaException: HTTP 400 quota api\n\tat frame1", "actualNumOccurrences": 7, "collectorVersion": "2.0.1",
			"occurrences": []any{map[string]any{"collectorId": "c1", "deviceName": "gcp-prod"}, map[string]any{"collectorId": "c1", "deviceName": "gcp-prod"}}},
		map[string]any{"stackTrace": "java.net.SocketTimeoutException: read timed out", "actualNumOccurrences": 1,
			"occurrences": []any{map[string]any{"collectorId": "c1", "deviceName": "edge1"}}}}})
	r, _ := collect(t, routes, `{"network_id":"n1","view":"exceptions"}`)
	d := r.Evidence[0].Detail
	ex := d["exceptions"].([]map[string]any)
	if r.Status != result.Failed || len(ex) != 2 || ex[0]["message"] != "com.example.GcpQuotaException: HTTP 400 quota api" || ex[0]["occurrences"] != 7 || !strings.Contains(r.Finding, "GcpQuotaException") {
		t.Fatalf("%s %s %v", r.Status, r.Finding, d)
	}
	r, _ = collect(t, routes, `{"network_id":"n1","view":"exceptions","device":"edge"}`)
	if got := r.Evidence[0].Detail["exceptions"].([]map[string]any); len(got) != 1 {
		t.Errorf("filter by device or account name: %v", got)
	}
	// without the permission: an explained unknown, not an error
	routes["GET /api/collector/exceptions"] = fwdtest.Const(403, map[string]any{"message": "Missing permission: OrgOperation.VIEW_COLLECTOR_EXCEPTIONS"})
	r, _ = collect(t, routes, `{"network_id":"n1","view":"exceptions"}`)
	if r.Status != result.Unknown || !strings.Contains(r.Finding, "VIEW_COLLECTOR_EXCEPTIONS") {
		t.Errorf("%s %s", r.Status, r.Finding)
	}
}

func TestCollectionSummarySurvivesAMissingDevicesTimeoutAsALimit(t *testing.T) {
	routes := cfRoutes("PROCESSED", nil, nil)
	routes["GET /api/networks/n1/missing-devices"] = func(*http.Request, []byte) (int, any) { return 599, nil }
	r, _ := collect(t, routes, "")
	if r.Status != result.OK || !strings.Contains(strings.Join(r.Limits, " "), "unmodelled neighbour devices could not be read") || !strings.Contains(strings.Join(r.Limits, " "), "FORWARD_TIMEOUT") {
		t.Fatalf("a slow/failed missing-devices read must not block the summary: %s %v", r.Status, r.Limits)
	}
}

func TestCollectionTriageMergesSummarySlowestAndWorstPlatform(t *testing.T) {
	routes := platformRoutes() // two snapshots, synthetic Juniper devices, s2 has 2 new PARSER_EXCEPTION failures on JUNOS 23.2
	routes["GET /api/networks/n1/endpoints"] = fwdtest.Const(200, []any{})
	routes["GET /api/snapshots/s2/exceptions"] = fwdtest.Const(200, map[string]any{"exceptions": []any{}})
	routes["GET /api/networks/n1/collection-metrics"] = fwdtest.Const(200, map[string]any{"snapshotId": "s2", "metrics": []any{
		map[string]any{"deviceName": "a2", "collectionDuration": 90000, "slowestCommand": "show tech", "slowestCommandDuration": 80000}}})
	r, _ := collect(t, routes, `{"network_id":"n1","view":"triage"}`)
	if r.Status != result.Failed {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	if !strings.Contains(r.Finding, "slowest: a2") || !strings.Contains(r.Finding, "worst platform: JUNIPER JUNOS 23.2") {
		t.Fatalf("triage must merge the slowest device and the worst platform into the finding: %s", r.Finding)
	}
	var names []string
	for _, e := range r.Evidence {
		names = append(names, e.Source.Operation)
	}
	if !slices.Contains(names, "triageSlowest") || !slices.Contains(names, "triageWorstPlatform") {
		t.Errorf("evidence sources: %v", names)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "does not read the organization's license capacity") {
		t.Errorf("triage must say license capacity is not read: %v", r.Limits)
	}
}

func TestCollectionTriageOnAHealthyNetworkIsStillOK(t *testing.T) {
	routes := cfRoutes("PROCESSED", nil, nil)
	routes["GET /api/networks/n1/collection-metrics"] = fwdtest.Const(200, map[string]any{"deviceMetrics": []any{}})
	r, _ := collect(t, routes, `{"network_id":"n1","view":"triage"}`)
	if r.Status != result.OK {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
}

func collSnap(id, created, processed string, devices int, task string) map[string]any {
	m := map[string]any{"id": id, "state": "PROCESSED", "processingTrigger": "COLLECTION", "createdAt": created, "processedAt": processed, "totalDevices": devices}
	if task != "" {
		m["collectionTaskId"] = task
	}
	return m
}

func collectionHistRoutes() map[string]fwdtest.Handler {
	metrics := func(collectionMs, processingMs int64, ok int) fwdtest.Handler {
		return fwdtest.Const(200, map[string]any{"snapshotId": "x", "numSuccessfulDevices": ok, "numCollectionFailureDevices": 3, "collectionDuration": collectionMs, "processingDuration": processingMs})
	}
	return map[string]fwdtest.Handler{
		snapsPath: fwdtest.Snapshots(
			collSnap("s1", "2026-09-10T06:00:00Z", "2026-09-10T07:10:00Z", 4500, ""),
			collSnap("s2", "2026-09-17T06:00:00Z", "2026-09-17T07:10:00Z", 4510, ""),
			collSnap("s3", "2026-09-24T06:00:00Z", "2026-09-24T07:10:00Z", 4520, ""),
			collSnap("s4", "2026-10-01T09:00:00Z", "2026-10-01T10:30:00Z", 20000, ""),
			collSnap("s5", "2026-10-02T09:46:00Z", "2026-10-02T09:46:53Z", 40000, "1021"),
			// a reprocess of the oldest, processed today: not a collection, and must not count as the newest
			map[string]any{"id": "rp", "state": "PROCESSED", "processingTrigger": "REPROCESS", "createdAt": "2026-09-10T06:00:00Z", "processedAt": "2026-10-02T14:00:00Z", "totalDevices": 4500},
			map[string]any{"id": "pr", "state": "PROCESSED", "processingTrigger": "PREDICT", "processedAt": "2026-10-02T15:00:00Z"},
		),
		"GET /api/snapshots/s1/metrics":  metrics(3_700_000, 600_000, 4497),
		"GET /api/snapshots/s2/metrics":  metrics(3_500_000, 610_000, 4507),
		"GET /api/snapshots/s3/metrics":  metrics(3_600_000, 600_000, 4517),
		"GET /api/snapshots/s4/metrics":  metrics(6_400_000, 900_000, 19990),
		"GET /api/snapshots/s5/metrics":  metrics(12_000_000, 1_500_000, 39900),
		"GET /api/collector-tasks/P1021": fwdtest.Const(200, map[string]any{"id": "P1021", "type": "NETWORK_COLLECTION", "status": "DONE", "startedAt": "2026-10-02T06:00:14Z", "finishedAt": "2026-10-02T09:20:39Z"}),
	}
}

func TestCollectionHistoryShowsDurationsDeviceGrowthAndFlagsTheSlowOnes(t *testing.T) {
	r, _ := collect(t, collectionHistRoutes(), `{"network_id":"n1","view":"history"}`)
	if r.Status != result.OK {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	d := r.Evidence[0].Detail
	rows := d["snapshots"].([]map[string]any)
	if len(rows) != 5 || rows[0]["snapshot_id"] != "s5" || rows[4]["snapshot_id"] != "s1" {
		t.Fatalf("five COLLECTION snapshots, newest first, no reprocess and no prediction: %v", rows)
	}
	if rows[0]["collection_seconds"] != 12000.0 || rows[0]["processing_seconds"] != 1500.0 || rows[0]["devices"] != 40000 || rows[0]["devices_change"] != 20000 {
		t.Errorf("latest: %v", rows[0])
	}
	// the collector task gives the real start and end, and the lag from the collection ending to the snapshot being usable
	if rows[0]["task_seconds"] != 12025.0 || rows[0]["collection_end_to_processed_seconds"] != 1574.0 {
		t.Errorf("task timing: %v", rows[0])
	}
	stats := d["stats"].(map[string]any)
	if stats["median_collection_seconds"] != 3700.0 || stats["slower_than_usual"] != 2 {
		t.Errorf("median 3700s and two collections above 1.5x it (s4 and s5): %v", stats)
	}
	if rows[0]["slower_than_usual"] != true || rows[1]["slower_than_usual"] != true || rows[2]["slower_than_usual"] == true || rows[3]["slower_than_usual"] == true {
		t.Errorf("only s5 and s4 are flagged: %v %v %v %v", rows[0]["slower_than_usual"], rows[1]["slower_than_usual"], rows[2]["slower_than_usual"], rows[3]["slower_than_usual"])
	}
	if !strings.Contains(r.Finding, "3h20m") || !strings.Contains(r.Finding, "3.2x") || !strings.Contains(r.Finding, "+20000 devices") {
		t.Errorf("the finding must say how long, against what, and the device change: %s", r.Finding)
	}
	if !strings.Contains(strings.Join(r.Limits, " | "), "not collections") {
		t.Errorf("limits must say reprocesses are excluded: %v", r.Limits)
	}
}

func TestCollectionHistoryDoesNotWaitForASnapshotStillProcessingAndRejectsForeignInputs(t *testing.T) {
	routes := collectionHistRoutes()
	routes[snapsPath] = fwdtest.Snapshots(
		collSnap("s1", "2026-09-10T06:00:00Z", "2026-09-10T07:10:00Z", 4500, ""),
		map[string]any{"id": "s2", "state": "PROCESSING", "processingTrigger": "COLLECTION", "createdAt": "2026-10-02T09:00:00Z"},
	)
	r, _ := collect(t, routes, `{"network_id":"n1","view":"history"}`)
	if r.Status == result.Unknown && strings.Contains(r.Finding, "still") {
		t.Errorf("history reads finished collections and must not wait for the one in progress: %s", r.Finding)
	}
	if _, _, err := runSkill(t, "investigate-collection-failure", routes, `{"network_id":"n1","view":"history","device":"x"}`); err == nil {
		t.Error("device belongs to other views")
	}
}

func TestCollectionSlowViewSumsDeviceTimeComparesItToTheCollectorAndGroupsIt(t *testing.T) {
	routes := cfRoutes("PROCESSED", nil, nil)
	dev := func(name, typ, conn string, ms int, errText string) map[string]any {
		m := map[string]any{"deviceName": name, "deviceType": typ, "connTypeDisplayName": conn, "collectionDuration": ms}
		if errText != "" {
			m["error"] = errText
		}
		return m
	}
	// 4 devices, 100s of device time each = 400s, over a 100s collection: 4 devices at once on average
	routes["GET /api/networks/n1/collection-metrics"] = fwdtest.Const(200, map[string]any{"snapshotId": "s1", "collectionStartTime": 1_000_000, "collectionEndTime": 1_100_000, "metrics": []any{
		dev("fw1", "FIREWALL", "SSH", 100000, ""), dev("fw2", "FIREWALL", "SSH", 100000, "LICENSE_EXHAUSTED: no slot"),
		dev("sw1", "SWITCH", "SNMP", 100000, "LICENSE_EXHAUSTED: no slot"), dev("sw2", "SWITCH", "SNMP", 100000, "")}})
	routes["GET /api/networks/n1/collector"] = fwdtest.Const(200, map[string]any{"id": "7", "name": "Garland", "connectionStatus": "CONNECTED"})
	routes["GET /api/collectors/7/collection-settings"] = fwdtest.Const(200, map[string]any{})
	r, _ := collect(t, routes, `{"network_id":"n1","view":"slow"}`)
	st := r.Evidence[0].Detail["stats"].(map[string]any)
	if st["sum_ms"] != int64(400000) || st["collection_wall_ms"] != int64(100000) || st["implied_parallelism"] != 4.0 {
		t.Errorf("sum, wall and implied parallelism: %v", st)
	}
	c, _ := st["collector"].(map[string]any)
	if c["name"] != "Garland" || c["concurrency"] != 128 || c["concurrency_is_default"] != true || st["concurrency_in_use_percent"] != 4.0/128*100 {
		t.Errorf("an unset concurrency is the documented default (128), said so, and compared with the parallelism: %v", st)
	}
	groups := st["by_device_type"].([]map[string]any)
	if len(groups) != 2 || groups[0]["devices_timed"] != 2 || groups[0]["sum_ms"] != int64(200000) {
		t.Errorf("by device type: %v", groups)
	}
	if e := st["errors_by_type"].(map[string]int); e["LICENSE_EXHAUSTED"] != 2 {
		t.Errorf("errors are grouped by class, not by text: %v", e)
	}
	for _, want := range []string{"4 devices at once", "collector Garland runs 128 at once, the default", "total device time"} {
		if !strings.Contains(r.Finding, want) {
			t.Errorf("the headline numbers go in the finding (a table rendering prints only that): missing %q in %q", want, r.Finding)
		}
	}
	// a name filter sees a slice of the run, so the whole-run parallelism is not claimed for it
	r, _ = collect(t, routes, `{"network_id":"n1","view":"slow","device":"fw"}`)
	if _, has := r.Evidence[0].Detail["stats"].(map[string]any)["implied_parallelism"]; has {
		t.Error("parallelism is a whole-run figure and must not be computed for a filtered slice")
	}
}

// Ten devices over a 20 minute run: five run 0-5 min, nothing runs 5-10, five run 10-20. The profile has to show the start, the stall and the tail.
func slowProfileRoutes(endsAtMs int64, firstBatchMs int64) map[string]fwdtest.Handler {
	routes := cfRoutes("PROCESSED", nil, nil)
	var devs []any
	for i := 0; i < 5; i++ {
		devs = append(devs, map[string]any{"deviceName": "a" + string(rune('0'+i)), "deviceType": "SWITCH", "connTypeDisplayName": "SSH", "collectionStartTime": 0, "collectionDuration": firstBatchMs})
	}
	for i := 0; i < 5; i++ {
		devs = append(devs, map[string]any{"deviceName": "b" + string(rune('0'+i)), "deviceType": "FIREWALL", "connTypeDisplayName": "PAN-OS", "collectionStartTime": 600_000, "collectionDuration": endsAtMs - 600_000})
	}
	routes["GET /api/networks/n1/collection-metrics"] = fwdtest.Const(200, map[string]any{"snapshotId": "s1", "collectionStartTime": 0, "collectionEndTime": endsAtMs, "metrics": devs})
	return routes
}

func TestCollectionSlowViewShowsWhenConcurrencyFellNotJustTheAverage(t *testing.T) {
	r, _ := collect(t, slowProfileRoutes(1_200_000, 300_000), `{"network_id":"n1","view":"slow"}`)
	prof := r.Evidence[0].Detail["stats"].(map[string]any)["in_flight"].(map[string]any)
	bk := prof["in_flight_buckets"].([]map[string]any)
	got := []any{}
	for _, b := range bk {
		got = append(got, b["devices_in_flight"])
	}
	if len(bk) != 4 || got[0] != 5.0 || got[1] != 0.0 || got[2] != 5.0 || got[3] != 5.0 || prof["peak_devices_in_flight"] != 5.0 || prof["bucket_seconds"] != int64(300) {
		t.Errorf("five devices, then a stall, then five again: %v", got)
	}
	fin := prof["finished_by_seconds"].(map[string]float64)
	if fin["p50"] != 300 || fin["p99"] != 1200 || fin["max"] != 1200 {
		t.Errorf("half the devices were done at 5 minutes, all at 20: %v", fin)
	}
	if !strings.Contains(r.Finding, "99% of devices had finished by 20m00s") {
		t.Errorf("the finding must say when the tail ended: %s", r.Finding)
	}
}

func TestCollectionSlowViewComparesTwoCollectionsAndOnlyInTheSlowView(t *testing.T) {
	routes := slowProfileRoutes(1_200_000, 300_000)
	// the baseline snapshot's metrics: the same devices, firewalls twice as fast, so less total time
	base := slowProfileRoutes(900_000, 300_000)["GET /api/networks/n1/collection-metrics"]
	cur := routes["GET /api/networks/n1/collection-metrics"]
	routes["GET /api/networks/n1/collection-metrics"] = func(r *http.Request, b []byte) (int, any) {
		if r.URL.Query().Get("snapshotId") == "base" {
			return base(r, b)
		}
		return cur(r, b)
	}
	r, _ := collect(t, routes, `{"network_id":"n1","view":"slow","compare_to_snapshot_id":"base"}`)
	c, ok := r.Evidence[0].Detail["stats"].(map[string]any)["compare"].(map[string]any)
	if !ok {
		t.Fatalf("no comparison: %v %v", r.Finding, r.Limits)
	}
	ch := c["change"].(map[string]any)
	// 5 x 300 s + 5 x 600 s = 4500 s now; 5 x 300 s + 5 x 300 s = 3000 s before
	if ch["sum_ms"] != int64(1_500_000) || c["baseline_snapshot_id"] != "base" {
		t.Errorf("change: %v", ch)
	}
	fw := c["by_device_type"].([]map[string]any)[0]
	if fw["name"] != "FIREWALL" || fw["change_ms"] != int64(1_500_000) || fw["baseline_sum_ms"] != int64(1_500_000) {
		t.Errorf("the firewalls carry the whole change and come first: %v", fw)
	}
	if !strings.Contains(r.Finding, "against base: total device time 50m00s to 1h15m") {
		t.Errorf("the finding must carry the comparison: %s", r.Finding)
	}
	if _, _, err := runSkill(t, "investigate-collection-failure", routes, `{"network_id":"n1","view":"devices","compare_to_snapshot_id":"base"}`); err == nil {
		t.Error("compare_to_snapshot_id belongs to view slow only")
	}
}

func TestCollectionSlowViewFindsAnIdleStretchAndSaysWhatStartedAfterIt(t *testing.T) {
	routes := cfRoutes("PROCESSED", nil, nil)
	var devs []any
	for i := 0; i < 6; i++ { // a first batch of switches, 0 to 5 minutes
		devs = append(devs, map[string]any{"deviceName": "sw" + string(rune('0'+i)), "deviceType": "SWITCH", "connTypeDisplayName": "SSH", "collectionStartTime": 0, "collectionDuration": 300_000})
	}
	for i := 0; i < 4; i++ { // a second batch of routers starting at 30 minutes, after 25 minutes with nothing running
		devs = append(devs, map[string]any{"deviceName": "r" + string(rune('0'+i)), "deviceType": "ROUTER", "connTypeDisplayName": "Cisco IOS-XE", "collectionStartTime": 1_800_000, "collectionDuration": 300_000})
	}
	routes["GET /api/networks/n1/collection-metrics"] = fwdtest.Const(200, map[string]any{"snapshotId": "s1", "collectionStartTime": 0, "collectionEndTime": 2_100_000, "metrics": devs})
	r, _ := collect(t, routes, `{"network_id":"n1","view":"slow"}`)
	prof := r.Evidence[0].Detail["stats"].(map[string]any)["in_flight"].(map[string]any)
	gaps, _ := prof["idle_gaps"].([]map[string]any)
	if len(gaps) != 1 || gaps[0]["from_seconds"] != int64(300) || gaps[0]["to_seconds"] != int64(1800) || gaps[0]["idle_seconds"] != int64(1500) {
		t.Fatalf("one idle stretch from 5 to 30 minutes: %v", gaps)
	}
	after := prof["started_after_the_longest_gap"].(map[string]any)
	top := after["most_common"].([]map[string]any)
	if after["devices"] != 4 || top[0]["connection"] != "Cisco IOS-XE" || top[0]["device_type"] != "ROUTER" {
		t.Errorf("the second batch is four IOS-XE routers: %v", after)
	}
	if !strings.Contains(r.Finding, "NOTHING was being collected for 25m00s (5m00s to 30m00s), then 4 more devices started, mostly Cisco IOS-XE ROUTER (4)") {
		t.Errorf("the finding must name the gap and what came after it: %s", r.Finding)
	}
	if !strings.Contains(strings.Join(r.Limits, " | "), "can exceed the collector's configured concurrency") {
		t.Errorf("limits must warn that in-flight can exceed the configured concurrency: %v", r.Limits)
	}
	// a short stall (under three buckets) is not reported as a gap
	r2, _ := collect(t, slowProfileRoutes(1_200_000, 300_000), `{"network_id":"n1","view":"slow"}`)
	if _, has := r2.Evidence[0].Detail["stats"].(map[string]any)["in_flight"].(map[string]any)["idle_gaps"]; has {
		t.Error("a one-bucket stall is not an idle stretch")
	}
}

func TestCollectionHistoryListsACollectionWhoseSnapshotWasReplacedByAReprocess(t *testing.T) {
	routes := collectionHistRoutes()
	// 2026-10-01's collection (task P2000) now survives only as a reprocess of its snapshot; 2026-10-02 (P1021) is shown with s5
	routes["GET /api/collector-tasks"] = fwdtest.Const(200, []any{
		map[string]any{"id": "P1021", "networkId": "n1", "type": "NETWORK_COLLECTION", "status": "DONE", "startedAt": "2026-10-02T06:00:14Z", "finishedAt": "2026-10-02T09:20:39Z"},
		map[string]any{"id": "P2000", "networkId": "n1", "type": "NETWORK_COLLECTION", "status": "DONE", "startedAt": "2026-10-01T06:06:03Z", "finishedAt": "2026-10-01T07:52:42Z"},
		map[string]any{"id": "P2001", "networkId": "n1", "type": "NETWORK_COLLECTION", "status": "DONE", "startedAt": "2026-08-01T06:00:00Z", "finishedAt": "2026-08-01T07:00:00Z"}, // older than the rows read
		map[string]any{"id": "P2002", "networkId": "n1", "type": "OTHER", "status": "DONE", "startedAt": "2026-10-01T10:00:00Z", "finishedAt": "2026-10-01T10:05:00Z"},
	})
	routes[snapsPath] = fwdtest.Snapshots(
		collSnap("s1", "2026-09-10T06:00:00Z", "2026-09-10T07:10:00Z", 4500, ""),
		collSnap("s5", "2026-10-02T09:46:00Z", "2026-10-02T09:46:53Z", 40000, "1021"),
		map[string]any{"id": "rp", "state": "PROCESSED", "processingTrigger": "REPROCESS", "createdAt": "2026-10-01T07:53:00Z", "processedAt": "2026-10-02T14:00:00Z", "totalDevices": 20000, "collectionTaskId": "2000"},
	)
	r, _ := collect(t, routes, `{"network_id":"n1","view":"history"}`)
	or := r.Evidence[0].Detail["collections_without_a_shown_snapshot"].([]map[string]any)
	if len(or) != 1 || or[0]["task_id"] != "P2000" || or[0]["task_seconds"] != 6399.0 || or[0]["snapshot_id"] != "rp" || !strings.Contains(or[0]["snapshot_kind"].(string), "REPROCESS") {
		t.Fatalf("only the 10-01 collection: shown ones, other task types and tasks older than the rows are left out: %v", or)
	}
	if !strings.Contains(r.Finding, "1 more collection(s) ran whose snapshot is not shown") {
		t.Errorf("finding: %s", r.Finding)
	}
}

func TestCollectionSlowViewTiesAGapToThePerDeviceTimeoutAndListsDevicesWithNoRecordedCollection(t *testing.T) {
	routes := cfRoutes("PROCESSED", nil, nil)
	var devs []any
	for i := 0; i < 6; i++ {
		devs = append(devs, map[string]any{"deviceName": "sw" + string(rune('0'+i)), "deviceType": "SWITCH", "connTypeDisplayName": "SSH", "collectionStartTime": 0, "collectionDuration": 300_000})
	}
	for i := 0; i < 4; i++ { // the second batch starts exactly at the 30 minute per-device timeout
		devs = append(devs, map[string]any{"deviceName": "r" + string(rune('0'+i)), "deviceType": "ROUTER", "connTypeDisplayName": "Cisco IOS-XE", "collectionStartTime": 1_800_000, "collectionDuration": 300_000})
	}
	// a firewall that started and never recorded a duration
	devs = append(devs, map[string]any{"deviceName": "fw1", "deviceType": "PAN_OS", "connTypeDisplayName": "SSH", "collectionStartTime": 60_000})
	routes["GET /api/networks/n1/collection-metrics"] = fwdtest.Const(200, map[string]any{"snapshotId": "s1", "collectionStartTime": 0, "collectionEndTime": 2_100_000, "metrics": devs})
	routes["GET /api/collection-settings"] = fwdtest.Const(200, map[string]any{"deviceCollectionTimeoutMinutes": 30})
	r, _ := collect(t, routes, `{"network_id":"n1","view":"slow"}`)
	st := r.Evidence[0].Detail["stats"].(map[string]any)
	nd := st["no_recorded_duration"].(map[string]any)
	shown := nd["shown"].([]map[string]any)
	if nd["devices"] != 1 || shown[0]["device"] != "fw1" || shown[0]["start_offset_seconds"] != int64(60) {
		t.Errorf("fw1 has no recorded collection: %v", nd)
	}
	if !strings.Contains(r.Finding, "NO device ran to it") || !strings.Contains(r.Finding, "1 device(s) ended without a recorded collection duration") {
		t.Errorf("the finding ties the gap to the timeout and counts devices without a duration: %s", r.Finding)
	}
	if st["org_collection_settings"].(map[string]any)["idle_gap_ends_at_the_timeout"] != true {
		t.Errorf("%v", st["org_collection_settings"])
	}
	if first := st["in_flight"].(map[string]any)["started_after_the_longest_gap"].(map[string]any)["first_devices"].([]string); len(first) != 4 {
		t.Errorf("names of the late starters: %v", first)
	}
}

func TestCollectionSlowViewReadsTheTaskSeriesForTheGapAndClassifiesDevicesWithNoCollection(t *testing.T) {
	routes := cfRoutes("PROCESSED", nil, nil)
	snap := fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")
	snap["collectionTaskId"] = "500"
	routes[snapsPath] = fwdtest.Snapshots(snap)
	var devs []any
	for i := 0; i < 6; i++ {
		devs = append(devs, map[string]any{"deviceName": "sw" + string(rune('0'+i)), "deviceType": "SWITCH", "connTypeDisplayName": "SSH", "collectionStartTime": 0, "collectionDuration": 300_000})
	}
	for i := 0; i < 4; i++ {
		devs = append(devs, map[string]any{"deviceName": "r" + string(rune('0'+i)), "deviceType": "ROUTER", "connTypeDisplayName": "Cisco IOS-XE", "collectionStartTime": 1_800_000, "collectionDuration": 300_000})
	}
	devs = append(devs, map[string]any{"deviceName": "fw1", "deviceType": "PAN_OS", "connTypeDisplayName": "SSH", "collectionStartTime": 60_000})
	routes["GET /api/networks/n1/collection-metrics"] = fwdtest.Const(200, map[string]any{"snapshotId": "s1", "collectionStartTime": 0, "collectionEndTime": 2_100_000, "metrics": devs})
	routes["GET /api/collector-tasks"] = func(r *http.Request, _ []byte) (int, any) {
		if r.URL.Query().Get("snapshotId") == "" {
			return 200, []any{}
		}
		return 200, map[string]any{"timestamps": []int64{0, 200_000, 400_000, 1_000_000, 1_700_000, 1_800_000},
			"queued": []int{0, 0, 0, 0, 0, 0}, "running": []int{7, 7, 1, 1, 1, 4}, "concurrency": []int{7, 7, 1, 1, 1, 4}, "succeeded": []int{0, 0, 6, 6, 6, 6},
			"concurrencyLimits": map[string]any{"global": 128}}
	}
	routes["GET /api/collector-tasks/P500"] = func(r *http.Request, _ []byte) (int, any) {
		if r.URL.Query().Get("view") == "subtasks" {
			return 200, []any{map[string]any{"id": "v", "description": "Cisco SD-WAN vsmart family", "status": "RUNNING", "startedAt": 0, "operation": "show omp routes"}}
		}
		return 200, map[string]any{"id": "P500", "status": "FINISHED", "subTasks": []any{
			map[string]any{"id": "a", "description": "fw1", "status": "TIMED_OUT", "startedAt": 60_000, "finishedAt": 10_860_000},
			map[string]any{"id": "b", "description": "r9", "status": "CANCELED", "startedAt": 0, "finishedAt": 120_000}}}
	}
	r, _ := collect(t, routes, `{"network_id":"n1","view":"slow"}`)
	st := r.Evidence[0].Detail["stats"].(map[string]any)
	q := st["queue"].(map[string]any)
	g := q["during_the_longest_idle_gap"].(map[string]any)
	if g["max_queued"] != 0 || g["max_running"] != 1 || q["concurrency_limit"] != 128 {
		t.Errorf("one device was still running in the gap and nothing queued: %v", q)
	}
	es := st["no_recorded_duration"].(map[string]any)["end_states"].(map[string]any)
	bad := es["failed_timed_out_or_cancelled"].([]map[string]any)
	if len(bad) != 2 || bad[0]["status"] != "CANCELED" || bad[1]["device"] != "fw1" || bad[1]["ran_seconds"] != 10800.0 {
		t.Errorf("fw1 ran its full 3h and timed out: %v", bad)
	}
	run := st["running_in_the_gap"].(map[string]any)
	if run["subtasks_in_progress"] != 1 || run["subtasks"].([]map[string]any)[0]["description"] != "Cisco SD-WAN vsmart family" {
		t.Errorf("the subtask running mid-gap is named: %v", run)
	}
	if !strings.Contains(r.Finding, "up to 0 queued and 1 running during the gap") {
		t.Errorf("the finding reports the queue during the gap: %s", r.Finding)
	}
}
