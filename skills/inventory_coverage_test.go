package skills_test

import (
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

// kind cloud_coverage rolls a snapshot's cloud accounts up by setup (accounts, collected, not collected, VPCs, subnets, instances) and says, for each account that was not collected, what
// the setup itself holds about it: an Azure subscription's last connectivity test (none, passing, or an error), an AWS member account's enabled flag and last error. A reason is the setup's
// record, not a diagnosis; an account with a passing test that was still not collected says only that.
func coverageRoutes(accounts []any, setups []any) map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		snapsPath:                            ready("s1"),
		"GET /api/networks/n1/cloudAccounts": fwdtest.Const(200, setups),
		nqePath:                              fwdtest.Const(200, map[string]any{"items": accounts, "totalNumItems": len(accounts)}),
	}
}

func acct(id, setup, cloud string, collected bool, vpcs, subnets, inst int) map[string]any {
	return map[string]any{"Account": map[bool]string{true: "name-" + id, false: ""}[collected], "ID": id, "Cloud": cloud, "Collected": collected, "Cloud setup": setup, "VPCs": vpcs, "Subnets": subnets, "Instances": inst}
}

func TestCloudCoverageRollsUpBySetupAndExplainsUncollectedAzureSubscriptionsFromTheirTests(t *testing.T) {
	setups := []any{map[string]any{"type": "AZURE", "name": "azure-prod", "collect": true, "testResults": map[string]any{
		"s1": map[string]any{"error": "NONE", "testInstant": 1790000000000}, "s2": map[string]any{"error": "AUTHENTICATION_FAILED", "testInstant": 1790000000000}, "s4": map[string]any{"error": "NONE", "testInstant": 1790000000000}}}}
	accounts := []any{acct("s1", "azure-prod", "AZURE", true, 2, 5, 9), acct("s2", "azure-prod", "AZURE", false, 0, 0, 0), acct("s3", "azure-prod", "AZURE", false, 0, 0, 0), acct("s4", "azure-prod", "AZURE", false, 0, 0, 0)}
	r, _ := mustRun(t, "inspect-inventory", coverageRoutes(accounts, setups), `{"network_id":"n1","kind":"cloud_coverage"}`)
	if r.Status != result.OK {
		t.Fatalf("status %s: %s", r.Status, r.Finding)
	}
	if !strings.Contains(r.Finding, "3 of 4") {
		t.Errorf("finding should say 3 of 4 accounts were not collected: %q", r.Finding)
	}
	d := r.Evidence[0].Detail
	rows, _ := d["setups"].([]map[string]any)
	if d["accounts"] != 4 || d["collected"] != 1 || len(rows) != 1 {
		t.Fatalf("detail = %v", d)
	}
	row := rows[0]
	if row["setup"] != "azure-prod" || row["accounts"] != 4 || row["not_collected"] != 3 || row["vpcs"] != 2 || row["subnets"] != 5 || row["instances"] != 9 {
		t.Errorf("setup row = %v", row)
	}
	reasons, _ := row["reasons"].(map[string]int)
	if reasons["no connectivity test result for this subscription"] != 1 || reasons["connectivity test passes; not collected in this snapshot"] != 1 || reasons["connectivity test failed: AUTHENTICATION_FAILED"] != 1 {
		t.Errorf("reasons = %v", reasons)
	}
	un, _ := row["uncollected"].([]map[string]any)
	if len(un) != 3 || un[0]["id"] == "" || un[0]["reason"] == "" {
		t.Errorf("each uncollected account carries its id (its name is empty in the snapshot) and a reason: %v", un)
	}
}

func TestCloudCoverageExplainsUncollectedAWSMemberAccountsFromTheSetup(t *testing.T) {
	setups := []any{map[string]any{"type": "AWS", "name": "aws-main", "collect": true, "assumeRoleInfos": []any{
		map[string]any{"accountId": "111", "accountName": "app", "enabled": true},
		map[string]any{"accountId": "222", "enabled": false},
		map[string]any{"accountId": "333", "enabled": true, "errorMsg": "AccessDenied assuming the role"}}}}
	accounts := []any{acct("111", "aws-main", "AWS", true, 1, 1, 1), acct("222", "aws-main", "AWS", false, 0, 0, 0), acct("333", "aws-main", "AWS", false, 0, 0, 0), acct("444", "aws-main", "AWS", false, 0, 0, 0)}
	r, _ := mustRun(t, "inspect-inventory", coverageRoutes(accounts, setups), `{"network_id":"n1","kind":"cloud_coverage"}`)
	rows, _ := r.Evidence[0].Detail["setups"].([]map[string]any)
	reasons, _ := rows[0]["reasons"].(map[string]int)
	if reasons["disabled in the setup"] != 1 || reasons["enabled; last error: AccessDenied assuming the role"] != 1 || reasons["not in the setup's account list"] != 1 {
		t.Errorf("reasons = %v", reasons)
	}
}

func TestCloudCoverageWithNoUncollectedAccountsIsOKAndNoCloudAtAllIsUnknown(t *testing.T) {
	setups := []any{map[string]any{"type": "AZURE", "name": "azure-prod", "collect": true}}
	r, _ := mustRun(t, "inspect-inventory", coverageRoutes([]any{acct("s1", "azure-prod", "AZURE", true, 1, 1, 1)}, setups), `{"network_id":"n1","kind":"cloud_coverage"}`)
	if r.Status != result.OK || strings.Contains(r.Finding, "NOT") {
		t.Errorf("all collected: %s %s", r.Status, r.Finding)
	}
	r, _ = mustRun(t, "inspect-inventory", coverageRoutes([]any{}, []any{}), `{"network_id":"n1","kind":"cloud_coverage"}`)
	if r.Status != result.Unknown {
		t.Errorf("a network with no cloud accounts has no coverage to report: %s %s", r.Status, r.Finding)
	}
}

// An Azure setup with an explicit subscription selection collects only those; a subscription outside it that the snapshot still lists was left out on purpose, which is a different thing from
// a failure. An empty selection means every discovered subscription, so nothing is excluded then. GCP projects behave the same way.
func TestCloudCoverageSaysWhenAnAccountWasLeftOutOfTheSetupsOwnSelection(t *testing.T) {
	setups := []any{
		map[string]any{"type": "AZURE", "name": "azure-sel", "collect": true, "subscriptionIds": []string{"s1", "s2"}, "discoveredSubscriptionIds": []string{"s1", "s2", "s9"},
			"testResults": map[string]any{"s1": map[string]any{"error": "NONE"}, "s2": map[string]any{"error": "NONE"}}},
		map[string]any{"type": "AZURE", "name": "azure-all", "collect": true, "discoveredSubscriptionIds": []string{"a1", "a2"}},
		map[string]any{"type": "GCP", "name": "gcp-sel", "collect": true, "projectIds": []string{"p1"}, "discoveredProjectIds": []string{"p1", "p2"}},
	}
	accounts := []any{acct("s1", "azure-sel", "AZURE", true, 1, 1, 1), acct("s9", "azure-sel", "AZURE", false, 0, 0, 0), acct("a2", "azure-all", "AZURE", false, 0, 0, 0), acct("p2", "gcp-sel", "GCP", false, 0, 0, 0)}
	r, _ := mustRun(t, "inspect-inventory", coverageRoutes(accounts, setups), `{"network_id":"n1","kind":"cloud_coverage"}`)
	by := map[string]map[string]int{}
	for _, s := range r.Evidence[0].Detail["setups"].([]map[string]any) {
		by[s["setup"].(string)] = s["reasons"].(map[string]int)
	}
	if by["azure-sel"]["not selected: the setup collects only the subscriptions in its subscription list"] != 1 {
		t.Errorf("azure-sel reasons = %v", by["azure-sel"])
	}
	if by["azure-all"]["no connectivity test result for this subscription"] != 1 {
		t.Errorf("an empty selection excludes nothing: %v", by["azure-all"])
	}
	if by["gcp-sel"]["not selected: the setup collects only the projects in its project list"] != 1 {
		t.Errorf("gcp-sel reasons = %v", by["gcp-sel"])
	}
}
