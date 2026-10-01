package skills_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/knowledge"
	"github.com/forwardnetworks/fwdctl/result"
)

// orgPropRoutes serves a small configuration (a lower-case key per property) and records PUT/DELETE on /api/config/{property}.
func orgPropRoutes(effective, configured map[string]any, written map[string]string) map[string]fwdtest.Handler {
	routes := cfgRoutes(effective, configured, configured, configured, map[string]any{"advanced_reachability_analysis": "ON_DEMAND", "disable_flow_computation": false, "client_package": "2.0", "zz_new_property": 5})
	for _, p := range []string{"advanced_reachability_analysis", "disable_flow_computation", "zz_new_property", "client_package"} {
		p := p
		routes["PUT /api/config/"+p] = func(r *http.Request, _ []byte) (int, any) {
			v := r.URL.Query().Get("value") // the value travels as a query parameter
			written[p] = v
			effective[p] = v
			if v == "true" {
				effective[p] = true
			}
			return 200, effective
		}
		routes["DELETE /api/config/"+p] = func(*http.Request, []byte) (int, any) {
			written[p] = "DELETE"
			delete(effective, p)
			return 204, nil
		}
	}
	return routes
}

func TestOrgPropertyTableClassifiesEveryPropertyOrSaysUnclassified(t *testing.T) {
	names := knowledge.OrgPropertyNames()
	if len(names) < 100 {
		t.Skip("this build carries no property table")
	}
	ok := map[string]bool{"safe": true, "caution": true, "dangerous": true, "unclassified": true}
	unclassified := 0
	for _, n := range names {
		p, _ := knowledge.OrgPropertyInfo(n)
		if !ok[p.Risk] || strings.TrimSpace(p.Reason) == "" {
			t.Errorf("%s has risk %q and reason %q: every property is classified or reported unclassified, with a reason", n, p.Risk, p.Reason)
		}
		if p.Risk == "unclassified" {
			unclassified++
		}
		if p.Risk == "dangerous" && strings.TrimSpace(p.Consequence) == "" {
			t.Errorf("%s is dangerous but says nothing about what breaks", n)
		}
	}
	t.Logf("%d properties, %d unclassified (treated as caution)", len(names), unclassified)
}

func TestListOrgPropertiesShowsRiskAndAnUnknownPropertyIsNeverSafe(t *testing.T) {
	effective := map[string]any{"advanced_reachability_analysis": "ON_DEMAND", "disable_flow_computation": false, "zz_new_property": 5}
	r, _ := mustRun(t, "edit-org-property", orgPropRoutes(effective, map[string]any{}, map[string]string{}), `{}`)
	rows := r.Evidence[0].Detail["properties"].([]map[string]any)
	by := map[string]map[string]any{}
	for _, x := range rows {
		by[x["property"].(string)] = x
	}
	if r.Status != result.OK || by["DISABLE_FLOW_COMPUTATION"]["risk"] != "dangerous" && knowledge.OrgPropertyNames() != nil {
		t.Fatalf("%s %v", r.Status, by["DISABLE_FLOW_COMPUTATION"])
	}
	if by["ZZ_NEW_PROPERTY"]["risk"] != "unclassified" {
		t.Errorf("a property newer than the table must be unclassified, never safe: %v", by["ZZ_NEW_PROPERTY"])
	}
}

func TestSetOrgPropertyDryRunShowsBeforeAfterUndoAndWritesNothing(t *testing.T) {
	if len(knowledge.OrgPropertyNames()) == 0 {
		t.Skip("this build carries no property table")
	}
	effective, written := map[string]any{"advanced_reachability_analysis": "ON_DEMAND"}, map[string]string{}
	r, srv := mustRun(t, "edit-org-property", orgPropRoutes(effective, map[string]any{}, written), `{"property":"ADVANCED_REACHABILITY_ANALYSIS","value":"ASYNC"}`)
	if r.Status != result.OK || r.Mode != result.ModeDryRun || writes(srv) != 0 || len(r.Changes) != 1 {
		t.Fatalf("%s %s writes=%d", r.Status, r.Finding, writes(srv))
	}
	if !strings.Contains(r.Changes[0].Undo, "clear the organization override") || !strings.Contains(strings.Join(r.Limits, " "), "ORGANIZATION-wide") || !strings.Contains(strings.Join(r.Limits, " "), "processed after the change") {
		t.Errorf("undo %q limits %v", r.Changes[0].Undo, r.Limits)
	}
	// a value Forward does not accept is refused with the accepted ones named
	r, srv = mustRun(t, "edit-org-property", orgPropRoutes(effective, map[string]any{}, written), `{"property":"ADVANCED_REACHABILITY_ANALYSIS","value":"SOMETIMES"}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "ASYNC") || writes(srv) != 0 {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
}

func TestDangerousOrgPropertyNeedsConfirmAndThenAppliesAndReadsBack(t *testing.T) {
	if len(knowledge.OrgPropertyNames()) == 0 {
		t.Skip("this build carries no property table")
	}
	effective, written := map[string]any{"disable_flow_computation": false}, map[string]string{}
	r, srv := mustRun(t, "edit-org-property", orgPropRoutes(effective, map[string]any{}, written), `{"property":"DISABLE_FLOW_COMPUTATION","value":"true","apply":true}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "confirm") || writes(srv) != 0 || len(written) != 0 {
		t.Fatalf("a dangerous property is not applied without confirm: %s %s", r.Status, r.Finding)
	}
	r, _ = mustRun(t, "edit-org-property", orgPropRoutes(effective, map[string]any{}, written), `{"property":"DISABLE_FLOW_COMPUTATION","value":"true","apply":true,"confirm":"disable_flow_computation"}`)
	if r.Status != result.OK || !r.Changes[0].Applied || written["disable_flow_computation"] == "" {
		t.Fatalf("%s %s %v", r.Status, r.Finding, written)
	}
}

func TestOrgPropertyRefusesWhatForwardOnlySupportMayChangeAndWhatDoesNotExist(t *testing.T) {
	if len(knowledge.OrgPropertyNames()) == 0 {
		t.Skip("this build carries no property table")
	}
	effective, written := map[string]any{"client_package": "2.0"}, map[string]string{}
	r, srv := mustRun(t, "edit-org-property", orgPropRoutes(effective, map[string]any{}, written), `{"property":"CLIENT_PACKAGE","value":"3.0"}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "Forward support") || writes(srv) != 0 {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	r, _ = mustRun(t, "edit-org-property", orgPropRoutes(effective, map[string]any{}, written), `{"property":"NOT_A_PROPERTY","value":"1"}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "defines no organization property") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
}

func TestUnclassifiedOrgPropertyNeedsConfirmToo(t *testing.T) {
	effective, written := map[string]any{"zz_new_property": 5}, map[string]string{}
	r, _ := mustRun(t, "edit-org-property", orgPropRoutes(effective, map[string]any{}, written), `{"property":"ZZ_NEW_PROPERTY","value":"6","apply":true}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "unclassified") || len(written) != 0 {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
}
