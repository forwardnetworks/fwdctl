package skills_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/result"
)

func permRun(t *testing.T, counts, rows []any, extra string) result.Result {
	t.Helper()
	r, _ := mustRun(t, "check-network-compliance", shadowRoutes(counts, rows), `{"network_id":"n1","view":"permissive_rules"`+extra+`}`)
	return r
}

func TestPermissiveRulesViewReportsCandidatesAsFailedWithInferredConfidence(t *testing.T) {
	counts := []any{map[string]any{"os": "PAN_OS", "device": "fw1", "configured": float64(4), "zoned": float64(3)}}
	rows := shadowRowsFixture(
		shadowRule("fw1", "open", "PERMIT", 0, "0.0.0.0/0", "0.0.0.0/0"),
		shadowRule("fw1", "tight", "PERMIT", 1, "10.0.0.0/8", "192.0.2.0/24"),
		shadowRule("fw1", "block", "DENY", 2, "0.0.0.0/0", "0.0.0.0/0"),
	)
	r := permRun(t, counts, rows, "")
	if r.Status != result.Failed || r.Confidence != result.Inferred || !strings.Contains(r.Finding, "1 rule(s) on 1 device(s) are permissive candidates: 1 any-source-any-destination-any-service") {
		t.Fatalf("%s %s %s", r.Status, r.Confidence, r.Finding)
	}
	if !strings.Contains(r.Finding, "4 PAN-OS rules on 1 devices are configured; 3 carry a zone scope; 2 of those are permit entries that were analyzed") {
		t.Errorf("the scope is stated: %s", r.Finding)
	}
	ev := jsonOf(r)
	if !strings.Contains(ev, `"rule":"open"`) || strings.Contains(ev, `"rule":"tight"`) || strings.Contains(ev, `"rule":"block"`) {
		t.Errorf("only the open permit is a candidate: %s", ev)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "which the ACL entries do not carry") {
		t.Errorf("limits must say what the entries lack: %v", r.Limits)
	}
}

func TestPermissiveRulesViewIsUnknownWhenNothingIsFoundNeverOK(t *testing.T) {
	counts := []any{map[string]any{"os": "PAN_OS", "device": "fw1", "configured": float64(1), "zoned": float64(1)}}
	r := permRun(t, counts, shadowRowsFixture(shadowRule("fw1", "tight", "PERMIT", 0, "10.0.0.0/8", "192.0.2.0/24")), "")
	if r.Status != result.Unknown || !strings.Contains(r.Finding, "not proof the rule base is tight") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	r = permRun(t, []any{}, []any{}, "")
	if r.Status != result.Unknown || !strings.Contains(r.Finding, "nothing to read") {
		t.Errorf("no rules: %s %s", r.Status, r.Finding)
	}
}

func TestPermissiveRulesViewCountsOtherVendorsAndCapsTheRows(t *testing.T) {
	counts := []any{map[string]any{"os": "PAN_OS", "device": "fw1", "configured": float64(12), "zoned": float64(12)}, map[string]any{"os": "FORTIOS", "device": "fg1", "configured": float64(3), "zoned": float64(3)}}
	var rows []map[string]any
	for i := 0; i < 12; i++ {
		rows = append(rows, shadowRule("fw1", fmt.Sprintf("open%d", i), "PERMIT", i, "0.0.0.0/0", "0.0.0.0/0"))
	}
	r := permRun(t, counts, shadowRowsFixture(rows...), `,"limit":3`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "12 rule(s)") || !strings.Contains(strings.Join(r.Limits, " "), "3 configured rule(s) on vendors not yet verified were not read (FORTIOS 3)") {
		t.Fatalf("%s %s %v", r.Status, r.Finding, r.Limits)
	}
	if n := strings.Count(jsonOf(r), `"tier":`); n != 3 || len(r.Omitted) == 0 {
		t.Errorf("exactly three shown, the rest declared omitted: %d %v", n, r.Omitted)
	}
}

func TestPermissiveRulesViewSaysWhenTheReadsDisagreeAndHowManyRestOnNoAddressSet(t *testing.T) {
	counts := []any{map[string]any{"os": "PAN_OS", "device": "fw1", "configured": float64(4), "zoned": float64(4)}}
	open := shadowRule("fw1", "open", "PERMIT", 0, "0.0.0.0/0", "0.0.0.0/0")
	bare := shadowRule("fw1", "bare", "PERMIT", 1, "0.0.0.0/0", "0.0.0.0/0")
	bare["src4"], bare["dst4"] = []any{}, []any{}
	bare2 := shadowRule("fw1", "bare2", "PERMIT", 2, "0.0.0.0/0", "0.0.0.0/0")
	bare2["src4"], bare2["dst4"] = []any{}, []any{}
	r := permRun(t, counts, shadowRowsFixture(open, bare, bare2), "")
	if !strings.Contains(r.Finding, "2 of them rest on an entry with no address set") || !strings.Contains(jsonOf(r), `"by_basis"`) {
		t.Errorf("the headline counts the lower confidence basis: %s", r.Finding)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "the rows read (3) differ from the zone-scoped rules counted (4)") {
		t.Errorf("a mismatch between the two reads is stated: %v", r.Limits)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "LOOK like any") {
		t.Errorf("the limit says the false-candidate direction: %v", r.Limits)
	}
}
