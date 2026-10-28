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
