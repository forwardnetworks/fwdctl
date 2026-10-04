package skills_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

const (
	csRoot = "/api/networks/n1/change-sets"
	dcIn   = `{"network_id":"n1","name":"add lo","devices":[{"device":"r1","commands":"interface lo9\n ip address 9.9.9.9 255.255.255.255"}]`
)

var stagedBody string

func dcRoutes(validation map[string]any, existing ...any) map[string]fwdtest.Handler {
	if existing == nil {
		existing = []any{}
	}
	return map[string]fwdtest.Handler{
		snapsPath:        ready1(),
		"GET " + csRoot:  fwdtest.Const(200, map[string]any{"changeSets": existing}),
		"POST " + csRoot: fwdtest.Const(200, map[string]any{"id": "cs7", "name": "add lo", "networkId": "n1", "snapshotId": "s1"}),
		"POST " + csRoot + "/cs7/devices/r1/commands":      fwdtest.Const(200, validation),
		"PUT " + csRoot + "/cs7/draft/devices/r1/commands": func(_ *http.Request, b []byte) (int, any) { stagedBody = string(b); return 200, map[string]any{} },
		"DELETE " + csRoot + "/cs7":                        fwdtest.Const(200, map[string]any{}),
	}
}

func calls(srv *fwdtest.Server, method string) []string {
	var out []string
	for _, c := range srv.Calls() {
		if c.Method == method {
			out = append(out, c.Path)
		}
	}
	return out
}

func TestDraftChangeSetDryRunCreatesNothing(t *testing.T) {
	r, srv := mustRun(t, "edit-change-set", dcRoutes(map[string]any{"commandErrors": []any{}}), dcIn+`}`)
	if r.Status != result.OK || r.Mode != result.ModeDryRun || len(r.Changes) != 1 || r.Changes[0].Applied || writes(srv) != 0 {
		t.Fatalf("%s %s %+v writes=%d", r.Status, r.Mode, r.Changes, writes(srv))
	}
}

func TestDraftChangeSetAppliesValidatedCommandsVerbatimAndDoesNotRunByDefault(t *testing.T) {
	r, srv := mustRun(t, "edit-change-set", dcRoutes(map[string]any{"commandErrors": []any{}}), dcIn+`,"apply":true}`)
	if r.Status != result.OK || r.Mode != result.ModeApplied || !strings.Contains(r.Changes[0].Undo, "cs7") {
		t.Fatalf("%s %+v", r.Status, r.Changes)
	}
	if len(calls(srv, "PUT")) != 1 || len(calls(srv, "DELETE")) != 0 {
		t.Fatalf("one stage, no delete: %+v", srv.Calls())
	}
	for _, p := range calls(srv, "POST") {
		if strings.Contains(p, "run") || strings.Contains(p, "predict") {
			t.Errorf("no prediction without run: %s", p)
		}
	}
	if !strings.Contains(strings.Join(r.Limits, "|"), "not predicted") {
		t.Errorf("%v", r.Limits)
	}
	if !strings.Contains(stagedBody, "ip address 9.9.9.9 255.255.255.255") {
		t.Errorf("commands must be staged as given: %q", stagedBody)
	}
}

func TestDraftChangeSetDeletesTheDraftWhenForwardRejectsTheCommands(t *testing.T) {
	bad := map[string]any{"commandErrors": []any{map[string]any{"lineNumber": 2, "errorType": "INVALID", "errorMsg": "unknown command"}}}
	r, srv := mustRun(t, "edit-change-set", dcRoutes(bad), dcIn+`,"apply":true}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "rejected 1 line") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	if len(calls(srv, "DELETE")) != 1 || len(calls(srv, "PUT")) != 0 {
		t.Fatalf("the draft is removed and nothing staged: %+v", srv.Calls())
	}
	if !strings.Contains(jsonOf(r), "unknown command") || len(r.Changes) != 2 {
		t.Errorf("%s", jsonOf(r))
	}
}

func TestDraftChangeSetLeavesAnExistingNameAloneAndRefusesBadInput(t *testing.T) {
	ex := map[string]any{"id": "cs1", "name": "add lo", "networkId": "n1", "snapshotId": "s1"}
	r, srv := mustRun(t, "edit-change-set", dcRoutes(map[string]any{}, ex), dcIn+`,"apply":true}`)
	if r.Status != result.Unknown || writes(srv) != 0 {
		t.Fatalf("%s writes=%d", r.Status, writes(srv))
	}
	for _, in := range []string{`{"network_id":"n1","name":"x"}`, `{"network_id":"n1","devices":[{"device":"r1","commands":"x"}]}`,
		dcIn + `,"run":true}`, `{"network_id":"n1","name":"x","bgp_advertisements":[{"device":"r1","prefix":"1.1.1.0/24"}]}`} {
		if _, _, err := runSkill(t, "edit-change-set", dcRoutes(map[string]any{}), in); err == nil {
			t.Errorf("must refuse %s", in)
		}
	}
}

func TestDraftChangeSetStagesFirewallRulesAndReadsTheRuleDiffBack(t *testing.T) {
	var added string
	routes := map[string]fwdtest.Handler{
		snapsPath:                           ready("s1"),
		"GET /api/networks/n1/change-sets":  fwdtest.Const(200, []any{}),
		"POST /api/networks/n1/change-sets": fwdtest.Const(200, map[string]any{"id": "cs1", "name": "allow web", "snapshotId": "s1"}),
		"POST /api/networks/n1/change-sets/cs1/devices/fw1/scopes/LOCAL/rulebases/PRIMARY/security-rules": func(_ *http.Request, b []byte) (int, any) {
			added = string(b)
			return 204, nil
		},
		"GET /api/networks/n1/change-sets/cs1/devices/fw1/security-rules-diff": fwdtest.Const(200, map[string]any{"rulebases": []any{}, "entries": []any{map[string]any{"diffType": "ADDED"}}}),
	}
	in := `{"network_id":"n1","name":"allow web","firewall_rules":[{"op":"add","device":"fw1","predecessor":"r0","rule":{"name":"allow-web","action":"ALLOW","enabled":true}}]`
	r, srv := mustRun(t, "edit-change-set", routes, in+`}`)
	if r.Mode != result.ModeDryRun || writes(srv) != 0 || !strings.Contains(jsonOf(r), `"firewall_rules":1`) {
		t.Fatalf("dry run: %s", jsonOf(r))
	}
	r, _ = mustRun(t, "edit-change-set", routes, in+`,"apply":true}`)
	if r.Status != result.OK || !strings.Contains(added, "allow-web") || !strings.Contains(jsonOf(r), `"firewall_rules_diff"`) {
		t.Fatalf("apply: %s added=%s %s", r.Status, added, jsonOf(r))
	}
	for _, bad := range []string{`{"op":"add","device":"fw1"}`, `{"op":"remove","device":"fw1"}`, `{"op":"nope","device":"fw1"}`} {
		if _, _, err := runSkill(t, "edit-change-set", routes, `{"network_id":"n1","name":"x","firewall_rules":[`+bad+`]}`); err == nil {
			t.Errorf("%s must be refused", bad)
		}
	}
}
