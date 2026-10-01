package skills_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
	"github.com/forwardnetworks/fwdctl/skills"
)

func wsRoutes(created *bool) map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		"GET /api/networks": func(*http.Request, []byte) (int, any) {
			ns := []any{map[string]any{"id": "10", "name": "prod"}, map[string]any{"id": "11", "name": "ws-old", "parentId": "10", "creator": "someone"}}
			if *created {
				ns = append(ns, map[string]any{"id": "12", "name": "zz-test", "parentId": "10", "retentionDays": 7})
			}
			return 200, ns
		},
		"GET /api/networks/10/classic-devices": fwdtest.Const(200, []any{map[string]any{"name": "sw-small", "host": "10.0.0.1", "type": "CISCO_IOS"}}),
		"POST /api/networks/10/workspaces": func(*http.Request, []byte) (int, any) {
			*created = true
			return 201, map[string]any{"id": "12", "name": "zz-test", "parentId": "10", "retentionDays": 7}
		},
		"GET /api/networks/10/endpoints": fwdtest.Const(200, []any{map[string]any{"type": "SNMP", "name": "ep1", "host": "10.9.9.9", "protocol": "SNMP_V2C", "credentialId": "parent-cred", "profileId": "SNMP-10"},
			map[string]any{"type": "SNMP", "name": "ep2", "host": "10.9.9.8", "protocol": "SNMP_V2C", "profileId": "SNMP-10"}}),
		"GET /api/networks/11/endpoints": fwdtest.Const(200, []any{}),
	}
}

const wsPlan = `{"network_id":"10","create_workspace":{"name":"zz-test","note":"temporary","devices":["sw-small"],"omissions":["NQE_CHECKS"]}`

func TestEditWorkspaceDryRunShowsThePayloadAndWritesNothing(t *testing.T) {
	created := false
	r, srv := mustRun(t, "edit-workspace", wsRoutes(&created), wsPlan+`}`)
	if r.Status != result.OK || r.Mode != result.ModeDryRun || writes(srv) != 0 || created {
		t.Fatalf("%s %s writes=%d", r.Status, r.Finding, writes(srv))
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "endpoints are NOT copied") {
		t.Errorf("limits %v", r.Limits)
	}
}

func TestEditWorkspaceRefusesWhatItShould(t *testing.T) {
	created := false
	for name, in := range map[string]string{
		"name in use":       `{"network_id":"10","create_workspace":{"name":"WS-OLD","devices":["sw-small"]}}`,
		"unknown device":    `{"network_id":"10","create_workspace":{"name":"zz-test","devices":["nope"]}}`,
		"under a workspace": `{"network_id":"11","create_workspace":{"name":"zz-test","devices":["sw-small"]}}`,
		"endpoints on prod": `{"network_id":"10","add_endpoints":{"endpoints":[{"name":"ep1","credential_id":"c"}]}}`,
		"delete prod":       `{"network_id":"10","delete_workspace":true,"confirm_name":"prod"}`,
		"delete wrong name": `{"network_id":"11","delete_workspace":true,"confirm_name":"nope"}`,
	} {
		r, srv := mustRun(t, "edit-workspace", wsRoutes(&created), in)
		if r.Status != result.Failed || writes(srv) != 0 || !strings.HasPrefix(r.Finding, "Refused") {
			t.Errorf("%s: %s %s", name, r.Status, r.Finding)
		}
	}
}

func TestEditWorkspaceApplyCreatesAndReadsBack(t *testing.T) {
	created := false
	r, _ := mustRun(t, "edit-workspace", wsRoutes(&created), wsPlan+`,"apply":true}`)
	if r.Status != result.OK || !created || r.Evidence[0].Detail["workspace_id"] != "12" || r.Evidence[0].Detail["held"] != true {
		t.Fatalf("%s %s %v", r.Status, r.Finding, r.Evidence[0].Detail)
	}
}

func TestEditWorkspaceAddEndpointsDefaultsToTheParentsCredentialAndSaysWhen(t *testing.T) {
	created := false
	r, _ := mustRun(t, "edit-workspace", wsRoutes(&created), `{"network_id":"11","add_endpoints":{"endpoints":[{"name":"ep1"}]}}`)
	if r.Status != result.OK || !strings.Contains(strings.Join(r.Limits, " "), "copy_credentials") {
		t.Fatalf("%s %v", r.Status, r.Limits)
	}
}

func TestEditWorkspaceAddsAnEndpointThatHasNoCredentialInTheParentAsItIs(t *testing.T) {
	created := false
	r, _ := mustRun(t, "edit-workspace", wsRoutes(&created), `{"network_id":"11","add_endpoints":{"endpoints":[{"name":"ep2"}]}}`)
	if r.Status != result.OK || !strings.Contains(strings.Join(r.Limits, " "), "no credential id in the parent either") {
		t.Fatalf("%s %v", r.Status, r.Limits)
	}
}

func runWithDeleter(t *testing.T, routes map[string]fwdtest.Handler, del fwd.NetworkDeleter, in string) result.Result {
	t.Helper()
	sess, _ := fwdtest.New(t, routes)
	sess.NetworkDeleter = del
	r, err := skills.Run(context.Background(), "edit-workspace", sess, json.RawMessage(in))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestEditWorkspaceDeletesOnlyTheLoginsOwnWorkspaceThroughTheInjectedDeleter(t *testing.T) {
	deleted := false
	routes := wsRoutes(new(bool))
	routes["GET /api/users/current"] = fwdtest.Const(200, map[string]any{"user": map[string]any{"id": "u1", "username": "me@example.test", "email": "me@example.test"}, "roles": map[string]any{"org": []string{"ADMIN"}, "network": map[string]string{}}})
	listed := routes["GET /api/networks"]
	routes["GET /api/networks"] = func(r *http.Request, b []byte) (int, any) {
		code, v := listed(r, b)
		ns := v.([]any)
		var out []any
		for _, n := range ns {
			m := n.(map[string]any)
			if m["id"] == "11" {
				m["creator"] = "me@example.test"
				if deleted {
					continue
				}
			}
			out = append(out, n)
		}
		return code, out
	}
	calls := 0
	del := func(_ context.Context, id string) error { calls++; deleted = true; return nil }
	in := `{"network_id":"11","delete_workspace":true,"confirm_name":"ws-old"`
	r := runWithDeleter(t, routes, del, in+`}`)
	if r.Status != result.OK || r.Mode != result.ModeDryRun || calls != 0 || !strings.Contains(strings.Join(r.Limits, " "), "recorded creator is this login") {
		t.Fatalf("dry run: %s %s calls=%d", r.Status, r.Finding, calls)
	}
	r = runWithDeleter(t, routes, del, in+`,"apply":true}`)
	if r.Status != result.OK || calls != 1 || !r.Changes[0].Applied {
		t.Fatalf("apply: %s %s calls=%d", r.Status, r.Finding, calls)
	}
	// a host that forbids deletion: a refusal, not an error, and nothing deleted
	deleted = false
	r = runWithDeleter(t, routes, func(context.Context, string) error { return fwd.ErrDeletionRefused }, in+`,"apply":true}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "does not allow deleting networks") || deleted {
		t.Fatalf("refused: %s %s", r.Status, r.Finding)
	}
}

func TestEditWorkspaceRefusesAWorkspaceSomeoneElseCreated(t *testing.T) {
	routes := wsRoutes(new(bool))
	routes["GET /api/users/current"] = fwdtest.Const(200, map[string]any{"user": map[string]any{"id": "u1", "username": "me@example.test", "email": "me@example.test"}, "roles": map[string]any{"org": []string{}, "network": map[string]string{}}})
	calls := 0
	r := runWithDeleter(t, routes, func(context.Context, string) error { calls++; return nil }, `{"network_id":"11","delete_workspace":true,"confirm_name":"ws-old","apply":true}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "created by") || calls != 0 {
		t.Fatalf("%s %s calls=%d", r.Status, r.Finding, calls)
	}
}
