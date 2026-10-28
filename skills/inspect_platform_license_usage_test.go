package skills_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
)

// The own organization: CCH used against licensed, each licence's running total over the window (the figure a corrected licence is sized from), devices and edge assets.
func TestInspectPlatformLicenseUsageOwnOrgReadsCCHPerLicence(t *testing.T) {
	var historyQuery string
	routes := map[string]fwdtest.Handler{
		"GET /api/platform-usage": func(r *http.Request, _ []byte) (int, any) {
			switch r.URL.Query().Get("view") {
			case "cch":
				return 200, map[string]any{"cchUsed": 900, "cchLicensed": 1000, "instant": "2026-10-28T00:00:00Z"}
			case "cch-history":
				historyQuery = r.URL.RawQuery
				return 200, map[string]any{"instanceCounts": map[string]any{}, "licenses": []any{
					map[string]any{"id": "L1", "cch": 1000, "replaces": false, "replaced": true, "startedAt": "2026-06-16T00:00:00Z", "usage": map[string]any{"2026-10-15T00:00:00Z": 700, "2026-10-16T00:00:00Z": 800}},
					map[string]any{"id": "L2", "cch": 500, "replaces": true, "startedAt": "2026-10-16T19:33:00Z", "usage": map[string]any{"2026-10-20T00:00:00Z": 100}},
				}}
			}
			return 404, nil
		},
		"GET /api/licensed-devices":      fwdtest.Const(200, map[string]any{"standardDeviceCount": 40, "liteDeviceCount": 5, "standardUnlicensedDeviceCount": 2, "standardDeviceCap": 50, "liteDeviceCap": 10}),
		"GET /api/licensed-edge-assets":  fwdtest.Const(200, map[string]any{"edgeAssetCount": 3, "edgeAssetCap": 10}),
		"GET /api/metrics/license-usage": fwdtest.Const(200, map[string]any{"events": []any{map[string]any{"timestamp": 1760000000000, "standardDeviceUsage": 39, "standardDeviceCap": 50}}}),
	}
	r, _ := mustRun(t, "inspect-platform", routes, `{"area":"license_usage","start":"2026-10-15","end":"2026-10-29"}`)
	b := jsonOf(r)
	for _, want := range []string{`"cch_remaining":100`, `"last_cumulative_cch_used":800`, `"cch_remaining_at_last_point":200`, `"replaced":true`, `"standard_cap":50`, `"edge_assets"`, `device_and_edge_use_over_time`, "separate figures"} {
		if !strings.Contains(b, want) {
			t.Errorf("missing %q: %s", want, b)
		}
	}
	if !strings.Contains(historyQuery, "start=2026-10-15T00%3A00%3A00Z") {
		t.Errorf("the window start must reach Forward: %q", historyQuery)
	}
}

// Another organization by id (support): tier with facets, summed caps, the unit counts and CCH used per stored period of each licence.
func TestInspectPlatformLicenseUsageByOrgIDReadsTierAndPeriods(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		"GET /api/admin/licenses":       fwdtest.Const(200, map[string]any{"orgs": []any{map[string]any{"orgId": "o1", "tier": "NSP", "status": "ACTIVE", "standardDeviceCap": 300, "cloudCreditHours": 41172000, "edgeAssetCap": 20}}}),
		"GET /api/admin/platform-usage": fwdtest.Const(200, map[string]any{"usage": []any{map[string]any{"orgId": "o1", "standardDeviceCount": 120, "liteDeviceCount": 4, "edgeAssetCount": 7, "cloudCreditHoursUsed": 5000}}}),
		"GET /api/orgs/o1/platform-usage": fwdtest.Const(200, map[string]any{"updatedAt": "2026-10-28T00:00:00Z",
			"licenses": []any{map[string]any{"id": "L1", "cch": 1000}},
			"periods":  []any{map[string]any{"start": "2026-06-16T00:00:00Z", "licenseId": "L1", "cchUsed": 800.5, "endReason": "LICENSE_REPLACED"}, map[string]any{"start": "2026-10-16T00:00:00Z", "cchUsed": 12}}}),
	}
	r, _ := mustRun(t, "inspect-platform", routes, `{"area":"license_usage","org_id":"o1"}`)
	b := jsonOf(r)
	for _, want := range []string{`"tier":"NSP"`, "NETWORK", "PREDICT", `"edge_assets_used":7`, `"cloud_credit_hours_used":5000`, `"cch_used_in_periods":800.5`, `"row":"cch_unattributed"`} {
		if !strings.Contains(b, want) {
			t.Errorf("missing %q: %s", want, b)
		}
	}
}
