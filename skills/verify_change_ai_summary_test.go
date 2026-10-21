package skills_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

// aiRoutes serves a change set with a prediction, the org properties that switch Forward's AI assist on or off, and the generation routes.
func aiRoutes(props map[string]any, calls *[]string) map[string]fwdtest.Handler {
	r := changeFix{}.routes()
	r["GET /api/config"] = fwdtest.Const(200, props)
	say := func(name, text string) fwdtest.Handler {
		return func(*http.Request, []byte) (int, any) {
			*calls = append(*calls, name)
			return 200, map[string]any{"summary": text}
		}
	}
	r["POST /api/diffs/b1/a1/impact-summary-assists"] = say("impact", "Two BGP sessions are removed and 14 routes change next hop.")
	r["POST /api/diffs/b1/a1/config-summary-assists"] = say("config", "A prefix list is tightened on edge1.")
	r["GET /api/diffs/b1/a1/failure-summary-assist"] = say("failure", "The default-route check fails because edge1 lost its default.")
	return r
}

const aiOn = `{"ai_allowed":"true","predict_ai_assist":"true","predict_failure_summary_assist":"true"}`

func aiProps(t *testing.T, s string) map[string]any {
	t.Helper()
	m := map[string]any{}
	for _, kv := range strings.Split(strings.Trim(s, "{}"), ",") {
		k, v, _ := strings.Cut(kv, ":")
		m[strings.Trim(k, `"`)] = strings.Trim(v, `"`)
	}
	return m
}

// The AI summary of a diff is generated only when asked for (the view is the opt-in), says it is generated text and not a measurement, names the AI the diff content was sent to, and gives the numbers' view to use alongside it.
func TestAISummaryOfAChangeSetImpactIsOptInLabelledAndSourced(t *testing.T) {
	var calls []string
	r, srv := mustRun(t, "verify-change", aiRoutes(aiProps(t, aiOn), &calls), `{"network_id":"n1","change_set_id":"cs1","view":"ai_summary","summary_kind":"impact"}`)
	b, l := jsonOf(r), strings.Join(r.Limits, " ")
	// the one POST is the generation call; nothing in Forward is changed (an AI summary is generated, not stored)
	if r.Status != result.OK || strings.Join(calls, ",") != "impact" || writes(srv) != 1 {
		t.Fatalf("%s %v writes=%d %s", r.Status, calls, writes(srv), b)
	}
	for _, want := range []string{"Two BGP sessions are removed", `"generated_by":"Forward AI"`, `"summary_kind":"impact"`, `"before_snapshot_id":"b1"`, `"after_snapshot_id":"a1"`} {
		if !strings.Contains(b, want) {
			t.Errorf("missing %s in %s", want, b)
		}
	}
	for _, want := range []string{"generated text", "not a measurement", "sent to Forward", "view impact"} {
		if !strings.Contains(l, want) {
			t.Errorf("limits miss %q: %s", want, l)
		}
	}
}

// It works on any two snapshots, not only a change set; each kind calls its own route, and the failure summary uses the failure route.
func TestAISummaryKindsAndPlainSnapshotPairs(t *testing.T) {
	for _, tc := range []struct{ kind, call, text string }{{"config", "config", "prefix list is tightened"}} {
		var calls []string
		r, _ := mustRun(t, "verify-change", aiRoutes(aiProps(t, aiOn), &calls), `{"network_id":"n1","view":"ai_summary","summary_kind":"`+tc.kind+`","before_snapshot_id":"b1","after_snapshot_id":"a1"}`)
		if r.Status != result.OK || strings.Join(calls, ",") != tc.call || !strings.Contains(jsonOf(r), tc.text) {
			t.Errorf("%s: %s %v %s", tc.kind, r.Status, calls, jsonOf(r))
		}
	}
}

// The change-set overview (a name, description and tags Forward proposes) needs only the change set; the failure summary is not in the SDK yet, so it is unknown and says so instead of calling a route by hand.
func TestAISummaryOverviewAndTheFailureSummaryGap(t *testing.T) {
	var calls []string
	r := aiRoutes(aiProps(t, aiOn), &calls)
	r["POST /api/networks/n1/change-sets/cs1/overview-assists"] = func(*http.Request, []byte) (int, any) {
		calls = append(calls, "overview")
		return 200, map[string]any{"name": "Withdraw transit default", "description": "Removes the default from the transit peer.", "tags": []any{"bgp", "edge"}}
	}
	res, _ := mustRun(t, "verify-change", r, `{"network_id":"n1","change_set_id":"cs1","view":"ai_summary","summary_kind":"overview"}`)
	if res.Status != result.OK || strings.Join(calls, ",") != "overview" || !strings.Contains(jsonOf(res), "Withdraw transit default") || !strings.Contains(jsonOf(res), `"tags":["bgp","edge"]`) {
		t.Errorf("overview: %s %v %s", res.Status, calls, jsonOf(res))
	}
	calls = nil
	res, _ = mustRun(t, "verify-change", aiRoutes(aiProps(t, aiOn), &calls), `{"network_id":"n1","change_set_id":"cs1","view":"ai_summary","summary_kind":"failure"}`)
	if res.Status != result.Unknown || len(calls) != 0 || !strings.Contains(strings.Join(res.Limits, " ")+res.Finding, "SDK") {
		t.Errorf("failure: %s %v %s", res.Status, calls, res.Finding)
	}
}

// When Forward's AI is off for the organization the view says so, names the property, and calls nothing (unknown is never a pass).
func TestAISummaryWhenAIIsOffCallsNothingAndNamesTheProperty(t *testing.T) {
	var calls []string
	r, _ := mustRun(t, "verify-change", aiRoutes(aiProps(t, `{"ai_allowed":"false","predict_ai_assist":"true"}`), &calls), `{"network_id":"n1","change_set_id":"cs1","view":"ai_summary","summary_kind":"impact"}`)
	l := strings.Join(r.Limits, " ") + r.Finding
	if r.Status != result.Unknown || len(calls) != 0 || !strings.Contains(l, "AI_ALLOWED") {
		t.Fatalf("%s %v %s", r.Status, calls, l)
	}
	r, _ = mustRun(t, "verify-change", aiRoutes(aiProps(t, `{"ai_allowed":"true","predict_ai_assist":"false"}`), &calls), `{"network_id":"n1","change_set_id":"cs1","view":"ai_summary","summary_kind":"config"}`)
	if r.Status != result.Unknown || len(calls) != 0 || !strings.Contains(strings.Join(r.Limits, " ")+r.Finding, "PREDICT_AI_ASSIST") {
		t.Fatalf("predict_ai_assist property: %s %v", r.Status, calls)
	}
}

// summary_kind belongs to this view only, and an unknown kind is refused by name.
func TestAISummaryInputsBelongToTheView(t *testing.T) {
	var calls []string
	if _, _, err := runSkill(t, "verify-change", aiRoutes(aiProps(t, aiOn), &calls), `{"network_id":"n1","change_set_id":"cs1","view":"impact","summary_kind":"impact"}`); err == nil {
		t.Errorf("summary_kind is rejected by another view")
	}
	if _, _, err := runSkill(t, "verify-change", aiRoutes(aiProps(t, aiOn), &calls), `{"network_id":"n1","change_set_id":"cs1","view":"ai_summary","summary_kind":"poem"}`); err == nil {
		t.Errorf("an unknown summary_kind is refused")
	}
}
