package skills_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/knowledge"
	"github.com/forwardnetworks/fwdctl/result"
)

func sessionJSON(org []string, net map[string]string) map[string]any {
	return map[string]any{"user": map[string]any{"id": "u-me", "username": "me@example.test", "enabled": true, "authSource": "LOCAL"},
		"roles": map[string]any{"org": org, "network": net}, "groupIds": []string{"g1"}}
}

// accessRoutes serves a small org: the caller, two users, two groups. Writes are recorded in written.
func accessRoutes(sess map[string]any, written map[string]string) map[string]fwdtest.Handler {
	users := []map[string]any{
		{"id": "u-me", "username": "me@example.test", "email": "me@example.test", "enabled": true, "authSource": "LOCAL", "orgAdmin": false},
		{"id": "u-bob", "username": "bob@example.test", "email": "bob@example.test", "enabled": true, "authSource": "LOCAL", "orgAdmin": false,
			"accessibleNetworks": []map[string]any{{"id": "N1", "name": "n1", "role": "READ_ONLY"}}, "groupIds": []string{"g1"}},
		{"id": "u-dana", "username": "dana@example.test", "email": "dana@example.test", "enabled": true, "authSource": "LOCAL", "orgAdmin": true},
	}
	groups := []map[string]any{{"id": "g1", "name": "netops", "externalGroupNames": []string{"NetOps"}, "networkRoles": map[string]string{"N1": "READ_ONLY"}, "deviceAccessLabelIds": []string{}}}
	rec := func(k string) fwdtest.Handler {
		return func(r *http.Request, b []byte) (int, any) { written[k] = r.URL.RawQuery + string(b); return 204, nil }
	}
	return map[string]fwdtest.Handler{
		"GET /api/users/current": fwdtest.Const(200, sess),
		"GET /api/users": func(r *http.Request, _ []byte) (int, any) {
			if r.URL.Query().Get("view") == "2fa" {
				return 200, map[string]any{"u-bob": map[string]any{"setUp": true}}
			}
			return 200, users
		},
		"GET /api/access-control-groups":           fwdtest.Const(200, groups),
		"POST /api/users/u-bob/roles/network/N1":   rec("set_role"),
		"PATCH /api/users/u-bob":                   rec("patch"),
		"DELETE /api/users/u-bob":                  rec("delete"),
		"POST /api/users/u-dana/roles/org/ADMIN":   rec("grant"),
		"DELETE /api/users/u-dana/roles/org/ADMIN": rec("revoke"),
	}
}

func needModel(t *testing.T) {
	if knowledge.RBACModel() == nil {
		t.Skip("this build carries no role model")
	}
}

func TestExplainNamesTheRoleAnOperationNeedsAndWhoGrantsIt(t *testing.T) {
	needModel(t)
	r, _ := mustRun(t, "inspect-access", accessRoutes(sessionJSON(nil, map[string]string{"N1": "READ_ONLY"}), map[string]string{}),
		`{"view":"explain","error":"Missing permission: NetworkOperation.EDIT_CHECKS","network_id":"N1"}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "READ_ONLY") || !strings.Contains(r.Finding, "OPERATOR") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	res := strings.Join(anyStrings(r.Evidence[0].Detail["resolution"]), " ")
	if !strings.Contains(res, "organization administrator") {
		t.Errorf("no resolution naming who can fix it: %q", res)
	}
	// an org admin refused anyway is not a role problem
	r, _ = mustRun(t, "inspect-access", accessRoutes(sessionJSON([]string{"ADMIN"}, nil), map[string]string{}), `{"view":"explain","operation":"EDIT_CHECKS","network_id":"N1"}`)
	if !strings.Contains(r.Finding, "although this login is an organization administrator") {
		t.Fatalf("%s", r.Finding)
	}
	// a licence refusal is not a role problem either
	r, _ = mustRun(t, "inspect-access", accessRoutes(sessionJSON(nil, nil), map[string]string{}), `{"view":"explain","error":"Unlicensed operation: NetworkOperation.VIEW_PATHS"}`)
	if !strings.Contains(r.Finding, "licence") {
		t.Fatalf("%s", r.Finding)
	}
	// an operation only an org admin holds
	r, _ = mustRun(t, "inspect-access", accessRoutes(sessionJSON(nil, map[string]string{"N1": "ADMIN"}), map[string]string{}), `{"view":"explain","error":"Missing permission: OrgOperation.MANAGE_USER_ACCOUNTS"}`)
	if !strings.Contains(r.Finding, "needs an organization administrator") {
		t.Fatalf("%s", r.Finding)
	}
	// an operation this build does not know is unknown, never a guess
	r, _ = mustRun(t, "inspect-access", accessRoutes(sessionJSON(nil, nil), map[string]string{}), `{"view":"explain","operation":"ZZ_NOT_AN_OPERATION"}`)
	if r.Status != result.Unknown {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
}

func anyStrings(v any) []string {
	var out []string
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		for _, e := range x {
			out = append(out, e.(string))
		}
	}
	return out
}

func TestAccessMeAndListsAndAForbiddenListIsExplained(t *testing.T) {
	needModel(t)
	r, _ := mustRun(t, "inspect-access", accessRoutes(sessionJSON(nil, map[string]string{"N1": "OPERATOR"}), map[string]string{}), `{"network_id":"N1"}`)
	if r.Status != result.OK || !strings.Contains(r.Evidence[0].Detail["on_network"].(map[string]any)["role"].(string), "OPERATOR") {
		t.Fatalf("%s %v", r.Finding, r.Evidence[0].Detail)
	}
	r, _ = mustRun(t, "inspect-access", accessRoutes(sessionJSON([]string{"ADMIN"}, nil), map[string]string{}), `{"view":"users"}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "1 organization administrators") {
		t.Fatalf("%s", r.Finding)
	}
	r, _ = mustRun(t, "inspect-access", accessRoutes(sessionJSON([]string{"ADMIN"}, nil), map[string]string{}), `{"view":"groups"}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "1 access control groups") {
		t.Fatalf("%s", r.Finding)
	}
	routes := accessRoutes(sessionJSON(nil, map[string]string{"N1": "ADMIN"}), map[string]string{})
	routes["GET /api/users"] = fwdtest.Const(403, map[string]any{"message": "Missing permission: OrgOperation.VIEW_USER_ACCOUNTS"})
	r, _ = mustRun(t, "inspect-access", routes, `{"view":"users"}`)
	if r.Status != result.Unknown || !strings.Contains(r.Finding, "VIEW_USER_ACCOUNTS") || !strings.Contains(r.Finding, "organization administrator") {
		t.Fatalf("a forbidden list is explained, not an error: %s %s", r.Status, r.Finding)
	}
}

func TestEditAccessNeedsAnOrgAdminAndIsADryRunFirst(t *testing.T) {
	written := map[string]string{}
	r, srv := mustRun(t, "edit-access", accessRoutes(sessionJSON(nil, map[string]string{"N1": "ADMIN"}), written), `{"action":"set_network_role","user":"bob@example.test","network_id":"N1","role":"OPERATOR"}`)
	if r.Status != result.Unknown || !strings.Contains(r.Finding, "organization administrator") || writes(srv) != 0 {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	admin := sessionJSON([]string{"ADMIN"}, nil)
	r, srv = mustRun(t, "edit-access", accessRoutes(admin, written), `{"action":"set_network_role","user":"bob@example.test","network_id":"N1","role":"OPERATOR"}`)
	if r.Status != result.OK || r.Mode != result.ModeDryRun || writes(srv) != 0 || len(r.Changes) != 1 || !strings.Contains(r.Changes[0].Undo, "READ_ONLY") {
		t.Fatalf("%s %s writes=%d %+v", r.Status, r.Finding, writes(srv), r.Changes)
	}
	// the same role is a no-op, not a write
	r, srv = mustRun(t, "edit-access", accessRoutes(admin, written), `{"action":"set_network_role","user":"bob@example.test","network_id":"N1","role":"READ_ONLY","apply":true}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "nothing to change") || writes(srv) != 0 {
		t.Fatalf("%s", r.Finding)
	}
}

func TestEditAccessRefusesWhatForwardRefusesAndConfirmsWhatCannotBeUndone(t *testing.T) {
	admin := sessionJSON([]string{"ADMIN"}, nil)
	written := map[string]string{}
	r, srv := mustRun(t, "edit-access", accessRoutes(admin, written), `{"action":"set_user","user":"me@example.test","enabled":false,"apply":true}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "own account") || writes(srv) != 0 {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	r, srv = mustRun(t, "edit-access", accessRoutes(admin, written), `{"action":"delete_user","user":"bob@example.test","apply":true}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "confirm") || writes(srv) != 0 {
		t.Fatalf("delete needs confirm: %s %s", r.Status, r.Finding)
	}
	r, srv = mustRun(t, "edit-access", accessRoutes(admin, written), `{"action":"set_org_admin","user":"dana@example.test","admin":false}`)
	if r.Status != result.OK || writes(srv) != 0 || !strings.Contains(r.Changes[0].Undo, "admin=true") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	r, _ = mustRun(t, "edit-access", accessRoutes(admin, written), `{"action":"set_network_role","user":"dana@example.test","network_id":"N1","role":"READ_ONLY"}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "organization administrator") {
		t.Fatalf("an org admin already holds every role: %s", r.Finding)
	}
}

func TestAnySkillsPermissionDenialIsExplainedNotAnError(t *testing.T) {
	routes := cfgRoutes(map[string]any{"advanced_reachability_analysis": "ON_DEMAND"}, map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{"advanced_reachability_analysis": "ON_DEMAND"})
	routes["PUT /api/config/advanced_reachability_analysis"] = fwdtest.Const(403, map[string]any{"message": "Missing permission: OrgOperation.MANAGE_ORG_SETTINGS"})
	r, _ := mustRun(t, "edit-org-property", routes, `{"property":"ADVANCED_REACHABILITY_ANALYSIS","value":"ASYNC","apply":true}`)
	if r.Status != result.Unknown || !strings.Contains(r.Finding, "MANAGE_ORG_SETTINGS") || !strings.Contains(strings.Join(r.NextActions, " "), "inspect-access") {
		t.Fatalf("%s %s %v", r.Status, r.Finding, r.NextActions)
	}
}

func TestEditAccessAppliesAndReadsBack(t *testing.T) {
	admin := sessionJSON([]string{"ADMIN"}, nil)
	written := map[string]string{}
	routes := accessRoutes(admin, written)
	deleted := false
	list := routes["GET /api/users"]
	routes["GET /api/users"] = func(r *http.Request, b []byte) (int, any) {
		code, v := list(r, b)
		if us, ok := v.([]map[string]any); ok && deleted {
			var keep []map[string]any
			for _, u := range us {
				if u["id"] != "u-bob" {
					keep = append(keep, u)
				}
			}
			return code, keep
		}
		return code, v
	}
	routes["DELETE /api/users/u-bob"] = func(*http.Request, []byte) (int, any) { deleted = true; written["delete"] = "x"; return 204, nil }
	r, _ := mustRun(t, "edit-access", routes, `{"action":"delete_user","user":"bob@example.test","apply":true,"confirm":"bob@example.test"}`)
	if r.Status != result.OK || !r.Changes[0].Applied || written["delete"] == "" || !strings.Contains(r.Changes[0].Undo, "cannot be undone") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	// Forward accepted but the user is still there: failed, not ok
	written2 := map[string]string{}
	r, _ = mustRun(t, "edit-access", accessRoutes(admin, written2), `{"action":"delete_user","user":"bob@example.test","apply":true,"confirm":"bob@example.test"}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "still exists") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
}

func TestAccessActivitySummarisesAuditRecordsAndSaysWhatTheyCannotProve(t *testing.T) {
	routes := accessRoutes(sessionJSON([]string{"ADMIN"}, nil), map[string]string{})
	var q string
	routes["GET /api/audit-logs"] = func(r *http.Request, _ []byte) (int, any) {
		q = r.URL.RawQuery
		return 200, map[string]any{"records": []map[string]any{
			{"timestamp": "2026-10-01T10:00:00Z", "userId": "u-bob", "httpMethod": "DELETE", "targetUri": "/networks/N1/classic-devices/r1", "httpResponseCode": 204, "remoteIp": "192.0.2.1"},
			{"timestamp": "2026-10-01T09:00:00Z", "userId": "u-bob", "httpMethod": "POST", "targetUri": "/networks/N1/classic-devices", "httpResponseCode": 403, "remoteIp": "192.0.2.1"},
		}, "paging": map[string]any{"total": 2}}
	}
	r, _ := mustRun(t, "inspect-access", routes, `{"view":"activity","network_id":"N1","match":"classic-devices","user":"bob@example.test","since":"48h"}`)
	b := jsonOf(r)
	if r.Status != result.OK || !strings.Contains(r.Finding, "most by bob@example.test (2)") || !strings.Contains(r.Finding, "1 failed") {
		t.Fatalf("%s %s", r.Status, b)
	}
	for _, want := range []string{"userId=u-bob", "targetUri=%2Fnetworks%2FN1%2Fclassic-devices"} {
		if !strings.Contains(q, want) {
			t.Errorf("query %q lacks %s", q, want)
		}
	}
	if !strings.Contains(strings.Join(r.Limits, "|"), "no request bodies") {
		t.Errorf("limits: %v", r.Limits)
	}
	r, _ = mustRun(t, "inspect-access", routes, `{"view":"activity","status":"failed","until":"2026-10-02T00:00:00Z","since":"2026-09-01T00:00:00Z"}`)
	b = jsonOf(r)
	for _, want := range []string{`"by_day":[{"name":"2026-10-01","requests":1}]`, `POST /networks/N1/classic-devices`, "endTime=2026-10-02"} {
		if !strings.Contains(b, want) && !strings.Contains(q, want) {
			t.Errorf("missing %s in %s / %s", want, b, q)
		}
	}
	if strings.Contains(b, "DELETE /networks") || !strings.Contains(strings.Join(r.Limits, "|"), "no request or response sizes") {
		t.Errorf("status failed keeps only the 4xx/5xx rows and says sizes are not recorded: %s", b)
	}
	routes["GET /api/audit-logs"] = fwdtest.Const(200, map[string]any{"records": []any{}, "paging": map[string]any{"total": 0}})
	if r, _ = mustRun(t, "inspect-access", routes, `{"view":"activity"}`); r.Status != result.Unknown {
		t.Errorf("no records is not proof: %s", r.Status)
	}
	if _, _, err := runSkill(t, "inspect-access", routes, `{"view":"activity","since":"soon"}`); err == nil {
		t.Errorf("a bad since must be refused")
	}
}
