package skills_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

// cfgRoutes serves Forward's configuration API: /api/config answers by its filter (OFF = every property, the rest the subsets Forward defines) and /api/global-config the
// deployment defaults. A nil view answers 403 so a view that cannot be read is exercised.
func cfgRoutes(effective, configured, overridden, nondefault, global map[string]any) map[string]fwdtest.Handler {
	pick := func(m map[string]any) (int, any) {
		if m == nil {
			return 403, map[string]any{"message": "forbidden"}
		}
		return 200, m
	}
	return map[string]fwdtest.Handler{
		"GET /api/version": fwdtest.Const(200, map[string]any{"version": "1.0.0", "release": "r", "build": "b"}),
		"GET /api/config": func(r *http.Request, _ []byte) (int, any) {
			switch r.URL.Query().Get("filter") {
			case "CONFIGURED":
				return pick(configured)
			case "OVERRIDDEN":
				return pick(overridden)
			case "NONDEFAULT":
				return pick(nondefault)
			}
			return pick(effective)
		},
		"GET /api/global-config": func(*http.Request, []byte) (int, any) { return pick(global) },
	}
}

func featureRows(t *testing.T, r result.Result) (map[string]map[string]any, map[string]any) {
	t.Helper()
	f, ok := r.Evidence[0].Detail["features"].(map[string]any)
	if !ok {
		t.Fatalf("no features block: %v", r.Evidence[0].Detail)
	}
	rows := map[string]map[string]any{}
	for _, x := range f["rows"].([]any) {
		row := x.(map[string]any)
		rows[row["property"].(string)] = row
	}
	return rows, f
}

func TestEnvironmentFeaturesShowEffectiveValueDefaultAndWhereItIsSet(t *testing.T) {
	routes := cfgRoutes(
		map[string]any{"advanced_reachability_analysis": "ASYNC", "disable_flow_computation": true, "predict_modeling": true, "background_snapshot_reprocess": "LOW_PRIORITY", "nqe_security_rules_panos": false},
		map[string]any{"advanced_reachability_analysis": "ASYNC", "predict_modeling": true},
		map[string]any{"advanced_reachability_analysis": "ASYNC"},
		map[string]any{"advanced_reachability_analysis": "ASYNC", "disable_flow_computation": true, "background_snapshot_reprocess": "LOW_PRIORITY"},
		map[string]any{"advanced_reachability_analysis": "ON_DEMAND", "disable_flow_computation": false, "predict_modeling": true, "background_snapshot_reprocess": "LOW_PRIORITY", "nqe_security_rules_panos": false})
	r, srv := mustRun(t, "inspect-environment", routes, `{}`)
	rows, f := featureRows(t, r)
	ar := rows["advanced_reachability_analysis"]
	if ar["value"] != "ASYNC" || ar["state"] != "automatic" || ar["default"] != "ON_DEMAND" || ar["overridden_at_org"] != true || ar["set_at"] != "organization override" || ar["level"] != "BOTH" || ar["org_admin_configurability"] != "ON_PREMISES" {
		t.Errorf("advanced reachability row: %v", ar)
	}
	dfc := rows["disable_flow_computation"]
	if dfc["state"] != "off" || dfc["symptom_when_off"] == nil || !strings.Contains(dfc["symptom_when_off"].(string), "REACHABILITY_COMPUTATION_DISABLED") || dfc["who_can_change_it"] == nil || dfc["overridden_at_org"] != false || dfc["set_at"] != "deployment default (differs from the built-in default)" && dfc["set_at"] != "built-in default" {
		t.Errorf("disable_flow_computation is inverted: true means the feature is off: %v", dfc)
	}
	if rows["predict_modeling"]["state"] != "on" || rows["predict_modeling"]["symptom_when_off"] != nil || rows["predict_modeling"]["set_at"] != "organization row equal to the deployment default" {
		t.Errorf("an on feature carries no symptom, and an org row that equals the default is said so: %v", rows["predict_modeling"])
	}
	if b := rows["background_snapshot_reprocess"]; b["set_at"] != "deployment default (differs from the built-in default)" || b["effect_of_a_change"] == nil {
		t.Errorf("a value changed at the deployment level: %v", b)
	}
	if rows["nqe_security_rules_panos"]["state"] != "off" {
		t.Errorf("an off flag: %v", rows["nqe_security_rules_panos"])
	}
	// a property the build does not return is said to be absent, never guessed
	missing, _ := json.Marshal(f["not_defined_by_this_build"])
	if !strings.Contains(string(missing), "peer_lldp_cdp_capability") || rows["peer_lldp_cdp_capability"] != nil {
		t.Errorf("missing properties: %s", missing)
	}
	l := strings.Join(r.Limits, "|")
	for _, want := range []string{"not who set a value, when", "network-level overrides of disable_flow_computation", "license tier", "reprocess_to_apply"} {
		if !strings.Contains(l, want) {
			t.Errorf("limits lack %q: %v", want, r.Limits)
		}
	}
	for _, c := range srv.Calls() {
		if c.Method != "GET" {
			t.Errorf("reading features must only read: %s %s", c.Method, c.Path)
		}
	}
}

func TestEnvironmentFeaturesSayWhichViewCouldNotBeRead(t *testing.T) {
	eff := map[string]any{"advanced_reachability_analysis": "ON_DEMAND", "disable_flow_computation": false}
	r, _ := mustRun(t, "inspect-environment", cfgRoutes(eff, eff, nil, nil, nil), `{}`)
	rows, f := featureRows(t, r)
	if _, has := rows["advanced_reachability_analysis"]["default"]; has {
		t.Errorf("without the global read there is no default to show: %v", rows["advanced_reachability_analysis"])
	}
	if _, has := rows["advanced_reachability_analysis"]["set_at"]; has {
		t.Errorf("set_at needs the overridden and nondefault views: %v", rows["advanced_reachability_analysis"])
	}
	src := f["sources"].(map[string]string)
	if !strings.HasPrefix(src["global"], "not read") || src["effective"] != "read" {
		t.Errorf("sources: %v", src)
	}
	if l := strings.Join(r.Limits, "|"); !strings.Contains(l, "the global view") || !strings.Contains(l, "left out, not guessed") {
		t.Errorf("limits: %v", r.Limits)
	}
	// without the effective view nothing about any feature is known
	r, _ = mustRun(t, "inspect-environment", cfgRoutes(nil, nil, nil, nil, nil), `{}`)
	if rows, _ := featureRows(t, r); len(rows) != 0 || !strings.Contains(strings.Join(r.Limits, "|"), "state of every feature is unknown") {
		t.Errorf("an unreadable configuration yields no rows and says so: %v %v", rows, r.Limits)
	}
}

func TestEnvironmentFeaturesCanBeSkipped(t *testing.T) {
	routes := cfgRoutes(map[string]any{"disable_flow_computation": false}, nil, nil, nil, nil)
	r, srv := mustRun(t, "inspect-environment", routes, `{"include_features":false}`)
	if _, has := r.Evidence[0].Detail["features"]; has {
		t.Errorf("features were skipped but are present")
	}
	for _, c := range srv.Calls() {
		if strings.Contains(c.Path, "config") {
			t.Errorf("skipped features must not read the configuration: %s", c.Path)
		}
	}
}

// exposureRoutes is a snapshot with the given advanced reachability state whose vulnerability list refuses the internet_addressable view with Forward's reason code.
func exposureRoutes(state, code string, cfg map[string]any) map[string]fwdtest.Handler {
	routes := map[string]fwdtest.Handler{
		snapsPath: fwdtest.Snapshots(advSnap(state)),
		vulnList:  fwdtest.Const(400, map[string]any{"message": "Cannot filter by internetAddressable: Internet exposure analysis is unavailable (" + code + ")"}),
	}
	if cfg != nil {
		routes["GET /api/config"] = fwdtest.Const(200, cfg)
	}
	return routes
}

func TestExposureDistinguishesDisabledFromNeverTriggeredFromShouldHaveStarted(t *testing.T) {
	cases := []struct {
		name, state, code string
		cfg               map[string]any
		want, notWant     []string
		next              string
	}{
		{"blocked: disabled by the organization property", "UNPROCESSED", "PENDING_ADVANCED_REACHABILITY",
			map[string]any{"disable_flow_computation": true, "advanced_reachability_analysis": "ON_DEMAND"},
			[]string{"blocked, not just never triggered", "DISABLE_FLOW_COMPUTATION is true", "would not produce exposure"}, []string{"by design"}, "inspect-environment"},
		{"never asked for, by design", "UNPROCESSED", "PENDING_ADVANCED_REACHABILITY",
			map[string]any{"disable_flow_computation": false, "advanced_reachability_analysis": "ON_DEMAND"},
			[]string{"by design", "ON_DEMAND"}, []string{"blocked"}, "edit-advanced-reachability"},
		{"async that did not start: UNKNOWN which", "UNPROCESSED", "PENDING_ADVANCED_REACHABILITY",
			map[string]any{"disable_flow_computation": false, "advanced_reachability_analysis": "ASYNC"},
			[]string{"ASYNC", "UNKNOWN", "predicted snapshots"}, []string{"by design", "blocked"}, "inspect-environment"},
		{"code says disabled, property says not: the cause is UNKNOWN", "PROCESSED", "REACHABILITY_COMPUTATION_DISABLED",
			map[string]any{"disable_flow_computation": false},
			[]string{"flow computation is disabled", "network-level override", "license", "UNKNOWN"}, nil, "inspect-environment"},
		{"code says disabled and the property is true", "PROCESSED", "REACHABILITY_COMPUTATION_DISABLED",
			map[string]any{"disable_flow_computation": true},
			[]string{"flow computation is disabled", "DISABLE_FLOW_COMPUTATION is true"}, []string{"UNKNOWN"}, "inspect-environment"},
		{"properties unreadable", "UNPROCESSED", "PENDING_ADVANCED_REACHABILITY", nil,
			[]string{"could not be read", "UNKNOWN"}, []string{"by design", "DISABLE_FLOW_COMPUTATION is true"}, "edit-advanced-reachability"},
	}
	for _, c := range cases {
		r, _ := vuln(t, exposureRoutes(c.state, c.code, c.cfg), netIn+`,"internet_addressable":true}`)
		text := r.Finding + "|" + strings.Join(r.Limits, "|")
		if r.Status != result.Unknown {
			t.Errorf("%s: %s", c.name, r.Status)
		}
		for _, w := range c.want {
			if !strings.Contains(text, w) {
				t.Errorf("%s: lacks %q in %s", c.name, w, text)
			}
		}
		for _, w := range c.notWant {
			if strings.Contains(text, w) {
				t.Errorf("%s: must not say %q: %s", c.name, w, text)
			}
		}
		if len(r.NextActions) == 0 || r.NextActions[0] != c.next {
			t.Errorf("%s: next actions %v, want %s first", c.name, r.NextActions, c.next)
		}
	}
}

func TestEditAdvancedReachabilityRefusesWhenFlowComputationIsDisabledAndSaysWhatItCannotTell(t *testing.T) {
	disabled := advRoutes("UNPROCESSED", "UNPROCESSED", accept())
	disabled["GET /api/config"] = fwdtest.Const(200, map[string]any{"disable_flow_computation": true, "advanced_reachability_analysis": "ON_DEMAND"})
	for _, in := range []string{`{"network_id":"n1","snapshot_id":"s1"}`, `{"network_id":"n1","snapshot_id":"s1","apply":true}`} {
		r, srv := mustRun(t, "edit-advanced-reachability", disabled, in)
		if r.Status != result.Failed || !strings.Contains(r.Finding, "blocked, not just never triggered") || writes(srv) != 0 {
			t.Fatalf("%s: %s %q writes %d", in, r.Status, r.Finding, writes(srv))
		}
		if l := strings.Join(r.Limits, "|"); !strings.Contains(l, "DISABLE_FLOW_COMPUTATION is true") || !strings.Contains(l, "reprocessed") {
			t.Errorf("limits: %v", r.Limits)
		}
	}
	// enabled (the property is false): the request goes ahead, and the limits say the two causes no API shows
	ok := advRoutes("UNPROCESSED", "PROCESSING", accept())
	ok["GET /api/config"] = fwdtest.Const(200, map[string]any{"disable_flow_computation": false, "advanced_reachability_analysis": "ON_DEMAND"})
	r, _ := mustRun(t, "edit-advanced-reachability", ok, `{"network_id":"n1","snapshot_id":"s1"}`)
	if r.Status != result.OK || r.Mode != result.ModeDryRun || !strings.Contains(strings.Join(r.Limits, "|"), "network-level override of it or a license without path analysis") {
		t.Errorf("%s %v", r.Status, r.Limits)
	}
	// ASYNC: it should have started itself; the limit says so
	async := advRoutes("UNPROCESSED", "PROCESSING", accept())
	async["GET /api/config"] = fwdtest.Const(200, map[string]any{"disable_flow_computation": false, "advanced_reachability_analysis": "ASYNC"})
	r, _ = mustRun(t, "edit-advanced-reachability", async, `{"network_id":"n1","snapshot_id":"s1"}`)
	if !strings.Contains(strings.Join(r.Limits, "|"), "ASYNC, so Forward should have started this") {
		t.Errorf("%v", r.Limits)
	}
	// the properties cannot be read: UNKNOWN is said, and the request is not refused on a guess
	unread := advRoutes("UNPROCESSED", "PROCESSING", accept())
	r, _ = mustRun(t, "edit-advanced-reachability", unread, `{"network_id":"n1","snapshot_id":"s1"}`)
	if r.Status != result.OK || !strings.Contains(strings.Join(r.Limits, "|"), "whether flow computation is enabled is UNKNOWN") {
		t.Errorf("%s %v", r.Status, r.Limits)
	}
}
