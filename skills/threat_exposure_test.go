package skills_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

func teRoutes() map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		snapsPath: ready("s1"),
		"GET /api/networks/n1/threat-exposure-definitions": fwdtest.Const(200, map[string]any{"definitions": []any{map[string]any{"definition": map[string]any{"id": "d1", "name": "internet to crown jewels", "maxDurationSeconds": 600,
			"exposurePoints": []any{map[string]any{"exposurePoint": "INTERNET", "level": "HIGH"}}, "criticalities": []any{"CROWN_JEWEL"}}}}}),
		"GET /api/networks/n1/threat-exposure-definitions/d1": fwdtest.Const(200, map[string]any{"id": "d1", "name": "internet to crown jewels", "maxDurationSeconds": 600}),
		"GET /api/networks/n1/threat-exposure-analyses/d1": func(r *http.Request, _ []byte) (int, any) {
			switch r.URL.Query().Get("view") {
			case "status":
				return 200, map[string]any{"state": "SUCCEEDED", "progress": 100}
			case "criticalAssetDirectExposureDetails":
				return 200, map[string]any{"assetId": r.URL.Query().Get("assetId"), "paths": []any{"p1"}}
			}
			return 404, map[string]any{"message": "no such view"}
		},
		"POST /api/networks/n1/threat-exposure-analyses/d1": func(r *http.Request, _ []byte) (int, any) {
			switch r.URL.Query().Get("view") {
			case "criticalAssetsExposureSummary":
				return 200, map[string]any{"assets": []any{map[string]any{"assetId": 77, "name": "db01", "exposed": true}}, "total": 1}
			case "criticalAssetTransitiveExposureDetails":
				return 200, map[string]any{"hosts": []any{"app01"}}
			}
			return 404, map[string]any{"message": "no such view"}
		},
		"GET /api/networks/n1/edge-assets": fwdtest.Const(200, map[string]any{"edgeAssets": []any{map[string]any{"name": "vpn-gw", "include": true, "criticality": "CRITICAL_INFRA", "ips": []any{"192.0.2.9"}}}}),
	}
}

func teRun(t *testing.T, routes map[string]fwdtest.Handler, opt string) result.Result {
	t.Helper()
	r, _ := mustRun(t, "inspect-vulnerabilities", routes, `{"network_id":"n1","snapshot_id":"s1","threat_exposure_experimental":`+opt+`}`)
	return r
}

// Threat exposure is EXPERIMENTAL and labelled so in every answer: the evidence says experimental, the limits name the org property that enables it and that older builds do not serve the routes, and nothing here writes.
func TestThreatExposureReadsAreLabelledExperimental(t *testing.T) {
	for _, tc := range []struct{ opt, want string }{
		{`{"view":"definitions"}`, "internet to crown jewels"},
		{`{"view":"definition","definition_id":"d1"}`, "maxDurationSeconds"},
		{`{"view":"status","definition_id":"d1"}`, "SUCCEEDED"},
		{`{"view":"critical_assets","definition_id":"d1","limit":10}`, "db01"},
		{`{"view":"direct","definition_id":"d1","asset_id":77}`, `"assetId":"77"`},
		{`{"view":"transitive","definition_id":"d1","asset_id":77}`, "app01"},
		{`{"view":"edge_assets"}`, "vpn-gw"},
	} {
		r := teRun(t, teRoutes(), tc.opt)
		b, l := jsonOf(r), strings.Join(r.Limits, " ")
		if r.Status != result.OK || !strings.Contains(b, tc.want) {
			t.Errorf("%s: %s %s", tc.opt, r.Status, b)
		}
		for _, want := range []string{"EXPERIMENTAL", "THREAT_EXPOSURE", "404"} {
			if !strings.Contains(l, want) {
				t.Errorf("%s: limits miss %q: %s", tc.opt, want, l)
			}
		}
		if !strings.Contains(b, `"experimental":true`) {
			t.Errorf("%s: evidence is not labelled experimental: %s", tc.opt, b)
		}
	}
}

// A deployment that does not serve the routes (404) is unknown with the reason, not an error; a missing id is refused by name.
func TestThreatExposureOnAnOlderBuildAndBadInput(t *testing.T) {
	routes := teRoutes()
	routes["GET /api/networks/n1/threat-exposure-definitions"] = fwdtest.Const(404, map[string]any{"message": "No endpoint GET /api/networks/n1/threat-exposure-definitions."})
	r := teRun(t, routes, `{"view":"definitions"}`)
	if r.Status != result.Unknown || !strings.Contains(r.Finding+strings.Join(r.Limits, " "), "does not serve") {
		t.Errorf("404: %s %s %v", r.Status, r.Finding, r.Limits)
	}
	for _, bad := range []string{`{"view":"status"}`, `{"view":"direct","definition_id":"d1"}`, `{"view":"poem"}`} {
		if _, _, err := runSkill(t, "inspect-vulnerabilities", teRoutes(), `{"network_id":"n1","snapshot_id":"s1","threat_exposure_experimental":`+bad+`}`); err == nil {
			t.Errorf("should be refused: %s", bad)
		}
	}
}

// Forward copies edge assets into a snapshot only when the snapshot is CREATED; a reprocess re-reads what was stored then. An included asset listed with includedInLatestSnapshot false is invisible to threat
// exposure on the latest snapshot, and the answer says so with the remedy (a new snapshot; reprocess does not add them).
func TestEdgeAssetsViewCountsTheOnesNotInTheLatestSnapshotAndGivesTheRemedy(t *testing.T) {
	routes := teRoutes()
	routes["GET /api/networks/n1/edge-assets"] = fwdtest.Const(200, map[string]any{"edgeAssets": []any{
		map[string]any{"name": "old-gw", "include": true, "criticality": "CRITICAL_INFRA", "includedInLatestSnapshot": true},
		map[string]any{"name": "new-db", "include": true, "criticality": "CROWN_JEWEL", "includedInLatestSnapshot": false},
		map[string]any{"name": "new-app", "include": true, "includedInLatestSnapshot": false},
		map[string]any{"name": "off", "include": false, "criticality": "CROWN_JEWEL", "includedInLatestSnapshot": false},
	}})
	r := teRun(t, routes, `{"view":"edge_assets"}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "4 edge asset(s)") || !strings.Contains(r.Finding, "2 of them (1 with a criticality) are NOT in the latest snapshot") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	d := jsonOf(r)
	if !strings.Contains(d, `"edge_assets_not_in_latest_snapshot":2`) || !strings.Contains(d, `"edge_assets_with_criticality":3`) || !strings.Contains(d, "new-db") || !strings.Contains(d, "new-app") {
		t.Errorf("counts and names: %s", d)
	}
	if strings.Contains(strings.Join(r.Evidence[0].Detail["edge_assets_not_in_latest_snapshot_names"].([]string), ","), "off") {
		t.Errorf("an asset that is not included is never copied into a snapshot and is not the finding")
	}
	l := strings.Join(r.Limits, " ")
	if !strings.Contains(l, "reprocessing does NOT add them") || !strings.Contains(l, "Create a NEW snapshot") || !strings.Contains(l, "not verified") {
		t.Errorf("the remedy and what is unverified are said: %v", r.Limits)
	}
	// all in the latest snapshot: no finding about it
	routes["GET /api/networks/n1/edge-assets"] = fwdtest.Const(200, map[string]any{"edgeAssets": []any{map[string]any{"name": "a", "include": true, "includedInLatestSnapshot": true}}})
	r = teRun(t, routes, `{"view":"edge_assets"}`)
	if strings.Contains(r.Finding, "NOT in the latest snapshot") || strings.Contains(strings.Join(r.Limits, " "), "reprocessing does NOT add them") {
		t.Errorf("nothing to say when every asset is in: %s", r.Finding)
	}
}

func TestEdgeAssetsViewNamesNoIncludedAssetsAndCapsTheListedNames(t *testing.T) {
	routes := teRoutes()
	var assets []any
	for i := 0; i < 60; i++ {
		assets = append(assets, map[string]any{"name": fmt.Sprintf("a%02d", i), "include": true, "includedInLatestSnapshot": false})
	}
	routes["GET /api/networks/n1/edge-assets"] = fwdtest.Const(200, map[string]any{"edgeAssets": assets})
	r := teRun(t, routes, `{"view":"edge_assets"}`)
	if len(r.Omitted) == 0 || !strings.Contains(jsonOf(r), `"edge_assets_not_in_latest_snapshot":60`) {
		t.Errorf("60 not included, 50 names shown, the cut declared: %v", r.Omitted)
	}
	routes["GET /api/networks/n1/edge-assets"] = fwdtest.Const(200, map[string]any{"edgeAssets": []any{map[string]any{"name": "x", "include": false, "includedInLatestSnapshot": false}}})
	r = teRun(t, routes, `{"view":"edge_assets"}`)
	if !strings.Contains(strings.Join(r.Limits, " "), "no edge asset is marked include") {
		t.Errorf("limits: %v", r.Limits)
	}
}

func TestThreatExposureResolveShowsWhatADefinitionSelectsAndWritesNothing(t *testing.T) {
	routes := teRoutes()
	var posted string
	routes["GET /api/networks/n1/threat-exposure-definitions/d1"] = fwdtest.Const(200, map[string]any{"id": "d1", "name": "internet to crown jewels", "maxDurationSeconds": 600,
		"exposurePoints": []any{map[string]any{"exposurePoint": "INTERNET", "level": "HIGH"}}, "criticalities": []any{"CROWN_JEWEL"}})
	routes["POST /api/networks/n1/threat-exposure-definitions"] = func(r *http.Request, b []byte) (int, any) {
		posted = r.URL.RawQuery + " " + string(b)
		return 200, map[string]any{"rows": []any{map[string]any{"asset": "db01"}}, "assets": 1}
	}
	r := teRun(t, routes, `{"view":"resolve","definition_id":"d1"}`)
	if r.Status != result.OK || !strings.Contains(jsonOf(r), "db01") || !strings.Contains(posted, "action=resolve") || !strings.Contains(posted, "snapshotId=s1") || !strings.Contains(posted, "CROWN_JEWEL") || !strings.Contains(posted, "internet to crown jewels") {
		t.Fatalf("%s %s posted=%s", r.Status, r.Finding, posted)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "writes nothing") || !strings.Contains(strings.Join(r.Limits, " "), "not among them") {
		t.Errorf("limits: %v", r.Limits)
	}
	if _, _, err := runSkill(t, "inspect-vulnerabilities", routes, `{"network_id":"n1","snapshot_id":"s1","threat_exposure_experimental":{"view":"resolve"}}`); err == nil {
		t.Errorf("resolve needs a definition_id")
	}
}

func TestEdgeAssetsViewWarnsWhenTwoIncludedAssetsShareAMAC(t *testing.T) {
	routes := teRoutes()
	routes["GET /api/networks/n1/edge-assets"] = fwdtest.Const(200, map[string]any{"edgeAssets": []any{
		map[string]any{"name": "a", "include": true, "macAddress": "02:00:00:00:00:01", "includedInLatestSnapshot": true},
		map[string]any{"name": "b", "include": true, "macAddress": "02:00:00:00:00:01", "includedInLatestSnapshot": true},
		map[string]any{"name": "c", "include": true, "macAddress": "02:00:00:00:00:02", "includedInLatestSnapshot": true},
		map[string]any{"name": "e", "include": true, "macAddress": "02:00:00:00:00:AA", "includedInLatestSnapshot": true},
		map[string]any{"name": "f", "include": true, "macAddress": "02:00:00:00:00:aa", "includedInLatestSnapshot": true},
		map[string]any{"name": "d", "include": false, "macAddress": "02:00:00:00:00:02", "includedInLatestSnapshot": false},
	}})
	r := teRun(t, routes, `{"view":"edge_assets"}`)
	d := jsonOf(r)
	if !strings.Contains(d, `"edge_assets_sharing_a_mac"`) || !strings.Contains(d, "02:00:00:00:00:01") || strings.Contains(d, `"mac":"02:00:00:00:00:02"`) || !strings.Contains(d, `"mac":"02:00:00:00:00:aa"`) || !strings.Contains(strings.Join(r.Limits, " "), "Forward maps NEITHER asset") {
		t.Errorf("only an included pair that shares a MAC: %s %v", d, r.Limits)
	}
}
