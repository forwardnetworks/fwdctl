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
	if _, _, err := runSkill(t, "edit-source", routes, `{"network_id":"n1","object":"sase_setup","action":"test","definition":{"name":"zpa1","connectorId":"nope"}}`); err == nil {
		t.Errorf("an unknown connector must be refused")
	}
}

// SASE setup create, update and delete: dry runs send nothing; delete needs confirm = the setup's name; an update that replaces the connector list
// carries a staying connector's saved test result over; a create that Forward answers 400 (no online collector) but saved is read back as created.
func TestEditSourceSaseSetupCreateUpdateDelete(t *testing.T) {
	var created, patched string
	deleted := false
	exists := false
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/sase-setups/zpa1": func(*http.Request, []byte) (int, any) {
			if !exists || deleted {
				return 404, map[string]any{"message": "not found"}
			}
			return 200, map[string]any{"type": "ZSCALER", "name": "zpa1", "baseUrl": "https://zpa.example.test",
				"appConnectorSetups": []any{map[string]any{"connectorId": "c9", "name": "ac-9", "host": "192.0.2.9", "collectionType": "SSH", "testResult": map[string]any{"savedAt": "2026-11-03T00:00:00Z"}}}}
		},
		"POST /api/networks/n1/sase-setups": func(_ *http.Request, b []byte) (int, any) {
			created, exists = string(b), true
			return 400, map[string]any{"message": "no online collector"}
		},
		"PATCH /api/networks/n1/sase-setups/zpa1": func(_ *http.Request, b []byte) (int, any) {
			patched = string(b)
			return 200, map[string]any{"name": "zpa1"}
		},
		"DELETE /api/networks/n1/sase-setups/zpa1": func(*http.Request, []byte) (int, any) { deleted = true; return 204, nil },
	}
	create := `{"network_id":"n1","object":"sase_setup","action":"create","definition":{"name":"ZPA1","baseUrl":"https://zpa.example.test","httpCredId":"cred1"}`
	if r, _ := mustRun(t, "edit-source", routes, create+`}`); r.Mode != result.ModeDryRun || created != "" {
		t.Fatalf("create dry run: %s", jsonOf(r))
	}
	if r, _ := mustRun(t, "edit-source", routes, create+`,"apply":true}`); r.Status != result.OK || !strings.Contains(created, "zpa1") {
		t.Errorf("a create Forward saved but answered 400 for (no collector) is read back as created: %s %s", r.Status, r.Finding)
	}
	upd := `{"network_id":"n1","object":"sase_setup","action":"update","name":"zpa1","definition":{"appConnectorSetups":[{"connectorId":"c9","name":"ac-9","host":"192.0.2.9","collectionType":"SSH"}]}`
	if r, _ := mustRun(t, "edit-source", routes, upd+`}`); r.Mode != result.ModeDryRun || !strings.Contains(strings.Join(r.Limits, " "), "REPLACES the whole connector list") {
		t.Errorf("update dry run must say the connector list is replaced: %v", r.Limits)
	}
	mustRun(t, "edit-source", routes, upd+`,"apply":true}`)
	if !strings.Contains(patched, "2026-11-03") {
		t.Errorf("the staying connector's saved test result must be carried over: %s", patched)
	}
	del := `{"network_id":"n1","object":"sase_setup","action":"delete","name":"zpa1","apply":true`
	if _, _, err := runSkill(t, "edit-source", routes, del+`}`); deleted || err == nil || !strings.Contains(err.Error(), "confirm") {
		t.Errorf("delete without confirm must be refused and delete nothing: %v", err)
	}
	if r, _ := mustRun(t, "edit-source", routes, del+`,"confirm":"zpa1"}`); !deleted || r.Status != result.OK {
		t.Errorf("delete with confirm: %s %s", r.Status, r.Finding)
	}
}
