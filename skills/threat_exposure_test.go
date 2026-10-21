package skills_test

import (
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
