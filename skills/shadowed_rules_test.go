package skills_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/nqelint"
	"github.com/forwardnetworks/fwdctl/nqeschema"
	"github.com/forwardnetworks/fwdctl/result"
	"github.com/forwardnetworks/fwdctl/skills"
)

func shadowRowsFixture(rows ...map[string]any) []any {
	out := make([]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, r)
	}
	return out
}

func shadowRule(dev, name, action string, idx int, src, dst string) map[string]any {
	rng := func(lo, hi int) []any { return []any{map[string]any{"start": float64(lo), "end": float64(hi)}} }
	return map[string]any{"os": "PAN_OS", "device": dev, "index": float64(idx), "siblings": float64(1), "name": name, "action": action, "isDefault": false, "approximated": false,
		"src4": []any{src}, "dst4": []any{dst}, "src6": []any{}, "dst6": []any{},
		"proto": rng(0, 255), "tpSrc": rng(0, 65535), "tpDst": rng(0, 65535), "icmpType": rng(0, 255), "icmpCode": rng(0, 255),
		"ingress": []any{"inside"}, "egress": []any{"outside"}}
}

// shadowRoutes serves the two queries of the view: the counts (they select 'configured') and the rows (they select 'index').
func shadowRoutes(counts, rows []any) map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/snapshots": fwdtest.Snapshots(fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")),
		"POST /api/nqe": func(_ *http.Request, body []byte) (int, any) {
			items := rows
			if strings.Contains(string(body), "configured:") {
				items = counts
			}
			var q struct {
				Options struct{ Limit, Offset int } `json:"queryOptions"`
			}
			_ = json.Unmarshal(body, &q)
			end := min(q.Options.Offset+q.Options.Limit, len(items))
			if q.Options.Offset > len(items) {
				return 200, map[string]any{"items": []any{}, "totalNumItems": len(items)}
			}
			return 200, map[string]any{"items": items[q.Options.Offset:end], "totalNumItems": len(items)}
		},
	}
}

func TestShadowedRulesViewReportsCandidatesAsFailedWithInferredConfidence(t *testing.T) {
	counts := []any{map[string]any{"os": "PAN_OS", "device": "fw1", "configured": float64(4), "zoned": float64(3)}}
	rows := shadowRowsFixture(shadowRule("fw1", "allow corp", "PERMIT", 0, "10.0.0.0/8", "0.0.0.0/0"), shadowRule("fw1", "allow lab", "PERMIT", 1, "10.1.0.0/16", "192.168.0.0/16"),
		shadowRule("fw1", "block lab", "DENY", 2, "10.1.2.0/24", "192.168.1.0/24"))
	r, srv := mustRun(t, "check-network-compliance", shadowRoutes(counts, rows), `{"network_id":"n1","view":"shadowed_rules"}`)
	if r.Status != result.Failed || r.Confidence != result.Inferred || writes(srv) != 0 {
		t.Fatalf("%s %s writes=%d: %s", r.Status, r.Confidence, writes(srv), r.Finding)
	}
	for _, want := range []string{"2 rule(s) on 1 device(s)", "candidates", "1 redundant", "1 ineffective", "4 PAN-OS rules on 1 devices are configured; 3 carry a zone scope; 3 of those were analyzed"} {
		if !strings.Contains(r.Finding, want) {
			t.Errorf("finding lacks %q: %s", want, r.Finding)
		}
	}
	b := jsonOf(r)
	for _, want := range []string{`"rule":"allow corp"`, `"kind":"redundant"`, `"kind":"ineffective"`, `"rules_without_zone_scope":1`} {
		if !strings.Contains(b, want) {
			t.Errorf("evidence lacks %s in %s", want, b)
		}
	}
	l := strings.Join(r.Limits, " | ")
	for _, want := range []string{"ONE earlier rule", "application, user, URL category or schedule", "a candidate to confirm in the rule base, not proof", "'any' rule", "So an any-to-any rule above others is not detected", "over-approximated", "only vendors whose zone semantics and first-match order were checked", "intrazone or interzone", "whether a disabled rule is listed", "Panorama pre, local and post"} {
		if !strings.Contains(l, want) {
			t.Errorf("limits lack %q: %s", want, l)
		}
	}
}

// Nothing found is never a pass: the method finds only single-rule coverage among rules with a modelled scope.
func TestShadowedRulesViewSaysUnknownWhenNothingIsFound(t *testing.T) {
	counts := []any{map[string]any{"os": "PAN_OS", "device": "fw1", "configured": float64(3), "zoned": float64(2)}}
	rows := shadowRowsFixture(shadowRule("fw1", "web", "PERMIT", 0, "10.0.0.0/8", "172.16.0.0/12"), shadowRule("fw1", "db", "PERMIT", 1, "192.168.0.0/16", "172.16.0.0/12"))
	r, _ := mustRun(t, "check-network-compliance", shadowRoutes(counts, rows), `{"network_id":"n1","view":"shadowed_rules"}`)
	if r.Status != result.Unknown || !strings.Contains(r.Finding, "not proof") {
		t.Fatalf("no finding must be unknown, not ok: %s %s", r.Status, r.Finding)
	}
}

func TestShadowedRulesViewSaysUnknownWhenThereAreNoRules(t *testing.T) {
	r, _ := mustRun(t, "check-network-compliance", shadowRoutes([]any{}, []any{}), `{"network_id":"n1","view":"shadowed_rules"}`)
	if r.Status != result.Unknown || !strings.Contains(r.Finding, "nothing to compare") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
}

func TestShadowedRulesViewCapsTheRowsShownAndSaysSo(t *testing.T) {
	counts := []any{map[string]any{"os": "PAN_OS", "device": "fw1", "configured": float64(11), "zoned": float64(11)}}
	rows := []map[string]any{shadowRule("fw1", "allow all", "PERMIT", 0, "0.0.0.0/0", "0.0.0.0/0")}
	for i := 1; i <= 10; i++ {
		rows = append(rows, shadowRule("fw1", fmt.Sprintf("later %d", i), "PERMIT", i, "10.0.0.0/8", "10.0.0.0/8"))
	}
	r, _ := mustRun(t, "check-network-compliance", shadowRoutes(counts, shadowRowsFixture(rows...)), `{"network_id":"n1","view":"shadowed_rules","limit":3}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "10 rule(s)") || len(r.Omitted) == 0 {
		t.Fatalf("ten findings, three shown, the rest declared omitted: %s %s %+v", r.Status, r.Finding, r.Omitted)
	}
}

// The two queries the view runs lint clean against the offline schema, so a renamed field is caught before a run.
func TestShadowQueriesLintClean(t *testing.T) {
	if !nqeschema.Available() {
		t.Skip("this build carries no schema")
	}
	for name, q := range map[string]string{"rows": skills.ShadowRowsQuery(), "counts": skills.ShadowCountsQuery()} {
		if ds := nqelint.LintWith(q, nqelint.Modules); len(ds) != 0 {
			t.Errorf("%s query: %v", name, ds)
		}
	}
}

// Other vendors' rules are counted and named but never read or compared; rows that do not match the counts say so.
func TestShadowedRulesViewNamesOtherVendorsAndSaysWhenTheReadsDisagree(t *testing.T) {
	counts := []any{map[string]any{"os": "FORTIOS", "device": "fg1", "configured": float64(2), "zoned": float64(2)}}
	r, _ := mustRun(t, "check-network-compliance", shadowRoutes(counts, []any{}), `{"network_id":"n1","view":"shadowed_rules"}`)
	if r.Status != result.Unknown || !strings.Contains(r.Finding, "No PAN-OS device has configured firewall rules") || !strings.Contains(strings.Join(r.Limits, " "), "2 configured rule(s) on 1 device(s) of vendors not yet verified for this analysis were not compared (FORTIOS 2)") {
		t.Fatalf("%s %s %v", r.Status, r.Finding, r.Limits)
	}
	// a PAN-OS device alongside: its rules are compared, the other vendor is named in the detail and the finding
	pan := []any{map[string]any{"os": "PAN_OS", "device": "fw1", "configured": float64(2), "zoned": float64(2)}, counts[0]}
	rows := shadowRowsFixture(shadowRule("fw1", "wide", "PERMIT", 0, "0.0.0.0/0", "0.0.0.0/0"), shadowRule("fw1", "narrow", "PERMIT", 1, "10.0.0.0/8", "10.0.0.0/8"))
	r, _ = mustRun(t, "check-network-compliance", shadowRoutes(pan, rows), `{"network_id":"n1","view":"shadowed_rules"}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "(2 rules on other vendors' devices were not compared)") || !strings.Contains(jsonOf(r), `"vendors_not_compared":{"FORTIOS":2}`) {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	// the counts say two zone-scoped rules, the rows hold one: the two reads disagree
	one := shadowRowsFixture(shadowRule("fw1", "only", "PERMIT", 0, "10.0.0.0/8", "10.0.0.0/8"))
	r, _ = mustRun(t, "check-network-compliance", shadowRoutes(pan[:1], one), `{"network_id":"n1","view":"shadowed_rules"}`)
	if !strings.Contains(strings.Join(r.Limits, " "), "the rows read (1) differ from the zone-scoped rules counted (2)") {
		t.Errorf("limits: %v", r.Limits)
	}
}

// The rows query carries the vendor allowlist, so another vendor's rules cannot use up the row cap, and asks for the number of entries that share a name.
func TestShadowRowsQueryFiltersTheVendorAndCountsSameNameEntries(t *testing.T) {
	q := skills.ShadowRowsQuery()
	for _, want := range []string{"device.platform.os == OS.PAN_OS", "siblings: length(foreach x in entries where x.name == e.name select x)"} {
		if !strings.Contains(q, want) {
			t.Errorf("the rows query lacks %q:\n%s", want, q)
		}
	}
}
