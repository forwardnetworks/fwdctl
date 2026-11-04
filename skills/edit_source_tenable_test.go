package skills_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

func tenableRoutes(sent *string, listed *[]any) map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/end-host-scanners": func(*http.Request, []byte) (int, any) { return 200, map[string]any{"sources": *listed} },
		"POST /api/networks/n1/end-host-scanners": func(r *http.Request, b []byte) (int, any) {
			*sent = string(b)
			return 201, map[string]any{}
		},
	}
}

// A create is a dry run until apply, names the credential by id only, and refuses a second source of the same name.
func TestTenableSourceCreateIsADryRunAndTakesNoSecret(t *testing.T) {
	var sent string
	listed := []any{}
	in := `{"network_id":"n1","object":"tenable_source","action":"create","definition":{"name":"nuclei","baseUrl":"https://tenable.example.invalid","credentialId":"H-5"}`
	r, srv := mustRun(t, "edit-source", tenableRoutes(&sent, &listed), in+`}`)
	if r.Mode != result.ModeDryRun || sent != "" || writes(srv) != 0 {
		t.Fatalf("a dry run must write nothing: %s %q", r.Mode, sent)
	}
	if _, _, err := runSkill(t, "edit-source", tenableRoutes(&sent, &listed), `{"network_id":"n1","object":"tenable_source","action":"create","secret_file":"/x","definition":{"name":"a","baseUrl":"u","credentialId":"H-5"}}`); err == nil {
		t.Errorf("a secret input must be refused: the credential is named by id")
	}
	if _, _, err := runSkill(t, "edit-source", tenableRoutes(&sent, &listed), `{"network_id":"n1","object":"tenable_source","action":"create","definition":{"name":"a"}}`); err == nil {
		t.Errorf("create without baseUrl and credentialId must be refused")
	}
	if !strings.Contains(jsonOf(r), "H-5") {
		t.Errorf("the plan must show the credential id: %s", jsonOf(r))
	}
}

// Update changes only what is given and needs something to change.
func TestTenableSourceUpdateIsAPatchAndNeedsAChange(t *testing.T) {
	var sent string
	listed := []any{map[string]any{"name": "nuclei", "type": "TENABLE", "baseUrl": "https://tenable.example.invalid", "credentialId": "H-5"}}
	if _, _, err := runSkill(t, "edit-source", tenableRoutes(&sent, &listed), `{"network_id":"n1","object":"tenable_source","action":"update","name":"nuclei","definition":{}}`); err == nil {
		t.Errorf("an empty update must be refused")
	}
	r, _ := mustRun(t, "edit-source", tenableRoutes(&sent, &listed), `{"network_id":"n1","object":"tenable_source","action":"update","name":"nuclei","definition":{"collectionDisabled":true}}`)
	if r.Mode != result.ModeDryRun || !strings.Contains(jsonOf(r), "only the fields given change") {
		t.Errorf("update must be a dry run that says it patches: %s", jsonOf(r))
	}
}

// A SASE setup test queues a collector task: the dry run sends nothing, apply posts the test, and the result says to read it back later.
func TestEditSourceSaseSetupTestQueuesAndSaysWhereTheResultLands(t *testing.T) {
	posted := ""
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/sase-setups/zpa1": fwdtest.Const(200, map[string]any{"type": "ZSCALER", "name": "zpa1", "baseUrl": "https://zpa.example.test",
			"appConnectorSetups": []any{map[string]any{"connectorId": "c9", "name": "ac-9", "host": "192.0.2.9", "collectionType": "SSH"}}, "testResult": map[string]any{"savedAt": "2026-11-03T00:00:00Z"}}),
		"POST /api/networks/n1/sase-setups/zpa1":                   func(r *http.Request, _ []byte) (int, any) { posted = r.URL.RawQuery; return 204, nil },
		"POST /api/networks/n1/sase-setups/zpa1/app-connectors/c9": func(r *http.Request, _ []byte) (int, any) { posted = "c9 " + r.URL.RawQuery; return 204, nil },
	}
	in := `{"network_id":"n1","object":"sase_setup","action":"test","definition":{"name":"zpa1"}`
	r, _ := mustRun(t, "edit-source", routes, in+`}`)
	if r.Mode != result.ModeDryRun || posted != "" || !strings.Contains(jsonOf(r), "2026-11-03") {
		t.Fatalf("dry run: %s posted=%q", jsonOf(r), posted)
	}
	r, _ = mustRun(t, "edit-source", routes, in+`,"apply":true}`)
	if r.Status == result.Error || posted == "" || !strings.Contains(strings.Join(r.Limits, " "), "inspect-platform area sase_setups") {
		t.Errorf("apply queues the test and says where the result lands: %s posted=%q %v", r.Status, posted, r.Limits)
	}
	if _, _, err := runSkill(t, "edit-source", routes, `{"network_id":"n1","object":"sase_setup","action":"delete","definition":{"name":"zpa1"}}`); err == nil {
		t.Errorf("delete is not offered for a SASE setup")
	}
	if _, _, err := runSkill(t, "edit-source", routes, `{"network_id":"n1","object":"sase_setup","action":"test","definition":{"name":"zpa1","connectorId":"nope"}}`); err == nil {
		t.Errorf("an unknown connector must be refused")
	}
}
