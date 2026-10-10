package skills_test

import (
	"encoding/json"
	"net/http"
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

func TestCollectionConfigListsDataFilesWithAttachmentAndCanPreviewOneSchema(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/classic-devices": fwdtest.Const(200, []any{}),
		"GET /api/data-files": fwdtest.Const(200, []any{
			map[string]any{"name": "sites", "nqeName": "sites", "type": "CSV", "description": "site ownership", "networkIds": []string{"n1", "n2"}},
			map[string]any{"name": "empty-one", "nqeName": "emptyOne", "type": "JSON", "networkIds": []string{}, "isEmpty": true},
		}),
		"GET /api/data-files/sites/schema":     fwdtest.Const(200, map[string]any{"content": "site,owner\nnyc,ops\n", "inference": map[string]any{"dataFormat": "CSV", "warnings": []string{}, "errors": []string{}, "schema": map[string]any{"type": "List"}}}),
		"GET /api/networks/n1/data-connectors": fwdtest.Const(200, map[string]any{"connectors": []any{}}),
	}
	r, srv := mustRun(t, "inspect-collection-config", routes, `{"network_id":"n1"}`)
	b := jsonOf(r)
	for _, want := range []string{`"data_files"`, `"attached_to_this_network":true`, `"nqe_name":"sites"`, `"empty":true`} {
		if !strings.Contains(b, want) {
			t.Errorf("missing %s in %s", want, b)
		}
	}
	if r.Status != result.OK {
		t.Fatalf("a network with no classic devices but a data file is configured: %s", r.Status)
	}
	// previewing one file's inferred schema is a second, separate call, and only happens when asked
	if writes(srv) != 0 {
		t.Errorf("reading data files and their schema must never write")
	}
	r, _ = mustRun(t, "inspect-collection-config", routes, `{"network_id":"n1","data_file":"sites"}`)
	if b = jsonOf(r); strings.Contains(b, "nyc,ops") || !strings.Contains(b, "include_content") {
		t.Errorf("content must be opt-in: %s", b)
	}
	r, _ = mustRun(t, "inspect-collection-config", routes, `{"network_id":"n1","data_file":"sites","include_content":true}`)
	b = jsonOf(r)
	for _, want := range []string{`"data_file_schema"`, `"data_format":"CSV"`, `nyc,ops`} {
		if !strings.Contains(b, want) {
			t.Errorf("missing %s in %s", want, b)
		}
	}
	// an exact-name mismatch is named, not guessed at
	r, _ = mustRun(t, "inspect-collection-config", routes, `{"network_id":"n1","data_file":"Sites"}`)
	if !strings.Contains(strings.Join(r.Limits, " "), `"Sites" does not match`) {
		t.Errorf("limits: %v", r.Limits)
	}
}

func TestCollectionConfigListsDataConnectorsWithStatusAndCanShowOneDetail(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/classic-devices": fwdtest.Const(200, []any{}),
		"GET /api/data-files":                  fwdtest.Const(200, []any{}),
		"GET /api/networks/n1/data-connectors": fwdtest.Const(200, map[string]any{
			"snapshotId": "snap1",
			"connectors": []any{
				map[string]any{"name": "weather-feed", "baseUrl": "https://example.test", "endpoints": []any{map[string]any{"name": "ep1", "path": "/a"}}, "status": map[string]any{}},
				map[string]any{"name": "broken-feed", "baseUrl": "https://example.test", "endpoints": []any{map[string]any{"name": "ep1", "path": "/a"}}},
			},
		}),
		"GET /api/networks/n1/data-connectors/weather-feed": fwdtest.Const(200, map[string]any{
			"name": "weather-feed", "baseUrl": "https://example.test",
			"endpoints":  []any{map[string]any{"name": "ep1", "path": "/a"}},
			"testResult": map[string]any{"startedAt": "t0", "endedAt": "t1"},
		}),
	}
	r, _ := mustRun(t, "inspect-collection-config", routes, `{"network_id":"n1"}`)
	b := jsonOf(r)
	for _, want := range []string{`"data_connectors"`, `"name":"weather-feed"`, `"status":"ok"`, `"status":"missing (not in the latest snapshot: never collected, or excluded)"`} {
		if !strings.Contains(b, want) {
			t.Errorf("missing %s in %s", want, b)
		}
	}
	r, _ = mustRun(t, "inspect-collection-config", routes, `{"network_id":"n1","data_connector":"weather-feed"}`)
	b = jsonOf(r)
	for _, want := range []string{`"data_connector_detail"`, `"last_test"`, `"started_at":"t0"`} {
		if !strings.Contains(b, want) {
			t.Errorf("missing %s in %s", want, b)
		}
	}
	r, _ = mustRun(t, "inspect-collection-config", routes, `{"network_id":"n1","data_connector":"nope"}`)
	if !strings.Contains(strings.Join(r.Limits, " "), `"nope" does not match`) {
		t.Errorf("limits: %v", r.Limits)
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

func TestCollectionConfigShowsCloudSetupsWithRegionsAndTheLastTestAndNeverACredential(t *testing.T) {
	r, _ := mustRun(t, "inspect-collection-config", map[string]fwdtest.Handler{
		"GET /api/networks/n1/classic-devices": fwdtest.Const(200, []any{}),
		"GET /api/networks/n1/cloudAccounts": fwdtest.Const(200, []any{map[string]any{"type": "AWS", "name": "prod-aws", "collect": true, "username": "AKIA-SECRET-KEY", "password": "s3cret-value", "concurrency": 8,
			"regions": map[string]any{"us-east-1": map[string]any{"error": "NONE", "testInstant": 1790000000000}, "us-west-2": map[string]any{"error": "AUTHENTICATION_FAILED", "testInstant": 1790000000000}, "eu-west-1": nil}}}),
	}, `{"network_id":"n1"}`)
	b := jsonOf(r)
	for _, leak := range []string{"AKIA-SECRET-KEY", "s3cret-value"} {
		if strings.Contains(b, leak) {
			t.Errorf("%q leaked into the result: %s", leak, b)
		}
	}
	for _, want := range []string{`"cloud_setups"`, `"prod-aws"`, `"region":"us-west-2"`, `"last_test":"AUTHENTICATION_FAILED"`, `"last_test":"ok"`, `"last_test":"never tested"`, `"failing_tests":1`, "1 cloud setups"} {
		if !strings.Contains(b, want) {
			t.Errorf("missing %s in %s", want, b)
		}
	}
	if r.Status != result.OK {
		t.Errorf("a network whose only source is a cloud setup is configured, not unknown: %s", r.Status)
	}
}

func TestCollectionConfigSummarisesTheWholeDeviceListNotJustThePage(t *testing.T) {
	devs := []any{}
	for i := 0; i < 5; i++ {
		d := map[string]any{"name": "sw" + string(rune('0'+i)), "host": "10.0.0.1", "type": "CISCO_IOS_XE", "cliCredentialId": "c1", "enableSnmpCollection": i < 2}
		devs = append(devs, d)
	}
	devs = append(devs, map[string]any{"name": "fw1", "host": "10.0.0.9", "type": "PALO_ALTO_PANOS"}, map[string]any{"name": "x", "host": "10.0.0.8"})
	routes := map[string]fwdtest.Handler{"GET /api/networks/n1/classic-devices": fwdtest.Const(200, devs), "GET /api/data-files": fwdtest.Const(200, []any{})}
	r, _ := mustRun(t, "inspect-collection-config", routes, `{"network_id":"n1","limit":2}`)
	sum := r.Evidence[0].Detail["summary"].(map[string]any)
	by := sum["by_type"].([]map[string]any)
	if len(r.Evidence[0].Detail["devices"].([]map[string]any)) != 2 || by[0]["name"] != "CISCO_IOS_XE" || by[0]["devices"] != 5 || sum["without_cli_credential"] != 2 || sum["snmp_collection_on"] != 2 {
		t.Errorf("a two-row page must still summarise all seven devices: %v / %v", sum, by)
	}
	if !strings.Contains(strings.Join(r.Limits, " | "), "view history") {
		t.Errorf("the limit must say what cannot be read and where the growth is: %v", r.Limits)
	}
}

func TestSnapshotsRetentionShowsThePolicyAndWhatTheNextCleanupWouldDelete(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/snapshotRetentionPolicy": fwdtest.Const(200, map[string]any{"enabled": true, "lastWeek": "ALL", "lastMonth": "ONE_PER_DAY", "lastQuarter": "ONE_PER_WEEK", "lastYear": "ONE_PER_MONTH", "older": "NONE"}),
		"POST /api/networks/n1/snapshotRetentionPolicy/trigger": fwdtest.Const(200, map[string]any{"count": 3, "deletedSnapshots": []any{
			map[string]any{"id": "s1", "createdAt": "2026-01-01T00:00:00Z"}, map[string]any{"id": "s2", "createdAt": "2026-01-02T00:00:00Z"}, map[string]any{"id": "s3", "createdAt": "2026-01-03T00:00:00Z"}}}),
	}
	r, srv := mustRun(t, "inspect-snapshots", routes, `{"network_id":"n1","retention":true,"limit":2}`)
	b := jsonOf(r)
	if r.Status != result.OK || !strings.Contains(r.Finding, "last month ONE_PER_DAY") || !strings.Contains(r.Finding, "would delete 3") || !strings.Contains(b, `"snapshot_id":"s2"`) || strings.Contains(b, `"snapshot_id":"s3"`) {
		t.Fatalf("%s %s", r.Finding, b)
	}
	for _, c := range srv.Calls() {
		if c.Method == "POST" && c.Query["dryRun"] != "true" {
			t.Errorf("the preview must be a dry run: %+v", c)
		}
	}
	if !strings.Contains(strings.Join(r.Limits, "|"), "2 of the 3 snapshots") {
		t.Errorf("a cut list says so: %v", r.Limits)
	}
	// the preview needs a permission the policy read does not: the policy is still answered
	routes["POST /api/networks/n1/snapshotRetentionPolicy/trigger"] = fwdtest.Const(403, map[string]any{"message": "Missing permission: NetworkOperation.DELETE_SNAPSHOT"})
	if r, _ := mustRun(t, "inspect-snapshots", routes, `{"network_id":"n1","retention":true}`); r.Status != result.OK || !strings.Contains(strings.Join(r.Limits, "|"), "DELETE_SNAPSHOT") {
		t.Errorf("a refused preview is a limit: %s %v", r.Status, r.Limits)
	}
	if _, _, err := runSkill(t, "inspect-snapshots", routes, `{"retention":true}`); err == nil {
		t.Errorf("retention needs a network")
	}
}

func TestChecksDetailReadsALoopViolationWhoseQueryIsAnObject(t *testing.T) {
	r, _ := mustRun(t, "inspect-checks", map[string]fwdtest.Handler{snapsPath: ready1(), "GET /api/snapshots/s1/checks/7": fwdtest.Const(200, map[string]any{
		"id": "7", "name": "No Forwarding Loop", "status": "FAIL", "numViolations": 1,
		"diagnosis": map[string]any{"details": []any{map[string]any{"query": map[string]any{"flowTypes": []string{"LOOP"}}}}}})}, `{"network_id":"n1","check_id":"7"}`)
	if r.Status != result.Failed || !strings.Contains(strings.Join(r.Limits, "|"), "violation kind(s) LOOP") || !strings.Contains(jsonOf(r), `"flowTypes":["LOOP"]`) {
		t.Fatalf("%s %v %s", r.Status, r.Limits, jsonOf(r))
	}
}

func TestChecksDetailShowsTheDefinitionAndTheViolatingRowsOfAFailingNQECheck(t *testing.T) {
	routes := map[string]fwdtest.Handler{snapsPath: ready1(),
		"GET /api/snapshots/s1/checks/9": fwdtest.Const(200, map[string]any{"id": "9", "name": "no telnet", "status": "FAIL", "numViolations": 2,
			"definition": map[string]any{"checkType": "NQE", "queryId": "Q_x"}}),
		nqePath: fwdtest.Const(200, map[string]any{"items": []any{map[string]any{"device": "r1", "line": "transport input telnet"}, map[string]any{"device": "r2", "line": "transport input telnet"}}, "totalNumItems": 2}),
	}
	r, _ := mustRun(t, "inspect-checks", routes, `{"network_id":"n1","check_id":"9"}`)
	b := jsonOf(r)
	if r.Status != result.Failed || !strings.Contains(b, `"violation_rows"`) || !strings.Contains(b, `"device":"r2"`) || !strings.Contains(b, `"queryId":"Q_x"`) || !strings.Contains(strings.Join(r.Limits, "|"), "rows the check's saved query returns") {
		t.Fatalf("%s %s", r.Status, b)
	}
	// a passing check runs no query
	routes["GET /api/snapshots/s1/checks/9"] = fwdtest.Const(200, map[string]any{"id": "9", "name": "no telnet", "status": "PASS", "definition": map[string]any{"checkType": "NQE", "queryId": "Q_x"}})
	if r, _ := mustRun(t, "inspect-checks", routes, `{"network_id":"n1","check_id":"9"}`); strings.Contains(jsonOf(r), "violation_rows") {
		t.Errorf("only a failing check lists violations")
	}
}

func cliProfileRoutes(assess fwdtest.Handler) map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/endpoints": fwdtest.Const(200, []any{
			map[string]any{"type": "CLI", "name": "e1", "host": "10.1.1.1", "profileId": "CLI-2"}}),
		"GET /api/endpoint-profiles": fwdtest.Const(200, map[string]any{"profiles": []any{
			map[string]any{"id": "CLI-2", "name": "cli-x", "type": "CLI", "customCommands": []string{"show version", "reload"}, "detectorCommand": "show hostname", "nameDetectorCommand": "show name"}}}),
		"GET /api/approved-cli-commands":  fwdtest.Const(200, map[string]any{"commands": []any{}}),
		"POST /api/approved-cli-commands": assess,
	}
}

func TestCollectionConfigSaysWhetherACLIProfileCollectsFromAllItsCommands(t *testing.T) {
	var asked []any
	r, _ := mustRun(t, "inspect-collection-config", cliProfileRoutes(func(_ *http.Request, body []byte) (int, any) {
		var in []string
		_ = json.Unmarshal(body, &in)
		for _, c := range in {
			asked = append(asked, c)
		}
		return 200, map[string]any{"approved": []string{"show version"}, "unapproved": []string{"reload"}}
	}), `{"network_id":"n1"}`)
	b := jsonOf(r)
	if len(asked) != 4 {
		t.Errorf("custom, detector and name detector commands are all assessed: %v", asked)
	}
	if !strings.Contains(b, `"collects":false`) || !strings.Contains(b, `"unapproved_commands":["reload"]`) {
		t.Fatalf("%s", b)
	}
	r, _ = mustRun(t, "inspect-collection-config", cliProfileRoutes(fwdtest.Const(200, map[string]any{"approved": []string{"show version", "reload", "show hostname", "show name"}, "unapproved": []string{}})), `{"network_id":"n1"}`)
	if !strings.Contains(jsonOf(r), `"collects":true`) {
		t.Fatalf("%s", jsonOf(r))
	}
}

func TestCollectionConfigRefusedAssessIsUnknownNotAPass(t *testing.T) {
	r, _ := mustRun(t, "inspect-collection-config", cliProfileRoutes(fwdtest.Const(403, map[string]any{"message": "forbidden"})), `{"network_id":"n1"}`)
	b := jsonOf(r)
	if !strings.Contains(b, `"collects":"unknown"`) || strings.Contains(b, `"collects":true`) || !strings.Contains(b, "manage-endpoint-profiles") {
		t.Fatalf("%s", b)
	}
}

func TestInspectPlatformCollectorsListWhichNetworksEachServes(t *testing.T) {
	r, _ := mustRun(t, "inspect-platform", map[string]fwdtest.Handler{
		"GET /api/collectors":            fwdtest.Const(200, []any{map[string]any{"id": "1", "name": "c1", "username": "collector-a"}, map[string]any{"id": "2", "name": "c2", "username": "collector-b"}}),
		"GET /api/networks":              fwdtest.Const(200, []any{map[string]any{"id": "n1", "name": "prod"}, map[string]any{"id": "n2", "name": "lab"}}),
		"GET /api/networks/n1/collector": fwdtest.Const(200, map[string]any{"id": "1", "username": "collector-a"}),
		"GET /api/networks/n2/collector": fwdtest.Const(200, map[string]any{}),
	}, `{"area":"collectors"}`)
	b := jsonOf(r)
	if r.Status != result.OK || !strings.Contains(b, `"n1 prod"`) || !strings.Contains(b, `"networks":[]`) {
		t.Fatalf("%s %s", r.Status, b)
	}
}

func TestCollectionConfigShowsEachDevicesCollectorAndCountsPerCollector(t *testing.T) {
	r, _ := mustRun(t, "inspect-collection-config", map[string]fwdtest.Handler{
		"GET /api/networks/n1/classic-devices": fwdtest.Const(200, []any{
			map[string]any{"name": "r1", "host": "10.0.0.1", "type": "CISCO_IOS", "collectorId": "C42"},
			map[string]any{"name": "r2", "host": "10.0.0.2", "type": "ARISTA_EOS", "collectorId": "C42"},
			map[string]any{"name": "r3", "host": "10.0.0.3", "type": "ARISTA_EOS"}}),
	}, `{"network_id":"n1"}`)
	b := jsonOf(r)
	if !strings.Contains(b, `"collector_id":"C42"`) || !strings.Contains(b, `"devices_by_collector":{"C42":2}`) {
		t.Errorf("each pinned device shows its collector and the pinned ones are counted per collector: %s", b)
	}
}
