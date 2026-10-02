package skills_test

import (
	"net/http"
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
