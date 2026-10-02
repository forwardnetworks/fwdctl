package skills_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

func dataConnectorsWorld(conns *[]map[string]any) map[string]fwdtest.Handler {
	find := func(name string) map[string]any {
		for _, c := range *conns {
			if c["name"] == name {
				return c
			}
		}
		return nil
	}
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/data-connectors": func(*http.Request, []byte) (int, any) {
			return 200, map[string]any{"connectors": *conns}
		},
		"GET /api/networks/n1/data-connectors/weather-feed": func(*http.Request, []byte) (int, any) {
			if c := find("weather-feed"); c != nil {
				return 200, c
			}
			return 404, map[string]any{"message": "not found"}
		},
		"POST /api/networks/n1/data-connectors": func(_ *http.Request, body []byte) (int, any) {
			var req map[string]any
			_ = json.Unmarshal(body, &req)
			created := map[string]any{"name": req["name"], "baseUrl": req["baseUrl"], "endpoints": req["endpoints"]}
			*conns = append(*conns, created)
			return 201, created
		},
		"PATCH /api/networks/n1/data-connectors/weather-feed": func(_ *http.Request, body []byte) (int, any) {
			c := find("weather-feed")
			if c == nil {
				return 404, map[string]any{"message": "not found"}
			}
			var req map[string]any
			_ = json.Unmarshal(body, &req)
			for k, v := range req {
				c[k] = v
			}
			return 200, c
		},
		"DELETE /api/networks/n1/data-connectors/weather-feed": func(*http.Request, []byte) (int, any) {
			kept := (*conns)[:0]
			for _, c := range *conns {
				if c["name"] != "weather-feed" {
					kept = append(kept, c)
				}
			}
			*conns = kept
			return 204, nil
		},
		"POST /api/networks/n1/data-connectors/weather-feed": func(r *http.Request, _ []byte) (int, any) {
			if r.URL.Query().Get("action") != "test" {
				return 400, map[string]any{"message": "unexpected"}
			}
			return 200, map[string]any{"startedAt": "t0", "endedAt": "t1"}
		},
	}
}

func TestEditDataConnectorAddDryRunThenAppliesAndReadsBack(t *testing.T) {
	conns := []map[string]any{}
	in := `{"action":"add","network_id":"n1","name":"weather-feed","base_url":"https://example.test","endpoints":[{"name":"ep1","path":"/a"}]`
	r, srv := mustRun(t, "edit-data-connector", dataConnectorsWorld(&conns), in+`}`)
	if r.Status != result.OK || r.Mode != result.ModeDryRun || writes(srv) != 0 {
		t.Fatalf("%s %s writes=%d", r.Status, r.Finding, writes(srv))
	}
	r, _ = mustRun(t, "edit-data-connector", dataConnectorsWorld(&conns), in+`,"apply":true}`)
	if r.Status != result.OK || !r.Changes[0].Applied || len(conns) != 1 {
		t.Fatalf("%s %s conns=%v", r.Status, r.Finding, conns)
	}
	r, srv = mustRun(t, "edit-data-connector", dataConnectorsWorld(&conns), in+`,"apply":true}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "already exists") || writes(srv) != 0 {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
}

func TestEditDataConnectorUpdateIsTriStateOnRefs(t *testing.T) {
	conns := []map[string]any{{"name": "weather-feed", "baseUrl": "https://example.test", "endpoints": []any{map[string]any{"name": "ep1", "path": "/a"}}, "credentialId": "cred1"}}
	world := dataConnectorsWorld(&conns)
	// omitting refs leaves credentialId alone
	r, _ := mustRun(t, "edit-data-connector", world, `{"action":"update","network_id":"n1","name":"weather-feed","collect":false,"apply":true}`)
	if r.Status != result.OK || conns[0]["credentialId"] != "cred1" {
		t.Fatalf("%s %s conns=%v", r.Status, r.Finding, conns)
	}
	// an empty refs.credential_id clears it
	r, _ = mustRun(t, "edit-data-connector", world, `{"action":"update","network_id":"n1","name":"weather-feed","refs":{"credential_id":""},"apply":true}`)
	if r.Status != result.OK || conns[0]["credentialId"] != nil {
		t.Fatalf("%s %s conns=%v", r.Status, r.Finding, conns)
	}
	// a non-empty refs.credential_id sets it
	r, _ = mustRun(t, "edit-data-connector", world, `{"action":"update","network_id":"n1","name":"weather-feed","refs":{"credential_id":"cred2"},"apply":true}`)
	if r.Status != result.OK || conns[0]["credentialId"] != "cred2" {
		t.Fatalf("%s %s conns=%v", r.Status, r.Finding, conns)
	}
}

func TestEditDataConnectorDeleteNeedsConfirmAndTestReportsFailureAsFailedNotError(t *testing.T) {
	conns := []map[string]any{{"name": "weather-feed", "baseUrl": "https://example.test", "endpoints": []any{map[string]any{"name": "ep1", "path": "/a"}}}}
	world := dataConnectorsWorld(&conns)
	r, srv := mustRun(t, "edit-data-connector", world, `{"action":"delete","network_id":"n1","name":"weather-feed","apply":true}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "confirm") || writes(srv) != 0 {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	r, _ = mustRun(t, "edit-data-connector", world, `{"action":"delete","network_id":"n1","name":"weather-feed","confirm":"weather-feed","apply":true}`)
	if r.Status != result.OK || !r.Changes[0].Applied || len(conns) != 0 {
		t.Fatalf("%s %s conns=%v", r.Status, r.Finding, conns)
	}
	// deleting again is a no-op, not an error
	r, _ = mustRun(t, "edit-data-connector", world, `{"action":"delete","network_id":"n1","name":"weather-feed","confirm":"weather-feed","apply":true}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "already gone") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
}

func TestEditDataConnectorTestIsReadOnlyAndRefusesApply(t *testing.T) {
	conns := []map[string]any{{"name": "weather-feed", "baseUrl": "https://example.test", "endpoints": []any{map[string]any{"name": "ep1", "path": "/a"}}}}
	world := dataConnectorsWorld(&conns)
	r, _ := mustRun(t, "edit-data-connector", world, `{"action":"test","network_id":"n1","name":"weather-feed"}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "passed") || !strings.Contains(r.Finding, "stored test result was updated") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	_, _, err := runSkill(t, "edit-data-connector", world, `{"action":"test","network_id":"n1","name":"weather-feed","apply":true}`)
	if err == nil || !strings.Contains(err.Error(), "apply") {
		t.Fatalf("want an apply-not-allowed error, got %v", err)
	}
}

func TestEditDataConnectorRefusesCredentialHeadersAtPlanTimeAndAllowsPlainOnes(t *testing.T) {
	conns := []map[string]any{}
	base := `{"action":"add","network_id":"n1","name":"weather-feed","base_url":"https://example.test","endpoints":[{"name":"ep1","path":"/a"}],"extra_headers":`
	for _, h := range []string{`{"Authorization":"Bearer x"}`, `{"x-api-key":"k"}`, `{"Cookie":"a=b"}`, `{"X-Auth-Token":"t"}`, `{"Proxy-Authorization":"p"}`} {
		if _, srv, err := runSkill(t, "edit-data-connector", dataConnectorsWorld(&conns), base+h+`}`); err == nil || !strings.Contains(err.Error(), "credential_id") || writes(srv) != 0 {
			t.Errorf("%s must be refused at plan time: %v", h, err)
		}
	}
	if r, _ := mustRun(t, "edit-data-connector", dataConnectorsWorld(&conns), base+`{"Accept":"application/json"}}`); r.Status != result.OK {
		t.Errorf("a plain header is allowed: %s %s", r.Status, r.Finding)
	}
}
