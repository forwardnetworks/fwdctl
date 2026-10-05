package skills_test

import (
	"encoding/json"
	"fmt"
	forward "github.com/forwardnetworks/forward-go-sdk"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

func cchk(id, name, status string, viol int, enabled bool) map[string]any {
	c := map[string]any{"id": id, "name": name, "status": status, "enabled": enabled}
	if viol > 0 {
		c["numViolations"] = viol
	}
	return c
}

// nqeSplit answers the violations query with rows and the scope query (its text contains "scope") with a total.
func nqeSplit(rows []map[string]any, scopeTotal int) fwdtest.Handler {
	return func(_ *http.Request, b []byte) (int, any) {
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		if q, _ := body["query"].(string); strings.Contains(q, "scope") {
			one := []map[string]any{}
			if scopeTotal > 0 {
				one = append(one, map[string]any{"d": "x"})
			}
			return 200, map[string]any{"items": one, "totalNumItems": scopeTotal}
		}
		return 200, map[string]any{"items": rows, "totalNumItems": len(rows)}
	}
}

func comply(t *testing.T, snapState, trigger string, checks []map[string]any, nqe fwdtest.Handler, in string) (result.Result, *fwdtest.Server) {
	routes := map[string]fwdtest.Handler{
		snapsPath:                      fwdtest.Snapshots(fwdtest.Snap("s1", snapState, trigger, "2026-09-01T00:00:00.000Z")),
		"GET /api/snapshots/s1/checks": fwdtest.Const(200, map[string]any{"checks": checks}),
	}
	if nqe != nil {
		routes[nqePath] = nqe
	}
	return mustRun(t, "check-network-compliance", routes, in)
}

const nqeIn = `{"network_id":"n1","snapshot_id":"s1","nqe":{"name":"no-telnet","query":"violations","scope_query":"scope"}}`
const checksIn = `{"network_id":"n1","snapshot_id":"s1","use_checks":true}`

func TestComplianceAllChecksPassIsOK(t *testing.T) {
	r, _ := comply(t, "PROCESSED", "COLLECTION", []map[string]any{cchk("1", "a", "PASS", 0, true), cchk("2", "b", "PASS", 0, true)}, nil, checksIn)
	if r.Status != result.OK || r.Confidence != result.Deterministic {
		t.Fatalf("got %s/%s", r.Status, r.Confidence)
	}
}

func TestComplianceAFailingCheckIsAFailureWithItsViolationCount(t *testing.T) {
	r, _ := comply(t, "PROCESSED", "COLLECTION", []map[string]any{cchk("1", "a", "PASS", 0, true), cchk("2", "no-telnet", "FAIL", 7, true)}, nil, checksIn)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "no-telnet") || r.Evidence[0].Detail["violations"] != int64(7) && r.Evidence[0].Detail["violations"] != 7 {
		t.Fatalf("got %s: %s %v", r.Status, r.Finding, r.Evidence[0].Detail)
	}
}

func TestComplianceUnevaluatedChecksAreUnknownNeverAPass(t *testing.T) {
	for _, st := range []string{"NONE", "PROCESSING", "ERROR", "TIMEOUT", "REQUIRES_ADDITIONAL_SNAPSHOT_PROCESSING"} {
		r, _ := comply(t, "PROCESSED", "COLLECTION", []map[string]any{cchk("1", "a", st, 0, true)}, nil, checksIn)
		if r.Status != result.Unknown || !strings.Contains(strings.Join(r.Limits, " "), "not evaluated") {
			t.Errorf("%s: status %s limits %v", st, r.Status, r.Limits)
		}
	}
}

func TestComplianceNoChecksAtAllIsUnknown(t *testing.T) {
	r, _ := comply(t, "PROCESSED", "COLLECTION", []map[string]any{}, nil, checksIn)
	if r.Status != result.Unknown {
		t.Fatalf("status %s", r.Status)
	}
}

func TestComplianceADisabledCheckIsReportedNotCounted(t *testing.T) {
	r, _ := comply(t, "PROCESSED", "COLLECTION", []map[string]any{cchk("1", "a", "PASS", 0, true), cchk("2", "b", "FAIL", 1, false)}, nil, checksIn)
	if r.Status != result.OK || !strings.Contains(strings.Join(r.Limits, " "), "disabled") {
		t.Fatalf("got %s %v", r.Status, r.Limits)
	}
}

func TestComplianceCheckIDsFilterAndReportMissing(t *testing.T) {
	r, _ := comply(t, "PROCESSED", "COLLECTION", []map[string]any{cchk("1", "a", "PASS", 0, true), cchk("2", "b", "FAIL", 1, true)}, nil,
		`{"network_id":"n1","snapshot_id":"s1","check_ids":["1","9"]}`)
	l := strings.Join(r.Limits, " ")
	if r.Status != result.OK || !strings.Contains(l, "not found") || !strings.Contains(l, "9") {
		t.Fatalf("got %s %v", r.Status, r.Limits)
	}
}

func TestComplianceNQEViolationRowsFailWithABoundedSample(t *testing.T) {
	rows := make([]map[string]any, 8)
	for i := range rows {
		rows[i] = map[string]any{"device": "r"}
	}
	r, _ := comply(t, "PROCESSED", "COLLECTION", nil, nqeSplit(rows, 40), nqeIn)
	if r.Status != result.Failed || len(r.Evidence[0].Detail["sample"].([]forward.NQERecord)) != 5 {
		t.Fatalf("got %s %v", r.Status, r.Evidence[0].Detail)
	}
}

func TestComplianceNQEZeroViolationsWithAPopulatedScopeIsOK(t *testing.T) {
	r, _ := comply(t, "PROCESSED", "COLLECTION", nil, nqeSplit(nil, 40), nqeIn)
	if r.Status != result.OK || !strings.Contains(r.Evidence[0].Summary, "0 violations across 40") {
		t.Fatalf("got %s %v", r.Status, r.Evidence)
	}
}

func TestComplianceNQEZeroViolationsWithAnEmptyScopeIsUnknown(t *testing.T) {
	r, _ := comply(t, "PROCESSED", "COLLECTION", nil, nqeSplit(nil, 0), nqeIn)
	if r.Status != result.Unknown || !strings.Contains(strings.Join(r.Limits, " "), "scope is empty") {
		t.Fatalf("got %s %v", r.Status, r.Limits)
	}
}

func TestComplianceNQEZeroViolationsWithoutAScopeQueryIsUnknown(t *testing.T) {
	r, _ := comply(t, "PROCESSED", "COLLECTION", nil, nqeSplit(nil, 40), `{"network_id":"n1","snapshot_id":"s1","nqe":{"name":"x","query":"violations"}}`)
	if r.Status != result.Unknown || !strings.Contains(strings.Join(r.Limits, " "), "no scope query") {
		t.Fatalf("got %s %v", r.Status, r.Limits)
	}
}

func TestComplianceZeroViolationsOnAPredictedSnapshotIsUnknown(t *testing.T) {
	r, _ := comply(t, "PROCESSED", "PREDICT", nil, nqeSplit(nil, 40), nqeIn)
	if r.Status != result.Unknown || r.Context.State != "predicted" || !strings.Contains(strings.Join(r.Limits, " "), "predicted snapshot") {
		t.Fatalf("got %s state %s %v", r.Status, r.Context.State, r.Limits)
	}
}

func TestComplianceAViolationBeatsAnUnevaluatedPolicy(t *testing.T) {
	r, _ := comply(t, "PROCESSED", "COLLECTION", []map[string]any{cchk("1", "a", "NONE", 0, true), cchk("2", "b", "FAIL", 2, true)}, nil, checksIn)
	if r.Status != result.Failed {
		t.Fatalf("status %s", r.Status)
	}
}

func TestComplianceUnprocessedSnapshotRunsNoQuery(t *testing.T) {
	r, srv := comply(t, "UNPROCESSED", "COLLECTION", nil, nqeSplit(nil, 40), nqeIn)
	if r.Status != result.Unknown || srv.Called("POST", "/api/nqe") {
		t.Fatalf("status %s", r.Status)
	}
}

func TestComplianceNoPolicySourceIsAnErrorResult(t *testing.T) {
	r, _ := mustRun(t, "check-network-compliance", map[string]fwdtest.Handler{}, `{"network_id":"n1"}`)
	if r.Status != result.Error {
		t.Fatalf("status %s", r.Status)
	}
}

// A network can fail hundreds of checks. Every cited check costs a harness tokens (the live Test Drive run was 50 KB),
// so evidence is bounded while the verdict still counts them all.
func TestComplianceHundredsOfFailingChecksAreCountedButOnlyTheWorstAreCited(t *testing.T) {
	var checks []map[string]any
	for i := 1; i <= 300; i++ {
		checks = append(checks, cchk(fmt.Sprint(i), fmt.Sprintf("check-%03d", i), "FAIL", i, true))
	}
	r, _ := comply(t, "PROCESSED", "COLLECTION", checks, nil, checksIn)
	if r.Status != result.Failed || !strings.HasPrefix(r.Finding, "300 policy(ies) violated") {
		t.Fatalf("got %s: %s", r.Status, r.Finding)
	}
	if len(r.Evidence) != 10 {
		t.Fatalf("%d evidence items cited, want 10", len(r.Evidence))
	}
	if r.Evidence[0].Detail["name"] != "check-300" {
		t.Errorf("the worst check is not first: %v", r.Evidence[0].Detail["name"])
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "300 checks failed; the 10 with the most violations are cited") {
		t.Errorf("limits %v", r.Limits)
	}
	if b, _ := json.Marshal(r); len(b) > 12000 {
		t.Errorf("a 300-failure result is %d bytes", len(b))
	}
}

func TestComplianceLimitRaisesTheCitedCount(t *testing.T) {
	var checks []map[string]any
	for i := 1; i <= 30; i++ {
		checks = append(checks, cchk(fmt.Sprint(i), fmt.Sprintf("c%d", i), "FAIL", i, true))
	}
	r, _ := comply(t, "PROCESSED", "COLLECTION", checks, nil, `{"network_id":"n1","snapshot_id":"s1","use_checks":true,"limit":25}`)
	if len(r.Evidence) != 25 {
		t.Errorf("%d cited, want 25", len(r.Evidence))
	}
}

// ERROR and TIMEOUT are checks with no verdict: the read view counts only FAIL as failing and says how many were not evaluated.
func TestComplianceReadViewDoesNotCountErroredChecksAsFailing(t *testing.T) {
	checks := []map[string]any{cchk("1", "a", "FAIL", 3, true), cchk("2", "b", "ERROR", 0, true), cchk("3", "c", "ERROR", 0, true),
		cchk("4", "d", "TIMEOUT", 0, true), cchk("5", "e", "PASS", 0, true)}
	r, _ := comply(t, "PROCESSED", "COLLECTION", checks, nil, `{"network_id":"n1","snapshot_id":"s1","view":"read"}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "1 of 5 checks are failing") || !strings.Contains(r.Finding, "3 not evaluated (2 error, 1 timeout)") {
		t.Fatalf("got %s: %s", r.Status, r.Finding)
	}
}

func TestComplianceReadViewOnlyErroredChecksIsUnknown(t *testing.T) {
	r, _ := comply(t, "PROCESSED", "COLLECTION", []map[string]any{cchk("1", "a", "ERROR", 0, true), cchk("2", "b", "PASS", 0, true)},
		nil, `{"network_id":"n1","snapshot_id":"s1","view":"read"}`)
	if r.Status != result.Unknown {
		t.Fatalf("got %s: %s", r.Status, r.Finding)
	}
}

// check_id alone selects the read view instead of failing with a message about policies.
func TestComplianceCheckIDAloneReadsTheCheck(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		snapsPath:                        fwdtest.Snapshots(fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")),
		"GET /api/snapshots/s1/checks/7": fwdtest.Const(200, map[string]any{"id": "7", "name": "Post", "status": "ERROR", "enabled": true, "description": "Run NQE diff (pre snapshot vs post snapshot)"}),
	}
	r, _ := mustRun(t, "check-network-compliance", routes, `{"network_id":"n1","snapshot_id":"s1","check_id":"7"}`)
	if r.Status != result.Unknown || !strings.Contains(strings.Join(r.Limits, " "), "pre/post diff") {
		t.Fatalf("got %s: %s %v", r.Status, r.Finding, r.Limits)
	}
}

func TestComplianceNQERowsWithAViolationColumnAreNotCountedAsViolations(t *testing.T) {
	rows := []map[string]any{
		{"device": "r1", "violation": true, "Outcome": "Open"},
		{"device": "r2", "violation": false, "Outcome": "Not a Finding"},
		{"device": "r3", "violation": nil, "Outcome": "Not Reviewed"},
	}
	r, _ := comply(t, "PROCESSED", "COLLECTION", nil, nqeSplit(rows, 40), nqeIn)
	if r.Status == result.Failed || r.Status == result.OK {
		t.Fatalf("rows that are not all violations must not give %s: %s", r.Status, r.Finding)
	}
	if strings.Contains(r.Finding, "violated") || !strings.Contains(strings.Join(r.Limits, " "), "not all violations") {
		t.Fatalf("finding %q limits %v", r.Finding, r.Limits)
	}
	// every row a real violation still fails
	all := []map[string]any{{"device": "r1", "violation": true}, {"device": "r2", "violation": true}}
	if r, _ = comply(t, "PROCESSED", "COLLECTION", nil, nqeSplit(all, 40), nqeIn); r.Status != result.Failed {
		t.Fatalf("all-true rows must fail: %s", r.Status)
	}
}
