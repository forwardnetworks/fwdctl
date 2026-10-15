package skills_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

// A network endpoint is a non-network host Forward collects (a Linux host over SSH, an SNMP or HTTP device). edit-source creates, updates and deletes them; a create is idempotent by name (the same
// definition again is "already as asked", a different one is refused), a dry run writes nothing, and what Forward would reject is refused first.
func endpointRoutes(posted *[]map[string]any, eps *[]any) map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/endpoints": func(*http.Request, []byte) (int, any) { return 200, *eps },
		"POST /api/networks/n1/endpoints": func(r *http.Request, b []byte) (int, any) {
			var bodies []map[string]any
			_ = json.Unmarshal(b, &bodies)
			for _, x := range bodies {
				*posted = append(*posted, x)
				*eps = append(*eps, map[string]any{"type": r.URL.Query().Get("type"), "name": x["name"], "host": x["host"], "credentialId": x["credentialId"], "profileId": x["profileId"]})
			}
			return 200, map[string]any{}
		},
		"DELETE /api/networks/n1/endpoints/h1": func(*http.Request, []byte) (int, any) { *eps = nil; return 204, nil },
	}
}

func TestEditSourceEndpointCreateIsDryRunFirstThenSendsTheCLIFields(t *testing.T) {
	var posted []map[string]any
	var eps []any
	in := `{"network_id":"n1","object":"endpoint","action":"create","definition":{"type":"cli","name":"h1","host":"10.0.0.5","credentialId":"L-9","profileId":"CLI-3","protocol":"SSH","collect":true}`
	r, srv := mustRun(t, "edit-source", endpointRoutes(&posted, &eps), in+`}`)
	if writes(srv) != 0 || len(posted) != 0 || r.Changes[0].Applied {
		t.Fatalf("a dry run wrote: %v", posted)
	}
	r, _ = mustRun(t, "edit-source", endpointRoutes(&posted, &eps), in+`,"apply":true}`)
	if r.Status != result.OK || len(posted) != 1 || posted[0]["name"] != "h1" || posted[0]["credentialId"] != "L-9" || posted[0]["profileId"] != "CLI-3" || posted[0]["protocol"] != "SSH" {
		t.Fatalf("%s %s %v", r.Status, r.Finding, posted)
	}
	// the same definition again changes nothing
	n := len(posted)
	r, _ = mustRun(t, "edit-source", endpointRoutes(&posted, &eps), in+`,"apply":true}`)
	if len(posted) != n || !strings.Contains(strings.ToLower(r.Finding), "already") {
		t.Errorf("an unchanged create should be a no-op: %s (posted %d)", r.Finding, len(posted))
	}
}

func TestEditSourceEndpointRefusesWhatForwardWouldRejectAndDeleteNeedsConfirm(t *testing.T) {
	var posted []map[string]any
	eps := []any{map[string]any{"type": "CLI", "name": "h1", "host": "10.0.0.5", "credentialId": "L-9", "profileId": "CLI-3"}}
	for name, def := range map[string]string{
		"no host":                   `{"type":"cli","name":"h2"}`,
		"unknown type":              `{"type":"ftp","name":"h2","host":"h"}`,
		"protocol on snmp":          `{"type":"snmp","name":"h2","host":"h","protocol":"SSH"}`,
		"different host, same name": `{"type":"cli","name":"h1","host":"10.9.9.9","credentialId":"L-9","profileId":"CLI-3"}`,
		"bad protocol":              `{"type":"cli","name":"h2","host":"h","protocol":"RSH"}`,
	} {
		if _, _, err := runSkill(t, "edit-source", endpointRoutes(&posted, &eps), `{"network_id":"n1","object":"endpoint","action":"create","definition":`+def+`,"apply":true}`); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if len(posted) != 0 {
		t.Errorf("a refused call still posted: %v", posted)
	}
	r, srv := mustRun(t, "edit-source", endpointRoutes(&posted, &eps), `{"network_id":"n1","object":"endpoint","action":"delete","name":"h1"}`)
	if writes(srv) != 0 || !strings.Contains(r.Finding, `confirm="h1"`) {
		t.Fatalf("delete dry run: %s", r.Finding)
	}
	if _, _, err := runSkill(t, "edit-source", endpointRoutes(&posted, &eps), `{"network_id":"n1","object":"endpoint","action":"delete","name":"h1","apply":true}`); err == nil {
		t.Errorf("delete without confirm was accepted")
	}
}
