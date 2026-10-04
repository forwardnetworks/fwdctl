package skills_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

const planted = "PLANTED-SECRET-VALUE-7731"

func TestPlatformCredentialsListNamesAndIdsAndNeverASecret(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/cli-credentials":  fwdtest.Const(200, []any{map[string]any{"id": "c1", "name": "ops", "username": "admin", "password": planted, "privilegedModePasswordId": planted}}),
		"GET /api/networks/n1/http-credentials": fwdtest.Const(200, []any{map[string]any{"id": "h1", "name": "api", "username": "u", "password": planted}}),
		"GET /api/networks/n1/snmpCredentials":  fwdtest.Const(200, []any{map[string]any{"id": "s1", "name": "poll", "communityString": planted, "privacyPassword": planted}}),
	}
	r, srv := mustRun(t, "inspect-platform", routes, `{"network_id":"n1","area":"credentials"}`)
	b := jsonOf(r)
	if strings.Contains(b, planted) {
		t.Fatalf("a secret leaked: %s", b)
	}
	if r.Status != result.OK || !strings.Contains(r.Finding, "3 CLI, SNMP and HTTP credentials") || !strings.Contains(b, `"credential_type":"SNMP"`) || !strings.Contains(b, `"name":"ops"`) || !strings.Contains(b, "redacted") {
		t.Fatalf("%s %s", r.Finding, b)
	}
	if writes(srv) != 0 {
		t.Errorf("a read must not write")
	}
	for _, c := range srv.Calls() {
		if strings.Contains(fmtAny(c.Body), planted) {
			t.Errorf("secret in a request")
		}
	}
}

func TestPlatformRefusesTheWrongScopeAndNamesTheAreas(t *testing.T) {
	routes := map[string]fwdtest.Handler{}
	for _, in := range []string{`{"area":"credentials"}`, `{"area":"webhooks","network_id":"n1"}`, `{"area":"nope"}`, `{}`} {
		if _, _, err := runSkill(t, "inspect-platform", routes, in); err == nil {
			t.Errorf("%s must be refused", in)
		}
	}
	if _, _, err := runSkill(t, "inspect-platform", routes, `{"area":"nope"}`); err == nil || !strings.Contains(err.Error(), "credentials") {
		t.Errorf("the refusal lists the areas: %v", err)
	}
}

func TestPlatformEmptyIsUnknownAndAForbiddenReadIsExplained(t *testing.T) {
	r, _ := mustRun(t, "inspect-platform", map[string]fwdtest.Handler{"GET /api/custom-banners": fwdtest.Const(200, []any{})}, `{"area":"banners"}`)
	if r.Status != result.Unknown {
		t.Errorf("an empty list is unknown: %s", r.Status)
	}
	r, _ = mustRun(t, "inspect-platform", map[string]fwdtest.Handler{"GET /api/custom-banners": fwdtest.Const(403, map[string]any{"message": "Missing permission: OrgOperation.VIEW_SETTINGS"})}, `{"area":"banners"}`)
	if r.Status != result.Unknown || !strings.Contains(strings.Join(r.Limits, " ")+r.Finding, "inspect-access") {
		t.Errorf("a 403 says what is missing: %s %v", r.Status, r.Limits)
	}
}

func fmtAny(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestPlatformDashboardsAndScorecardsSayTheyAreUnpublished(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/dashboards": fwdtest.Const(200, []any{map[string]any{"id": "d1", "name": "Overview"}}),
		"GET /api/networks/n1/scorecards": fwdtest.Const(200, []any{map[string]any{"name": "Hygiene", "id": "sc1"}}),
		"GET /api/networks/n1/snapshots":  fwdtest.Snapshots(fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")),
	}
	for _, area := range []string{"dashboards", "scorecards"} {
		r, _ := mustRun(t, "inspect-platform", routes, `{"area":"`+area+`","network_id":"n1"}`)
		if r.Status != result.OK || !strings.Contains(strings.Join(r.Limits, " "), "unpublished") {
			t.Errorf("%s: %s %v", area, r.Status, r.Limits)
		}
	}
}

func TestPlatformScorecardTrendsSummariseTheWindowAndSkipUnscoredPoints(t *testing.T) {
	routes := map[string]fwdtest.Handler{"GET /api/networks/n1/scorecards": func(r *http.Request, _ []byte) (int, any) {
		if r.URL.Query().Get("view") == "trends" {
			return 200, map[string]any{"-1": []any{
				map[string]any{"snapshotId": "s1", "instant": 1788000000000, "score": 0.5},
				map[string]any{"snapshotId": "s2", "instant": 1788100000000},
				map[string]any{"snapshotId": "s3", "instant": 1788200000000, "score": 0.7},
			}}
		}
		return 200, []any{map[string]any{"id": "-1", "name": "Forward Scorecard"}}
	}}
	r, _ := mustRun(t, "inspect-platform", routes, `{"area":"scorecard_trends","network_id":"n1"}`)
	row := r.Evidence[0].Detail["rows"].([]map[string]any)[0]
	if r.Status != result.OK || row["points"] != 3 || row["scored_points"] != 2 || row["name"] != "Forward Scorecard" || row["min"] != 0.5 || row["max"] != 0.7 {
		t.Fatalf("%s %v", r.Status, row)
	}
}
