package skills_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

// writeTE records the request bodies of the threat exposure writes so a test can see what was sent.
type teWrites struct{ posts, patches, deletes, evals []string }

func teWriteRoutes(w *teWrites, listed bool) map[string]fwdtest.Handler {
	routes := teRoutes()
	defs := map[string]any{"definitions": []any{}}
	if listed {
		defs = map[string]any{"definitions": []any{map[string]any{"definition": map[string]any{"id": "d1", "name": "internet to crown jewels", "maxDurationSeconds": 600,
			"exposurePoints": []any{map[string]any{"exposurePoint": "INTERNET", "level": "HIGH"}}, "criticalities": []any{"CROWN_JEWEL"}}}}}
	}
	routes["GET /api/networks/n1/threat-exposure-definitions"] = func(*http.Request, []byte) (int, any) {
		if len(w.posts) > 0 { // after the create, the definition is listed
			return 200, map[string]any{"definitions": []any{map[string]any{"definition": map[string]any{"id": "d9", "name": "dmz to crown jewels", "maxDurationSeconds": 600,
				"exposurePoints": []any{map[string]any{"exposurePoint": "INTERNET", "level": "HIGH"}}}}}}
		}
		return 200, defs
	}
	routes["POST /api/networks/n1/threat-exposure-definitions"] = func(_ *http.Request, b []byte) (int, any) {
		w.posts = append(w.posts, string(b))
		return 201, map[string]any{"id": "d9", "name": "dmz to crown jewels", "maxDurationSeconds": 600}
	}
	routes["PATCH /api/networks/n1/threat-exposure-definitions/d1"] = func(_ *http.Request, b []byte) (int, any) {
		w.patches = append(w.patches, string(b))
		return 204, nil
	}
	routes["DELETE /api/networks/n1/threat-exposure-definitions/d1"] = func(*http.Request, []byte) (int, any) {
		w.deletes = append(w.deletes, "d1")
		return 204, nil
	}
	routes["POST /api/networks/n1/threat-exposure-analyses/d1"] = func(r *http.Request, _ []byte) (int, any) {
		if r.URL.Query().Get("action") == "evaluate" {
			w.evals = append(w.evals, r.URL.Query().Get("snapshotId"))
			return 202, nil
		}
		return 404, map[string]any{"message": "no such view"}
	}
	routes["PATCH /api/networks/n1/assets"] = func(_ *http.Request, b []byte) (int, any) {
		w.patches = append(w.patches, string(b))
		return 204, nil
	}
	return routes
}

func neRun(t *testing.T, routes map[string]fwdtest.Handler, in string) (result.Result, *fwdtest.Server) {
	t.Helper()
	return mustRun(t, "edit-network", routes, in)
}

// Threat exposure definitions are managed through edit-network (object threat_exposure): dry run by default, labelled EXPERIMENTAL, validated before any request, and read back after apply.
func TestEditNetworkThreatExposureCreateIsADryRunThenApplies(t *testing.T) {
	w := &teWrites{}
	body := `{"network_id":"n1","object":"threat_exposure","action":"create","name":"dmz to crown jewels","definition":{"exposure_points":[{"exposure_point":"INTERNET","level":"HIGH"}],"criticalities":["CROWN_JEWEL"],"max_duration_seconds":600}`
	r, srv := neRun(t, teWriteRoutes(w, false), body+`}`)
	if r.Mode != result.ModeDryRun || writes(srv) != 0 || len(w.posts) != 0 {
		t.Fatalf("dry run writes nothing: %s %v", r.Finding, w.posts)
	}
	for _, want := range []string{"EXPERIMENTAL", "THREAT_EXPOSURE"} {
		if !strings.Contains(strings.Join(r.Limits, " "), want) {
			t.Errorf("limits miss %q: %v", want, r.Limits)
		}
	}
	r, _ = neRun(t, teWriteRoutes(w, false), body+`,"apply":true}`)
	if r.Status != result.OK || len(w.posts) != 1 || !strings.Contains(w.posts[0], `"name":"dmz to crown jewels"`) || !strings.Contains(w.posts[0], `"maxDurationSeconds":600`) || !strings.Contains(w.posts[0], `"exposurePoint":"INTERNET"`) {
		t.Fatalf("apply: %s %v", r.Finding, w.posts)
	}
	for _, bad := range []string{
		`{"network_id":"n1","object":"threat_exposure","action":"create","name":"x","definition":{"exposure_points":[{"exposure_point":"INTERNET","level":"LOUD"}],"max_duration_seconds":60}}`,
		`{"network_id":"n1","object":"threat_exposure","action":"create","name":"x","definition":{"exposure_points":[{"exposure_point":"INTERNET","level":"HIGH"}]}}`,
		`{"network_id":"n1","object":"threat_exposure","action":"create","definition":{"exposure_points":[{"exposure_point":"INTERNET","level":"HIGH"}],"max_duration_seconds":60}}`,
	} {
		if _, srv2, err := runSkill(t, "edit-network", teWriteRoutes(&teWrites{}, false), bad); err == nil || writes(srv2) != 0 {
			t.Errorf("a definition Forward would refuse is refused before any request: %s", bad)
		}
	}
}

func TestEditNetworkThreatExposureUpdateByNameAndDeleteNeedsConfirm(t *testing.T) {
	w := &teWrites{}
	routes := teWriteRoutes(w, true)
	upd := `{"network_id":"n1","object":"threat_exposure","action":"update","name":"internet to crown jewels","definition":{"max_duration_seconds":900,"criticalities":[]}`
	if r, _ := neRun(t, routes, upd+`}`); r.Mode != result.ModeDryRun || len(w.patches) != 0 {
		t.Fatalf("dry run: %v", w.patches)
	}
	neRun(t, routes, upd+`,"apply":true}`)
	if len(w.patches) != 1 || !strings.Contains(w.patches[0], `"maxDurationSeconds":900`) || !strings.Contains(w.patches[0], `"criticalities":[]`) {
		t.Errorf("a patch keeps an empty list distinct from an absent one: %v", w.patches)
	}
	del := `{"network_id":"n1","object":"threat_exposure","action":"delete","name":"internet to crown jewels"`
	if _, _, err := runSkill(t, "edit-network", routes, del+`,"apply":true}`); err == nil || len(w.deletes) != 0 {
		t.Errorf("delete cannot be undone and needs confirm: %v", err)
	}
	neRun(t, routes, del+`,"apply":true,"confirm":"internet to crown jewels"}`)
	if len(w.deletes) != 1 {
		t.Errorf("delete with confirm: %v", w.deletes)
	}
	if r, _ := neRun(t, routes, `{"network_id":"n1","object":"threat_exposure","action":"update","name":"no such","definition":{"max_duration_seconds":9}}`); r.Status != result.Unknown {
		t.Errorf("a name that matches nothing is unknown, not a guess: %s", r.Status)
	}
}

func TestEditNetworkThreatExposureEvaluateStartsAnAnalysisOnASnapshot(t *testing.T) {
	w := &teWrites{}
	routes := teWriteRoutes(w, true)
	ev := `{"network_id":"n1","object":"threat_exposure","action":"evaluate","name":"internet to crown jewels","definition":{"snapshot_id":"s1"}`
	if r, _ := neRun(t, routes, ev+`}`); r.Mode != result.ModeDryRun || len(w.evals) != 0 {
		t.Fatalf("dry run starts nothing: %v", w.evals)
	}
	r, _ := neRun(t, routes, ev+`,"apply":true}`)
	if len(w.evals) != 1 || w.evals[0] != "s1" || !strings.Contains(strings.Join(r.Limits, " "), "inspect-vulnerabilities") {
		t.Errorf("evaluate: %v %v", w.evals, r.Limits)
	}
	if _, _, err := runSkill(t, "edit-network", routes, `{"network_id":"n1","object":"threat_exposure","action":"evaluate","name":"internet to crown jewels"}`); err == nil {
		t.Errorf("an evaluation needs a snapshot_id")
	}
}

func TestEditNetworkAssetCriticalityAssignAndRemove(t *testing.T) {
	w := &teWrites{}
	routes := teWriteRoutes(w, true)
	set := `{"network_id":"n1","object":"asset_criticality","action":"assign","definition":{"names":["vpn-gw","db01"],"level":"CROWN_JEWEL"}`
	if r, _ := neRun(t, routes, set+`}`); r.Mode != result.ModeDryRun || len(w.patches) != 0 {
		t.Fatalf("dry run: %v", w.patches)
	}
	neRun(t, routes, set+`,"apply":true}`)
	if len(w.patches) != 1 || !strings.Contains(w.patches[0], `"names":["vpn-gw","db01"]`) || !strings.Contains(w.patches[0], `"criticality":"CROWN_JEWEL"`) {
		t.Errorf("assign: %v", w.patches)
	}
	w.patches = nil
	neRun(t, routes, `{"network_id":"n1","object":"asset_criticality","action":"delete","definition":{"names":["vpn-gw"]},"apply":true,"confirm":"vpn-gw"}`)
	if len(w.patches) != 1 || !strings.Contains(w.patches[0], `"criticality":null`) {
		t.Errorf("remove sends a null criticality: %v", w.patches)
	}
	if _, _, err := runSkill(t, "edit-network", routes, `{"network_id":"n1","object":"asset_criticality","action":"assign","definition":{"names":["x"],"level":"MEDIUM"}}`); err == nil {
		t.Errorf("an unknown criticality is refused")
	}
}

// A 500 on the analysis calls means the build lacks the analysis service: the evaluate error and the status read say so, and say not to retry.
func TestThreatExposureAnalysis500IsNamedAsABuildLimit(t *testing.T) {
	w := &teWrites{}
	routes := teWriteRoutes(w, true)
	routes["POST /api/networks/n1/threat-exposure-analyses/d1"] = fwdtest.Const(500, map[string]any{"message": "Please send logs to Forward for diagnosis."})
	routes["GET /api/networks/n1/threat-exposure-analyses/d1"] = fwdtest.Const(500, map[string]any{"message": "Please send logs to Forward for diagnosis."})
	_, _, err := runSkill(t, "edit-network", routes, `{"network_id":"n1","object":"threat_exposure","action":"evaluate","name":"internet to crown jewels","definition":{"snapshot_id":"s1"},"apply":true}`)
	if err == nil || !strings.Contains(err.Error(), "analysis service is not available") {
		t.Errorf("evaluate 500: %v", err)
	}
	r := teRun(t, routes, `{"view":"status","definition_id":"d1"}`)
	if r.Status != result.Unknown || !strings.Contains(strings.Join(r.Limits, " "), "analysis service is not available") {
		t.Errorf("status 500: %s %v", r.Status, r.Limits)
	}
}
