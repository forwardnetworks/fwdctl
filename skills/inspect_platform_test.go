package skills_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/knowledge"
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

func TestPlatformOrgPropertiesSaysWhereEachValueComesFrom(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		"GET /api/version": fwdtest.Const(200, map[string]any{"build": "1.0.0-test", "release": "26.10", "version": "1.0"}),
		"GET /api/orgs/o1/config": func(r *http.Request, _ []byte) (int, any) {
			switch r.URL.Query().Get("filter") {
			case "CONFIGURED":
				return 200, map[string]any{"predict_alpha": true}
			}
			return 200, map[string]any{"predict_alpha": true, "predict_beta": 7, "predict_model_ext_adv": true, "predict_model_config": "x"}
		},
		"GET /api/global-config": func(r *http.Request, _ []byte) (int, any) {
			if r.URL.Query().Get("filter") == "CONFIGURED" {
				return 200, map[string]any{"predict_beta": 7}
			}
			return 200, map[string]any{"predict_alpha": false, "predict_beta": 7, "predict_model_ext_adv": true, "predict_model_config": "x"}
		},
	}
	r, srv := mustRun(t, "inspect-platform", routes, `{"area":"org_properties","org_id":"o1","name":"predict_"}`)
	b := jsonOf(r)
	for _, want := range []string{`"source":"org"`, `"source":"global_override"`, `"source":"compiled_default"`, `"build":"1.0.0-test"`, "PREDICT_MODEL_EXT_ADV is true without an organization setting", "reprocessed"} {
		if !strings.Contains(b, want) {
			t.Errorf("missing %s in %s", want, b)
		}
	}
	if r.Status != result.OK || writes(srv) != 0 {
		t.Fatalf("%s writes=%d", r.Status, writes(srv))
	}
}

func TestPlatformOrgPropertiesTellsWhatLoginIsNeededOn403(t *testing.T) {
	r, _ := mustRun(t, "inspect-platform", map[string]fwdtest.Handler{
		"GET /api/version":        fwdtest.Const(200, map[string]any{"build": "1.0.0-test", "release": "26.10", "version": "1.0"}),
		"GET /api/orgs/o1/config": fwdtest.Const(403, map[string]any{"message": "Forward support authority required"}),
		"GET /api/global-config":  fwdtest.Const(403, map[string]any{"message": "Forward support authority required"}),
	}, `{"area":"org_properties","org_id":"o1"}`)
	lim := strings.Join(r.Limits, " | ")
	if r.Status != result.Unknown || !strings.Contains(lim, "support authority") || !strings.Contains(lim, "platform administrator") {
		t.Errorf("a 403 for support authority says which login is needed and who can run it: %s / %s", r.Status, lim)
	}
}

// The banners area reads every banner the UI can show: the org's custom banners, the operator's system announcement (SaaS only; on-prem has none and that is not a failure), and the org-wide critical
// states with the license-derived ones, the scheduled maintenance window, and any state this build does not know by name.
func TestPlatformBannersIncludeTheSystemAnnouncementAndCriticalStates(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		"GET /api/custom-banners":       fwdtest.Const(200, map[string]any{"banners": []any{map[string]any{"id": "b1", "enabled": true, "message": "freeze", "backgroundColor": "RED", "networkIds": []any{"1"}}}}),
		"GET /api/system-announcement":  fwdtest.Const(200, map[string]any{"announcement": map[string]any{"message": "Upgrade Saturday", "linkUrl": "https://example.test/n", "createdAt": "2026-10-01T00:00:00Z"}}),
		"GET /api/critical-states":      fwdtest.Const(200, map[string]any{"COLLECTOR_OUTDATED": true, "LICENSE_CCH_EXHAUSTED": false, "MAINTENANCE_WINDOW": map[string]any{"start": 1792000000000, "end": 1792003600000}, "LICENSE_WILL_EXPIRE": true}),
		"GET /api/licenses?view=status": fwdtest.Const(200, map[string]any{"licenseTier": "ENTERPRISE", "licenseStatus": "EXPIRED"}),
	}
	r, _ := mustRun(t, "inspect-platform", routes, `{"area":"banners"}`)
	all := fmtAny(r)
	for _, want := range []string{"freeze", "Upgrade Saturday", "COLLECTOR_OUTDATED", "LICENSE_EXPIRED", "LICENSE_WILL_EXPIRE", "maintenance_window"} {
		if !strings.Contains(all, want) {
			t.Errorf("the banners area lacks %q: %s", want, all)
		}
	}
	if r.Status != result.OK {
		t.Errorf("status %s %s", r.Status, r.Finding)
	}
}

func TestPlatformBannersOnPremHasNoAnnouncementAndThatIsNotAFailure(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		"GET /api/custom-banners":      fwdtest.Const(200, map[string]any{"banners": []any{}}),
		"GET /api/system-announcement": fwdtest.Const(404, map[string]any{"message": "No endpoint GET /api/system-announcement."}),
		"GET /api/critical-states":     fwdtest.Const(200, map[string]any{"LOW_STORAGE": true, "NO_STORAGE": false}),
	}
	r, _ := mustRun(t, "inspect-platform", routes, `{"area":"banners"}`)
	all := fmtAny(r)
	if r.Status != result.OK || !strings.Contains(all, "LOW_STORAGE") || !strings.Contains(strings.Join(r.Limits, " "), "no system announcement") {
		t.Errorf("%s %s %v", r.Status, r.Finding, r.Limits)
	}
}

// A property row says what the release's source default is and who may change it, so "ospf is off" comes with "the release default is false, and on SaaS only Forward support can turn it on". A property the release does not define says so.
func TestPlatformOrgPropertiesCarryTheReleaseDefaultAndWhoCanChangeIt(t *testing.T) {
	if len(knowledge.OrgPropertyNames()) == 0 {
		t.Skip("the property table is not in this build")
	}
	routes := map[string]fwdtest.Handler{
		"GET /api/version": fwdtest.Const(200, map[string]any{"build": "1.0.0-test", "release": "26.9.4-02", "version": "26.9"}),
		"GET /api/orgs/o1/config": func(r *http.Request, _ []byte) (int, any) {
			if r.URL.Query().Get("filter") == "CONFIGURED" {
				return 200, map[string]any{}
			}
			return 200, map[string]any{"predict_model_ospf": false}
		},
		"GET /api/global-config": fwdtest.Const(200, map[string]any{"predict_model_ospf": false}),
	}
	r, _ := mustRun(t, "inspect-platform", routes, `{"area":"org_properties","org_id":"o1","name":"predict_model_ospf"}`)
	b := jsonOf(r)
	if strings.Contains(b, `"master"`) || strings.Contains(b, "release_defaults") {
		t.Errorf("only the build's own release default is shown, never the unreleased master default: %s", b)
	}
	for _, want := range []string{`"release_default":"false"`, `"default_release":"26.9"`, "on-premises", "Forward support"} {
		if !strings.Contains(b, want) {
			t.Errorf("missing %s in %s", want, b)
		}
	}
}

// A property that changes how snapshots are computed says so (applies_to_snapshots_processed_after_change), so the reader knows a reprocess is needed for an existing snapshot to show it; the rows already name the build.
func TestPlatformOrgPropertiesSayWhichPropertiesNeedAReprocess(t *testing.T) {
	if len(knowledge.OrgPropertyNames()) == 0 {
		t.Skip("the property table is not in this build")
	}
	routes := map[string]fwdtest.Handler{
		"GET /api/version": fwdtest.Const(200, map[string]any{"build": "1.0.0-test", "release": "26.9.4-02", "version": "26.9"}),
		"GET /api/orgs/o1/config": func(r *http.Request, _ []byte) (int, any) {
			if r.URL.Query().Get("filter") == "CONFIGURED" {
				return 200, map[string]any{}
			}
			return 200, map[string]any{"predict_model_config": false}
		},
		"GET /api/global-config": fwdtest.Const(200, map[string]any{"predict_model_config": false}),
	}
	r, _ := mustRun(t, "inspect-platform", routes, `{"area":"org_properties","org_id":"o1","name":"predict_model_config"}`)
	b := jsonOf(r)
	if !strings.Contains(b, `"applies_to_snapshots_processed_after_change":true`) || !strings.Contains(b, `"release":"26.9.4-02"`) {
		t.Errorf("reprocess hint or build: %s", b)
	}
}

// Forward keys a webhook's last test result by the webhook's name (a webhook row has no id), so the result must be attached by name; a miss reads as "never tested".
func TestInspectPlatformWebhooksAttachTheLastTestByName(t *testing.T) {
	r, _ := mustRun(t, "inspect-platform", map[string]fwdtest.Handler{
		"GET /api/webhooks": fwdtest.Const(200, map[string]any{
			"webhooks":    []any{map[string]any{"name": "ops", "url": "https://hook.example.net/in", "enabled": true}},
			"testResults": map[string]any{"ops": map[string]any{"success": true, "statusCode": 200, "testedAt": "2026-10-26T10:00:00Z"}},
		}),
	}, `{"area":"webhooks"}`)
	b := jsonOf(r)
	if !strings.Contains(b, `"last_test":{`) || !strings.Contains(b, `"statusCode":200`) {
		t.Errorf("last_test not attached by name: %s", b)
	}
}

// Another organization's stored licenses (support): each shows whether it is activated and Available, and an Available stacking license of another tier is previewed as refused.
func TestInspectPlatformLicensingForAnOrgPreviewsTheTierRule(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		"GET /api/orgs/o1/licenses": fwdtest.Const(200, map[string]any{"licenses": []any{
			map[string]any{"signedLicenseKey": "KEY", "status": "ACTIVE", "license": map[string]any{"id": "L1", "type": "TIER", "tier": "NS", "replaces": false, "standardDeviceCap": 300, "startsAt": "2026-01-01T00:00:00Z", "expiresAt": "2026-12-31T00:00:00Z"}},
			map[string]any{"status": "ACTIVE", "license": map[string]any{"id": "L2", "type": "TIER", "tier": "NSP", "replaces": false, "standardDeviceCap": 301}},
			map[string]any{"status": "ACTIVE", "license": map[string]any{"id": "L3", "type": "TIER", "tier": "NSP", "replaces": true}},
			map[string]any{"status": "ACTIVE", "invalidated": true, "license": map[string]any{"id": "L4", "type": "TIER", "tier": "NSP", "replaces": true}},
		}}),
	}
	r, _ := mustRun(t, "inspect-platform", routes, `{"area":"licensing","org_id":"o1"}`)
	b := jsonOf(r)
	for _, want := range []string{`"id":"L2"`, "License tier mismatch: NS and NSP", "replacing: skips the tier check", `"activated":true`, "activates nothing", `"state":"INVALIDATED"`, `"state":"AVAILABLE"`, `"state":"ACTIVATED"`} {
		if !strings.Contains(b, want) {
			t.Errorf("missing %q: %s", want, b)
		}
	}
	if strings.Contains(b, "KEY") {
		t.Errorf("the signed key was returned: %s", b)
	}
}
