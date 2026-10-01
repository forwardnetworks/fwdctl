package skills_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
	"github.com/forwardnetworks/fwdctl/skills"
)

// The skills merged in v0.5.27 are views of a surviving skill. Their old names are aliases that fix the view (and rename inputs where they
// differed), so a design or harness that still names them keeps working; these tests pin that, and that a view rejects another view's inputs.

var mergedAliases = map[string]string{
	"review-change-set":      "verify-change",
	"compare-link-overrides": "inspect-topology",
	"find-ip-owner":          "inspect-inventory",
	"find-public-addresses":  "inspect-edge",
	"find-trace-source":      "inspect-edge",
	"plan-author-query":      "author-nqe-query",
}

func TestMergedSkillsAreAliasesAndNoLongerListed(t *testing.T) {
	listed := map[string]bool{}
	for _, n := range skills.Names() {
		listed[n] = true
	}
	for old, to := range mergedAliases {
		if listed[old] {
			t.Errorf("%s is still a listed skill", old)
		}
		if !listed[to] {
			t.Errorf("%s: the surviving skill %s is not listed", old, to)
		}
		if got := skills.Aliases()[old]; got != to {
			t.Errorf("alias %s -> %q, want %s", old, got, to)
		}
		if m, err := skills.Describe(old); err != nil || m.Name != to {
			t.Errorf("describe %s: %v %v", old, m.Name, err)
		}
	}
}

func TestVerifyChangeDescribeViewEqualsTheOldReviewChangeSet(t *testing.T) {
	preds := []any{map[string]any{"id": "p1", "state": "PROCESSED", "createdAt": "2026-09-30T00:00:00Z"}}
	routes := func() map[string]fwdtest.Handler {
		return csRoutes(preds, map[string]any{"checks": []any{csChk("c1", "PASS", 0)}}, map[string]any{"checks": []any{csChk("c1", "FAIL", 2)}})
	}
	byView, _ := mustRun(t, "verify-change", routes(), `{"network_id":"n1","change_set_id":"cs1","view":"describe"}`)
	byAlias, _ := mustRun(t, "review-change-set", routes(), `{"network_id":"n1","change_set_id":"cs1"}`)
	if byView.Skill != "verify-change" || byView.Status != result.Failed || byView.Finding != byAlias.Finding {
		t.Fatalf("view and alias differ: %s %s | %s %s", byView.Status, byView.Finding, byAlias.Status, byAlias.Finding)
	}
	// describe needs a change set and takes none of the compare inputs; the compare views take no device.
	for _, bad := range []string{
		`{"network_id":"n1","view":"describe"}`,
		`{"network_id":"n1","change_set_id":"cs1","view":"describe","expectations":[]}`,
		`{"network_id":"n1","change_set_id":"cs1","view":"describe","run_predict":true}`,
		`{"network_id":"n1","change_set_id":"cs1","device":"fw1"}`,
		`{"network_id":"n1","change_set_id":"cs1","view":"impact","device":"fw1"}`,
	} {
		if _, _, err := runSkill(t, "verify-change", routes(), bad); err == nil {
			t.Errorf("%s must be refused", bad)
		}
	}
	_, _, err := runSkill(t, "verify-change", routes(), `{"network_id":"n1","change_set_id":"cs1","view":"describe","run_predict":true}`)
	if err == nil || !strings.Contains(err.Error(), "run_predict") {
		t.Errorf("the error names the foreign input: %v", err)
	}
	// the old spelling of the default view still works
	if _, _, err := runSkill(t, "verify-change", routes(), `{"network_id":"n1","view":"verdict"}`); err != nil && strings.Contains(err.Error(), "view must be") {
		t.Errorf("view verdict is the default view: %v", err)
	}
}

func TestInspectTopologyCompareToSnapshotIsTheOverridesDiff(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		snapsPath: fwdtest.Snapshots(fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z"), fwdtest.Snap("s2", "PROCESSED", "COLLECTION", "2026-09-02T00:00:00.000Z")),
		"GET /api/snapshots/s1/topology/overrides": fwdtest.Const(200, map[string]any{"present": []any{lk("a et1", "b et1")}, "absent": []any{}}),
		"GET /api/snapshots/s2/topology/overrides": fwdtest.Const(200, map[string]any{"present": []any{lk("a et1", "b et1"), lk("c et1", "d et1")}, "absent": []any{}}),
	}
	r, _ := mustRun(t, "inspect-topology", routes, `{"network_id":"n1","kind":"link_overrides","snapshot_id":"s2","compare_to_snapshot_id":"s1"}`)
	if r.Skill != "inspect-topology" || !strings.Contains(r.Finding, "snapshot s1 (1) to snapshot s2 (2): 1 added, 0 removed, 0 changed") {
		t.Fatalf("%s %s", r.Skill, r.Finding)
	}
	// snapshot_id defaults to the newest processed snapshot
	newestFirst := map[string]fwdtest.Handler{
		snapsPath: fwdtest.Snapshots(fwdtest.Snap("s2", "PROCESSED", "COLLECTION", "2026-09-02T00:00:00.000Z"), fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")),
		"GET /api/snapshots/s1/topology/overrides": routes["GET /api/snapshots/s1/topology/overrides"],
		"GET /api/snapshots/s2/topology/overrides": routes["GET /api/snapshots/s2/topology/overrides"],
	}
	if d, _ := mustRun(t, "inspect-topology", newestFirst, `{"network_id":"n1","kind":"link_overrides","compare_to_snapshot_id":"s1"}`); !strings.Contains(d.Finding, "snapshot s2 (2)") {
		t.Errorf("the later snapshot defaults to the newest: %s", d.Finding)
	}
	// the old inputs, through the alias, are the same call
	old, _ := mustRun(t, "compare-link-overrides", routes, `{"network_id":"n1","before_snapshot_id":"s1","after_snapshot_id":"s2"}`)
	if old.Finding != r.Finding {
		t.Errorf("alias finding %q differs from %q", old.Finding, r.Finding)
	}
	// without compare_to_snapshot_id it is the summary view as before
	if s, _ := mustRun(t, "inspect-topology", routes, `{"network_id":"n1","kind":"link_overrides","snapshot_id":"s2"}`); !strings.Contains(s.Finding, "link override(s) in snapshot s2") {
		t.Errorf("summary view: %s", s.Finding)
	}
	for _, bad := range []string{
		`{"network_id":"n1","kind":"links","compare_to_snapshot_id":"s1"}`,
		`{"network_id":"n1","kind":"link_overrides","compare_to_snapshot_id":"s1","offset":5}`,
	} {
		if _, _, err := runSkill(t, "inspect-topology", routes, bad); err == nil {
			t.Errorf("%s must be refused", bad)
		}
	}
}

func TestInspectInventoryIPOwnerKindEqualsTheOldFindIPOwner(t *testing.T) {
	byKind, _ := mustRun(t, "inspect-inventory", modelRoutes(), `{"network_id":"n1","kind":"ip_owner","ips":["203.0.113.2","10.0.0.77","8.8.8.8"]}`)
	byAlias, _ := mustRun(t, "find-ip-owner", modelRoutes(), `{"network_id":"n1","ips":["203.0.113.2","10.0.0.77","8.8.8.8"]}`)
	if byKind.Skill != "inspect-inventory" || byKind.Finding != byAlias.Finding || !strings.Contains(byKind.Finding, "1 of 3") {
		t.Fatalf("%s %q | %q", byKind.Skill, byKind.Finding, byAlias.Finding)
	}
	for _, bad := range []string{
		`{"network_id":"n1","kind":"ip_owner"}`,
		`{"network_id":"n1","kind":"ip_owner","ips":["10.0.0.1"],"limit":5}`,
		`{"network_id":"n1","kind":"ip_owner","ips":["10.0.0.1"],"device":"r1"}`,
		`{"network_id":"n1","kind":"devices","ips":["10.0.0.1"]}`,
	} {
		if _, _, err := runSkill(t, "inspect-inventory", modelRoutes(), bad); err == nil {
			t.Errorf("%s must be refused", bad)
		}
	}
}

func TestInspectEdgeViewsEqualTheOldSkillsAndRejectOtherViewsInputs(t *testing.T) {
	// public_addresses
	byView, _ := mustRun(t, "inspect-edge", publicRoutes(true), `{"network_id":"n1","view":"public_addresses","role":"management"}`)
	byAlias, _ := mustRun(t, "find-public-addresses", publicRoutes(true), `{"network_id":"n1","role":"management"}`)
	if byView.Skill != "inspect-edge" || byView.Finding != byAlias.Finding {
		t.Fatalf("public_addresses: %q | %q", byView.Finding, byAlias.Finding)
	}
	// trace_sources
	rows := []map[string]any{{"device": "edge-near"}, {"device": "blackholed"}}
	routes := map[string]fwdtest.Handler{
		snapsPath: ready("s1"),
		nqePath:   fwdtest.Const(200, map[string]any{"items": rows, "totalNumItems": len(rows)}),
		pathsPath: func(r *http.Request, _ []byte) (int, any) {
			if r.URL.Query().Get("from") == "edge-near" {
				return 200, pathsBody(false, "EXACT", path("DELIVERED", "PERMITTED", hop("edge-near", "e0", "e1"), hop("edge1", "e2", "")))
			}
			return 200, pathsBody(false, "EXACT", path("BLACKHOLE", "PERMITTED", hop("blackholed", "e0", "")))
		},
	}
	tv, _ := mustRun(t, "inspect-edge", routes, `{"network_id":"n1","view":"trace_sources","target_ip":"203.0.113.1"}`)
	ta, _ := mustRun(t, "find-trace-source", routes, `{"network_id":"n1","target_ip":"203.0.113.1"}`)
	if tv.Skill != "inspect-edge" || tv.Status != result.OK || tv.Finding != ta.Finding {
		t.Fatalf("trace_sources: %s %q | %q", tv.Status, tv.Finding, ta.Finding)
	}
	// the default view is exits, and a view takes only its own inputs
	if r, _ := mustRun(t, "inspect-edge", modelRoutes(), `{"network_id":"n1"}`); !strings.Contains(modelEv(t, r), "exit_candidates") {
		t.Errorf("the default view is exits")
	}
	for _, bad := range []string{
		`{"network_id":"n1","target_ip":"203.0.113.1"}`,
		`{"network_id":"n1","view":"exits","role":"management"}`,
		`{"network_id":"n1","view":"exits","offset":3}`,
		`{"network_id":"n1","view":"public_addresses","include_owned":true}`,
		`{"network_id":"n1","view":"public_addresses","target_ip":"203.0.113.1"}`,
		`{"network_id":"n1","view":"trace_sources","target_ip":"203.0.113.1","limit":5}`,
		`{"network_id":"n1","view":"trace_sources","target_ip":"203.0.113.1","role":"management"}`,
		`{"network_id":"n1","view":"trace_sources"}`,
		`{"network_id":"n1","view":"nope"}`,
	} {
		if _, _, err := runSkill(t, "inspect-edge", routes, bad); err == nil {
			t.Errorf("%s must be refused", bad)
		}
	}
	_, _, err := runSkill(t, "inspect-edge", routes, `{"network_id":"n1","view":"exits","role":"management"}`)
	if err == nil || !strings.Contains(err.Error(), "role") || !strings.Contains(err.Error(), "public_addresses") {
		t.Errorf("the error names the input and the view it belongs to: %v", err)
	}
}

// The describe of an alias that only renames inputs must not lose an input it was given twice: the new name wins.
func TestAliasRenameKeepsTheNewNameWhenBothAreGiven(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		snapsPath: fwdtest.Snapshots(fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z"), fwdtest.Snap("s2", "PROCESSED", "COLLECTION", "2026-09-02T00:00:00.000Z")),
		"GET /api/snapshots/s1/topology/overrides": fwdtest.Const(200, map[string]any{"present": []any{}, "absent": []any{}}),
		"GET /api/snapshots/s2/topology/overrides": fwdtest.Const(200, map[string]any{"present": []any{}, "absent": []any{}}),
	}
	in, _ := json.Marshal(map[string]any{"network_id": "n1", "before_snapshot_id": "s9", "compare_to_snapshot_id": "s1", "snapshot_id": "s2"})
	r, _ := mustRun(t, "compare-link-overrides", routes, string(in))
	if !strings.Contains(r.Finding, "snapshot s1 and snapshot s2") {
		t.Errorf("%s", r.Finding)
	}
}
