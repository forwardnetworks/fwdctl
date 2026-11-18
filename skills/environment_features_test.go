package skills_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/knowledge"
	"github.com/forwardnetworks/fwdctl/result"
	"github.com/forwardnetworks/fwdctl/skills"
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

// The license tier IS returned (GET /api/licenses?view=status). A tier without the Predict facet makes the Predict features "licensed: no" even when their organization property reads on, which is what a 403 "Unlicensed
// operation" on every change-set call means.
func TestEnvironmentReportsTheLicenseTierAndMarksUnlicensedFeatures(t *testing.T) {
	routes := cfgRoutes(
		map[string]any{"predict_modeling": true, "predict_model_ospf": false},
		map[string]any{"predict_modeling": true},
		map[string]any{"predict_modeling": true},
		map[string]any{"predict_modeling": true},
		map[string]any{"predict_modeling": true})
	routes["GET /api/licenses"] = fwdtest.Const(200, map[string]any{"licenseTier": "NS", "licenseStatus": "ACTIVE"})
	r, _ := mustRun(t, "inspect-environment", routes, `{}`)
	lic, ok := r.Evidence[0].Detail["license"].(map[string]any)
	if !ok || lic["tier"] != "NS" || lic["status"] != "ACTIVE" {
		t.Fatalf("license: %v", r.Evidence[0].Detail["license"])
	}
	facets := lic["facets"].(map[string]bool)
	if !facets["network"] || !facets["security"] || facets["predict"] {
		t.Errorf("NS has the network and security facets, not Predict: %v", facets)
	}
	rows, _ := featureRows(t, r)
	if rows["predict_modeling"]["licensed"] != false || !strings.Contains(fmt.Sprint(rows["predict_modeling"]["licensed_note"]), "Predict") {
		t.Errorf("predict_modeling reads on but is not licensed: %v", rows["predict_modeling"])
	}
	if o := rows["predict_model_ospf"]; o["state"] != "off" || !strings.Contains(fmt.Sprint(o["licensed_note"]), "off, and also unlicensed") || strings.Contains(fmt.Sprint(o["licensed_note"]), "reads on") {
		t.Errorf("an off row must not say its property reads on: %v", o)
	}
	if strings.Contains(strings.Join(r.Limits, " "), "the license tier (a license") {
		t.Errorf("the limit that says the tier is not returned must go: %v", r.Limits)
	}
	routes["GET /api/licenses"] = fwdtest.Const(200, map[string]any{"licenseTier": "NSP", "licenseStatus": "EXPIRED"})
	r, _ = mustRun(t, "inspect-environment", routes, `{}`)
	lic = r.Evidence[0].Detail["license"].(map[string]any)
	if lic["can_write"] != false || !strings.Contains(fmt.Sprint(lic["note"]), "ACTIVE") {
		t.Errorf("only ACTIVE, EXEMPT or SOFTWARE_CENTRAL licenses can write: %v", lic)
	}
	rows, _ = featureRows(t, r)
	if rows["predict_modeling"]["licensed"] != true {
		t.Errorf("NSP includes Predict: %v", rows["predict_modeling"])
	}
}

// The features that unlock security and audit evidence are rows of the same table, tagged topic security, so an audit can read them and say which missing evidence an off property explains: an off
// one carries what is missing and who may change it (a support-only property says so); a non-security row has no topic; a property the build does not return is said to be absent.
func TestEnvironmentFeaturesTagSecurityRowsAndSayWhatMissingEvidenceAnOffOneExplains(t *testing.T) {
	routes := cfgRoutes(
		map[string]any{"cis_benchmarks": false, "threat_exposure": false, "nqe_device_services": true, "audit_logs_retention_days": 90, "copy_credentials": "DISABLED"},
		map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{"cis_benchmarks": true})
	r, _ := mustRun(t, "inspect-environment", routes, `{}`)
	rows, f := featureRows(t, r)
	cis := rows["cis_benchmarks"]
	if cis["topic"] != "security" || cis["state"] != "off" || !strings.Contains(fmt.Sprint(cis["symptom_when_off"]), "not the same as no findings") || cis["who_can_change_it"] == nil {
		t.Errorf("an off CIS feature says what evidence is missing: %v", cis)
	}
	if ds := rows["nqe_device_services"]; ds["topic"] != "security" || ds["state"] != "on" || ds["symptom_when_off"] != nil {
		t.Errorf("an on security feature carries no symptom: %v", ds)
	}
	if rows["copy_credentials"]["topic"] != nil {
		t.Errorf("copy_credentials is not a security-evidence feature: %v", rows["copy_credentials"])
	}
	if al := rows["audit_logs_retention_days"]; al["topic"] != "security" || al["state"] != "value" || al["value"] != float64(90) && al["value"] != "90" && fmt.Sprint(al["value"]) != "90" {
		t.Errorf("audit log retention is a value: %v", al)
	}
	missing, _ := json.Marshal(f["not_defined_by_this_build"])
	if !strings.Contains(string(missing), "snmp_trap_monitoring") || rows["snmp_trap_monitoring"] != nil {
		t.Errorf("a property the build does not return is absent, not guessed: %s", missing)
	}
	// a support-only property says who can change it
	routes = cfgRoutes(map[string]any{"snmp_trap_monitoring": false}, map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{})
	r, _ = mustRun(t, "inspect-environment", routes, `{}`)
	rows, _ = featureRows(t, r)
	if w := fmt.Sprint(rows["snmp_trap_monitoring"]["who_can_change_it"]); !strings.Contains(w, "Forward support or a Forward admin only") {
		t.Errorf("snmp_trap_monitoring cannot be changed by an organization administrator: %v", rows["snmp_trap_monitoring"])
	}
}

// Threat exposure needs the license's security facet (S): with a tier that lacks it the row says turning the property on would not make the calls work; with it the row is licensed.
func TestEnvironmentMarksThreatExposureUnlicensedWithoutTheSecurityFacet(t *testing.T) {
	routes := cfgRoutes(map[string]any{"threat_exposure": true, "edge_assets_in_sources": false},
		map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{})
	routes["GET /api/licenses"] = fwdtest.Const(200, map[string]any{"licenseTier": "NP", "licenseStatus": "ACTIVE"})
	r, _ := mustRun(t, "inspect-environment", routes, `{}`)
	rows, _ := featureRows(t, r)
	te := rows["threat_exposure"]
	if te["licensed"] != false || !strings.Contains(fmt.Sprint(te["licensed_note"]), "the organization property reads on, but license tier NP has no security facet") {
		t.Errorf("an on threat exposure on a tier with no security facet: %v", te)
	}
	if rows["edge_assets_in_sources"]["licensed"] != nil {
		t.Errorf("edge assets are not claimed licensed (not read from code): %v", rows["edge_assets_in_sources"])
	}
	routes["GET /api/config"] = cfgRoutes(map[string]any{"threat_exposure": false}, map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{})["GET /api/config"]
	r, _ = mustRun(t, "inspect-environment", routes, `{}`)
	rows, _ = featureRows(t, r)
	if n := fmt.Sprint(rows["threat_exposure"]["licensed_note"]); !strings.Contains(n, "off, and also unlicensed") || strings.Contains(n, "reads on") {
		t.Errorf("an off threat exposure must not say its property reads on: %v", rows["threat_exposure"])
	}
	routes["GET /api/licenses"] = fwdtest.Const(200, map[string]any{"licenseTier": "NSP", "licenseStatus": "ACTIVE"})
	r, _ = mustRun(t, "inspect-environment", routes, `{}`)
	rows, _ = featureRows(t, r)
	if rows["threat_exposure"]["licensed"] != true || rows["threat_exposure"]["licensed_note"] != nil {
		t.Errorf("NSP has the security facet: %v", rows["threat_exposure"])
	}
}

// Every feature row names a property Forward defines (a renamed or retired property would read as not defined by this build forever), and every security row is a feature row.
func TestEnvironmentFeatureListNamesOnlyPropertiesForwardDefines(t *testing.T) {
	if len(knowledge.OrgPropertyNames()) == 0 {
		t.Skip("this build carries no property table")
	}
	for _, name := range skills.FeatureProperties() {
		if _, ok := knowledge.OrgPropertyInfo(name); !ok {
			t.Errorf("%s is not an organization property in the table", name)
		}
	}
	for name, la := range skills.FeatureLevels() {
		info, ok := knowledge.OrgPropertyInfo(name)
		if !ok {
			continue
		}
		if info.Level != la[0] || info.Admin != la[1] {
			t.Errorf("%s: the feature row says level %s, admin %s; Forward's table says level %s, admin %s", name, la[0], la[1], info.Level, info.Admin)
		}
	}
	if n := len(skills.SecurityFeatureProperties()); n < 15 {
		t.Errorf("only %d security rows: a name in the security set no longer matches a feature", n)
	}
}

// The features reference is a markdown table: a blank line inside it ends the table and the rows after it render as text with pipes (also in the public distribution). Every row line of the
// first table must follow another table line.
func TestFeaturesReferenceTableHasNoBlankLineInsideIt(t *testing.T) {
	b, err := os.ReadFile("inspect-environment/reference/features.md")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(b), "\n")
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "| Feature | Property") {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatal("the property-gated table header is gone")
	}
	rows := 0
	for i := start + 1; i < len(lines); i++ {
		if lines[i] == "" {
			for j := i + 1; j < len(lines) && j < i+3; j++ {
				if strings.HasPrefix(lines[j], "| ") && !strings.HasPrefix(lines[j], "| Feature | Probe") {
					t.Errorf("line %d: a blank line ends the table above and line %d is a row without its header", i+1, j+1)
				}
			}
			break
		}
		rows++
	}
	if rows < 18 {
		t.Errorf("only %d table lines before the first blank line: the table was cut", rows)
	}
}
