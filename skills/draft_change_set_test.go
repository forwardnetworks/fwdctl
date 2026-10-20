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

// The dry run does the cheap checks the apply would fail on: an unlicensed organization is a licence limit (unknown, not ok), and a device that is not in the base snapshot is refused by name. commands may be one string of CLI
// text or an array of lines.
func TestDraftChangeSetDryRunChecksTheLicenceAndTheDevicesExist(t *testing.T) {
	routes := dcRoutes(map[string]any{"commandErrors": []any{}})
	routes["GET "+csRoot] = fwdtest.Const(403, map[string]any{"message": "Unlicensed operation: NetworkOperation.MANAGE_CHANGE_SETS"})
	r, srv := mustRun(t, "edit-change-set", routes, dcIn+`}`)
	if r.Status != result.Unknown || !strings.Contains(strings.Join(r.Limits, " "), "licence limit") || writes(srv) != 0 {
		t.Fatalf("an unlicensed org must not get an ok dry run: %s %s %v", r.Status, r.Finding, r.Limits)
	}
	routes = dcRoutes(map[string]any{"commandErrors": []any{}})
	routes[nqePath] = func(_ *http.Request, b []byte) (int, any) {
		if strings.Contains(string(b), `== \"r1\"`) {
			return 200, map[string]any{"items": []any{map[string]any{"name": "r1"}}, "totalNumItems": 1}
		}
		return 200, map[string]any{"items": []any{}, "totalNumItems": 0}
	}
	if _, _, err := runSkill(t, "edit-change-set", routes, `{"network_id":"n1","name":"x","devices":[{"device":"nope","commands":"interface lo99"}]}`); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("a device that is not in the snapshot must be named: %v", err)
	}
	if r, _ := mustRun(t, "edit-change-set", routes, dcIn+`}`); r.Status != result.OK {
		t.Errorf("an existing device passes: %s %s", r.Status, r.Finding)
	}
	arr := `{"network_id":"n1","name":"add lo","devices":[{"device":"r1","commands":["interface lo9"," ip address 9.9.9.9 255.255.255.255"]}],"apply":true}`
	stagedBody = ""
	r, _ = mustRun(t, "edit-change-set", routes, arr)
	if r.Status != result.OK || !strings.Contains(stagedBody, "interface lo9") {
		t.Errorf("an array of lines is joined with newlines: %s %s staged %q", r.Status, r.Finding, stagedBody)
	}
}

// A BGP advertisement has an action: inject (default) or modify stage it, withdraw stages the loss of a received one (the way to model losing a peer outside the snapshot), restore drops what is staged for the key. A withdraw
// needs only the key (device, vrf, external_peer, prefix); inject and modify need next_hop. The CLI `neighbor ... shutdown` keeps the routes learned from that peer, so staging one warns.
func TestDraftChangeSetBGPActionsAndTheShutdownWarning(t *testing.T) {
	var actions []string
	bgpRoute := func(r *http.Request, b []byte) (int, any) {
		actions = append(actions, r.URL.Query().Get("action")+" "+string(b))
		return 204, nil
	}
	routes := dcRoutes(map[string]any{"commandErrors": []any{}})
	routes["POST "+csRoot+"/cs7/draft/devices/r1/bgp-advertisements"] = bgpRoute
	routes[nqePath] = func(*http.Request, []byte) (int, any) {
		return 200, map[string]any{"items": []any{map[string]any{"name": "r1"}}, "totalNumItems": 1}
	}
	in := func(adv string, extra string) string {
		return `{"network_id":"n1","name":"depeer","bgp_advertisements":[` + adv + `]` + extra + `}`
	}
	wd := `{"action":"withdraw","device":"r1","vrf":"default","external_peer":"192.0.2.1","prefix":"203.0.113.0/24"}`
	if r, _ := mustRun(t, "edit-change-set", routes, in(`{"device":"r1","external_peer":"192.0.2.1","prefix":"203.0.113.0/24","next_hop":"192.0.2.9"}`, "")); !strings.Contains(strings.Join(r.Limits, " "), "not confirmed to take effect") {
		t.Errorf("an inject warns that it may not take effect: %v", r.Limits)
	}
	r, srv := mustRun(t, "edit-change-set", routes, in(wd, ""))
	if r.Mode != result.ModeDryRun || writes(srv) != 0 || !strings.Contains(strings.Join(r.Limits, " "), "received-advertisement collection") {
		t.Fatalf("dry run: %s %v", r.Mode, r.Limits)
	}
	r, _ = mustRun(t, "edit-change-set", routes, in(wd, `,"apply":true`))
	if r.Status != result.OK || len(actions) != 1 || !strings.HasPrefix(actions[0], "bulkRemove ") || !strings.Contains(actions[0], `"externalPeer":"192.0.2.1"`) {
		t.Errorf("withdraw: %s %v", r.Status, actions)
	}
	actions = nil
	rs := `{"action":"restore","device":"r1","vrf":"default","external_peer":"192.0.2.1","prefix":"203.0.113.0/24"}`
	mustRun(t, "edit-change-set", routes, in(rs, `,"apply":true`))
	if len(actions) != 1 || !strings.HasPrefix(actions[0], "bulkRestore ") {
		t.Errorf("restore: %v", actions)
	}
	if _, _, err := runSkill(t, "edit-change-set", routes, in(`{"device":"r1","external_peer":"192.0.2.1","prefix":"203.0.113.0/24"}`, "")); err == nil {
		t.Errorf("an inject needs next_hop")
	}
	if _, _, err := runSkill(t, "edit-change-set", routes, in(`{"action":"bounce","device":"r1","external_peer":"x","prefix":"p"}`, "")); err == nil {
		t.Errorf("an unknown action is refused")
	}
	cli := `{"network_id":"n1","name":"shut","devices":[{"device":"r1","commands":"router bgp 65000\n neighbor 192.0.2.1 shutdown"}]}`
	r, _ = mustRun(t, "edit-change-set", dcRoutes(map[string]any{"commandErrors": []any{}}), cli)
	if !strings.Contains(strings.Join(r.Limits, " "), "routes learned from it stay in the prediction") || !strings.Contains(strings.Join(r.Limits, " "), "withdraw") {
		t.Errorf("a neighbor shutdown warns: %v", r.Limits)
	}
}

// A device can take a whole configuration instead of commands (the "config file" option); a firewall rule can be edited, moved or have its staged change discarded.
func TestDraftChangeSetStagesAWholeConfigAndEditsMovesAndDiscardsFirewallRules(t *testing.T) {
	var saved, edited, moved, discarded string
	const fw = "/api/networks/n1/change-sets/cs1/devices/fw1/scopes/LOCAL/rulebases/PRIMARY/security-rules/"
	routes := map[string]fwdtest.Handler{
		snapsPath:                           ready("s1"),
		"GET /api/networks/n1/change-sets":  fwdtest.Const(200, []any{}),
		"POST /api/networks/n1/change-sets": fwdtest.Const(200, map[string]any{"id": "cs1", "name": "cfg", "snapshotId": "s1"}),
		"PUT /api/networks/n1/change-sets/cs1/draft/devices/r1/config": func(_ *http.Request, b []byte) (int, any) { saved = string(b); return 204, nil },
		"PATCH " + fw + "rule1": func(_ *http.Request, b []byte) (int, any) {
			if strings.Contains(string(b), "predecessor") {
				moved = string(b)
			} else {
				edited = string(b)
			}
			return 204, nil
		},
		"POST " + fw + "rule2": func(r *http.Request, _ []byte) (int, any) { discarded = r.URL.RawQuery; return 204, nil },
		"GET /api/networks/n1/change-sets/cs1/devices/fw1/security-rules-diff": fwdtest.Const(200, map[string]any{"rulebases": []any{}, "entries": []any{}}),
	}
	in := `{"network_id":"n1","name":"cfg","devices":[{"device":"r1","config":"hostname r1\ninterface lo0\n"}],"firewall_rules":[` +
		`{"op":"edit","device":"fw1","rule_id":"rule1","rule":{"name":"allow-web","action":"ALLOW","enabled":true}},` +
		`{"op":"move","device":"fw1","rule_id":"rule1","predecessor":"r0"},` +
		`{"op":"discard","device":"fw1","rule_id":"rule2"}]`
	r, srv := mustRun(t, "edit-change-set", routes, in+`}`)
	if r.Mode != result.ModeDryRun || writes(srv) != 0 || !strings.Contains(strings.Join(r.Limits, " "), "not validated") {
		t.Fatalf("dry run: %s %v", r.Mode, r.Limits)
	}
	r, _ = mustRun(t, "edit-change-set", routes, in+`,"apply":true}`)
	if r.Status != result.OK || !strings.Contains(saved, "interface lo0") || !strings.Contains(edited, "allow-web") || !strings.Contains(moved, "r0") || !strings.Contains(discarded, "discardChanges") {
		t.Fatalf("apply: %s saved=%q edited=%q moved=%q discarded=%q %s", r.Status, saved, edited, moved, discarded, r.Finding)
	}
	for _, bad := range []string{
		`"devices":[{"device":"r1","commands":"x","config":"y"}]`, // not both
		`"devices":[{"device":"r1"}]`,                             // neither
		`"firewall_rules":[{"op":"edit","device":"fw1","rule_id":"a"}]`,
		`"firewall_rules":[{"op":"move","device":"fw1","predecessor":"a"}]`,
		`"firewall_rules":[{"op":"discard","device":"fw1"}]`,
	} {
		if _, _, err := runSkill(t, "edit-change-set", routes, `{"network_id":"n1","name":"x",`+bad+`}`); err == nil {
			t.Errorf("%s must be refused", bad)
		}
	}
}

// lifecycle works on an existing change set: rename or describe it, copy it, delete it (confirmed by its id), throw away one device's uncommitted edits, or restore an earlier version. Each has a dry run.
func TestDraftChangeSetLifecycleOnAnExistingChangeSet(t *testing.T) {
	var patched, dup string
	deleted, restored, discarded := false, false, false
	const cs = "/api/networks/n1/change-sets/cs9"
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/change-sets": fwdtest.Const(200, []any{map[string]any{"id": "cs9", "name": "old name", "snapshotId": "s1"}}),
		"PATCH " + cs: func(_ *http.Request, b []byte) (int, any) {
			patched = string(b)
			return 200, map[string]any{"id": "cs9", "name": "new name"}
		},
		"POST " + cs: func(r *http.Request, b []byte) (int, any) {
			dup = r.URL.RawQuery + string(b)
			return 200, map[string]any{"id": "cs10", "name": "copy"}
		},
		"DELETE " + cs:                       func(*http.Request, []byte) (int, any) { deleted = true; return 204, nil },
		"DELETE " + cs + "/draft/devices/r1": func(*http.Request, []byte) (int, any) { discarded = true; return 204, nil },
		"POST " + cs + "/commits/c3":         func(*http.Request, []byte) (int, any) { restored = true; return 200, map[string]any{"commitId": "c4"} },
	}
	run := func(extra string) result.Result {
		r, _ := mustRun(t, "edit-change-set", routes, `{"network_id":"n1","change_set_id":"cs9",`+extra+`}`)
		return r
	}
	// dry runs change nothing and name the change set
	for _, l := range []string{`"lifecycle":{"op":"rename","name":"new name"}`, `"lifecycle":{"op":"duplicate","name":"copy"}`, `"lifecycle":{"op":"delete"}`, `"lifecycle":{"op":"discard_device","device":"r1"}`, `"lifecycle":{"op":"restore_commit","commit_id":"c3"}`} {
		if r := run(l); r.Mode != result.ModeDryRun || !strings.Contains(jsonOf(r), "cs9") {
			t.Fatalf("dry run %s: %s", l, jsonOf(r))
		}
	}
	if patched != "" || dup != "" || deleted || restored || discarded {
		t.Fatalf("a dry run wrote")
	}
	if r := run(`"lifecycle":{"op":"rename","name":"new name"},"apply":true`); r.Status != result.OK || !strings.Contains(patched, "new name") {
		t.Errorf("rename: %s %s", r.Status, patched)
	}
	if r := run(`"lifecycle":{"op":"duplicate","name":"copy"},"apply":true`); r.Status != result.OK || !strings.Contains(dup, "duplicate") || !strings.Contains(jsonOf(r), "cs10") {
		t.Errorf("duplicate: %s %s", r.Status, dup)
	}
	if r := run(`"lifecycle":{"op":"discard_device","device":"r1"},"apply":true`); r.Status != result.OK || !discarded {
		t.Errorf("discard_device: %s", r.Status)
	}
	if r := run(`"lifecycle":{"op":"restore_commit","commit_id":"c3"},"apply":true`); r.Status != result.OK || !restored {
		t.Errorf("restore_commit: %s", r.Status)
	}
	// delete needs confirm = the change set id
	if r := run(`"lifecycle":{"op":"delete"},"apply":true`); r.Status == result.OK || deleted {
		t.Errorf("delete without confirm must refuse: %s", r.Status)
	}
	if r := run(`"lifecycle":{"op":"delete"},"apply":true,"confirm":"cs9"`); r.Status != result.OK || !deleted {
		t.Errorf("delete: %s", r.Status)
	}
	// lifecycle does not combine with staging content
	if _, _, err := runSkill(t, "edit-change-set", routes, `{"network_id":"n1","change_set_id":"cs9","name":"x","lifecycle":{"op":"delete"},"devices":[{"device":"r1","commands":"x"}]}`); err == nil {
		t.Errorf("lifecycle and devices together must be refused")
	}
	// an unknown change set
	if r, _ := mustRun(t, "edit-change-set", routes, `{"network_id":"n1","change_set_id":"nope","lifecycle":{"op":"delete"}}`); r.Status == result.OK {
		t.Errorf("an unknown change set is not ok: %s", r.Status)
	}
}

// A base snapshot that is being (re)processed cannot carry a change set that runs: Forward stages it, reports success and never produces a predicted snapshot. An explicit snapshot_id that is not PROCESSED is refused (unknown) in the dry run and on apply, and says to wait.
func TestDraftChangeSetRefusesABaseSnapshotThatIsNotProcessed(t *testing.T) {
	routes := dcRoutes(map[string]any{"commandErrors": []any{}})
	routes[snapsPath] = fwdtest.Snapshots(fwdtest.Snap("s1", "PROCESSING", "COLLECTION", "2026-09-01T00:00:00.000Z"))
	for _, extra := range []string{``, `,"apply":true,"run":true`} {
		r, srv := mustRun(t, "edit-change-set", routes, `{"network_id":"n1","snapshot_id":"s1","name":"x","devices":[{"device":"r1","commands":"hostname r1"}]`+extra+`}`)
		if r.Status != result.Unknown || writes(srv) != 0 || !strings.Contains(strings.Join(r.Limits, " ")+r.Finding, "PROCESSING") {
			t.Errorf("a base that is processing: %s %s %v writes=%d", r.Status, r.Finding, r.Limits, writes(srv))
		}
	}
}

// With run requested, a result that has no predicted snapshot id says so in the finding itself (not only in a limit), and names where to read it later: a prediction that takes longer than the wait finishes afterwards, and a first result that
// reads like success would be trusted.
func TestDraftChangeSetRunWithoutAPredictedSnapshotSaysItIsNotPredictedYet(t *testing.T) {
	routes := dcRoutes(map[string]any{"commandErrors": []any{}})
	routes["GET "+csRoot+"/cs7/predicted-snapshots"] = fwdtest.Const(200, map[string]any{"predictedSnapshots": []any{}})
	routes["POST "+csRoot+"/cs7"] = fwdtest.Const(200, map[string]any{"id": "p1", "state": "PROCESSING"}) // started, but the snapshot cannot be polled
	r, _ := mustRun(t, "edit-change-set", routes, dcIn+`,"apply":true,"run":true}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "NOT predicted yet") || !strings.Contains(r.Finding, "verify-change view list") || !strings.Contains(r.Finding, "snapshot p1") {
		t.Fatalf("finding: %s %s", r.Status, r.Finding)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "did not finish") {
		t.Errorf("limits: %v", r.Limits)
	}
}

// A firewall rule's address, user, application and service fields are objects ({"subnets": [...]}; {} is any), not lists. Giving a list used to end in an opaque "cannot unmarshal array into ... AddressSpecification"; the error now says what shape each field takes.
func TestDraftChangeSetFirewallRuleListInAnObjectFieldSaysTheShape(t *testing.T) {
	in := `{"network_id":"n1","name":"x","firewall_rules":[{"op":"add","device":"fw1","rule":{"name":"deny","action":"DENY","enabled":true,"sourceAddresses":["10.0.0.0/8"]}}]}`
	_, _, err := runSkill(t, "edit-change-set", map[string]fwdtest.Handler{snapsPath: ready("s1")}, in)
	if err == nil || !strings.Contains(err.Error(), `{"subnets":["8.8.8.8/32"]}`) || !strings.Contains(err.Error(), "{} means any") {
		t.Fatalf("the error should show the shape: %v", err)
	}
}

// A rejected command line is named in the finding itself (its number, its text and Forward's message), not only in the evidence, so a caller sees which line to fix.
func TestDraftChangeSetFindingNamesTheRejectedLineAndTheParserMessage(t *testing.T) {
	bad := map[string]any{"commandErrors": []any{map[string]any{"lineNumber": 2, "errorType": "INVALID", "errorMsg": "expected a number after cost"}}}
	in := `{"network_id":"n1","name":"ospf cost","apply":true,"devices":[{"device":"r1","commands":"interface Ethernet1\n no ip ospf cost\n"}]}`
	r, _ := mustRun(t, "edit-change-set", dcRoutes(bad), in)
	if r.Status != result.Failed || !strings.Contains(r.Finding, `line 2: "no ip ospf cost"`) || !strings.Contains(r.Finding, "expected a number after cost") {
		t.Fatalf("finding: %s", r.Finding)
	}
}

// A withdraw says in the finding that it only changes the prediction for a peer outside the snapshot, since a peer that is itself a collected device needs none and a withdraw from it is a no-op that otherwise reports ok.
func TestDraftChangeSetWithdrawFindingNamesTheNoOpCase(t *testing.T) {
	routes := dcRoutes(map[string]any{"commandErrors": []any{}})
	routes["POST "+csRoot+"/cs7/draft/devices/r1/bgp-advertisements"] = func(*http.Request, []byte) (int, any) { return 204, nil }
	in := `{"network_id":"n1","name":"w","apply":true,"bgp_advertisements":[{"action":"withdraw","device":"r1","vrf":"default","external_peer":"192.0.2.1","prefix":"203.0.113.0/24"}]}`
	r, _ := mustRun(t, "edit-change-set", routes, in)
	if !strings.Contains(r.Finding, "only changes the prediction for a peer outside the snapshot") {
		t.Errorf("finding: %s", r.Finding)
	}
}

// With run requested the result names the predicted snapshot as soon as it was started, even if the wait ended before it finished, and says why: still running, or ended in a failed state.
func TestDraftChangeSetRunNamesTheStartedPredictedSnapshotAndItsEnd(t *testing.T) {
	for _, tc := range []struct{ state, want string }{
		{"PROCESSED", "predicted as snapshot p1"},
		{"FAILED", "snapshot p1 ended FAILED"},
	} {
		routes := dcRoutes(map[string]any{"commandErrors": []any{}})
		routes["GET "+csRoot+"/cs7/predicted-snapshots"] = fwdtest.Const(200, map[string]any{"predictedSnapshots": []any{}})
		routes["POST "+csRoot+"/cs7"] = fwdtest.Const(200, map[string]any{"id": "p1", "state": "PROCESSING"})
		routes[snapsPath] = fwdtest.Snapshots(fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z"), fwdtest.Snap("p1", tc.state, "PREDICT", "2026-09-02T00:00:00.000Z"))
		r, _ := mustRun(t, "edit-change-set", routes, dcIn+`,"apply":true,"run":true}`)
		if !strings.Contains(r.Finding, tc.want) {
			t.Errorf("%s: finding %q", tc.state, r.Finding)
		}
	}
}

// A leading "configure terminal" (or "conf t") is dropped, since the commands are applied in configuration mode already and Forward rejects it as an invalid command; the result says it was dropped.
func TestDraftChangeSetDropsALeadingConfigureTerminal(t *testing.T) {
	routes := dcRoutes(map[string]any{"commandErrors": []any{}})
	var staged string
	routes["PUT "+csRoot+"/cs7/draft/devices/r1/commands"] = func(_ *http.Request, b []byte) (int, any) { staged = string(b); return 200, map[string]any{} }
	in := `{"network_id":"n1","name":"patch","apply":true,"devices":[{"device":"r1","commands":"configure terminal\ninterface Ethernet1\n description x\n"}]}`
	r, _ := mustRun(t, "edit-change-set", routes, in)
	if r.Status != result.OK || strings.Contains(staged, "configure terminal") || !strings.Contains(staged, "interface Ethernet1") || !strings.Contains(strings.Join(r.Limits, " "), "dropped the leading") {
		t.Fatalf("staged %q, limits %v", staged, r.Limits)
	}
}

// view list reads each change set's content from its head commit and, where nothing is committed, its draft (the change-set list never carries the per-device changes): devices_edited counts the committed devices (a BGP-only change set counts its devices), and draft_devices the uncommitted ones.
func TestVerifyChangeListShowsDraftDevicesWhenNothingIsCommitted(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/change-sets": fwdtest.Const(200, map[string]any{"changeSets": []any{
			map[string]any{"id": "cs1", "name": "staged", "snapshotId": "s1"}, map[string]any{"id": "cs2", "name": "committed", "snapshotId": "s1"}}}),
		"GET /api/networks/n1/change-sets/cs1/predicted-snapshots": fwdtest.Const(200, map[string]any{"predictedSnapshots": []any{}}),
		"GET /api/networks/n1/change-sets/cs2/predicted-snapshots": fwdtest.Const(200, map[string]any{"predictedSnapshots": []any{}}),
		"GET /api/networks/n1/change-sets/cs1/commits/head":        fwdtest.Const(404, map[string]any{"message": "no commit"}),
		"GET /api/networks/n1/change-sets/cs1/draft":               fwdtest.Const(200, map[string]any{"draft": map[string]any{"id": "cs1", "deviceToChanges": map[string]any{"r1": map[string]any{"commands": "x"}, "r2": map[string]any{"removedAdvertisements": []any{map[string]any{"vrf": "default", "externalPeer": "p", "prefix": "10.0.0.0/8"}}}, "r3": map[string]any{"commands": "y"}}}}),
		"GET /api/networks/n1/change-sets/cs2/commits/head":        fwdtest.Const(200, map[string]any{"id": "cs2", "commitId": "c", "deviceToChanges": map[string]any{"r1": map[string]any{"removedAdvertisements": []any{map[string]any{"vrf": "default", "externalPeer": "p", "prefix": "10.0.0.0/8"}}}}}),
		"GET /api/networks/n1/change-sets/cs2/draft":               fwdtest.Const(200, map[string]any{"draft": nil}),
	}
	r, _ := mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"list"}`)
	b := jsonOf(r)
	if !strings.Contains(b, `"draft_devices":3`) || !strings.Contains(b, `"devices_edited":0`) || !strings.Contains(b, `"devices_edited":1`) || !strings.Contains(strings.Join(r.Limits, " "), "COMMITTED") {
		t.Errorf("devices: %s %v", b, r.Limits)
	}
}

// lifecycle op predict starts a prediction of an existing change set and returns the predicted snapshot id at once, without waiting (a wait of minutes to hours can then be done with fwdctl wait snapshot instead of a polling script). A dry run says what it would start.
func TestDraftChangeSetLifecyclePredictStartsAndReturnsAtOnce(t *testing.T) {
	started := 0
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/change-sets": fwdtest.Const(200, []any{map[string]any{"id": "cs9", "name": "withdraw", "snapshotId": "s1"}}),
		"POST /api/networks/n1/change-sets/cs9": func(r *http.Request, _ []byte) (int, any) {
			if r.URL.Query().Get("action") == "predict" {
				started++
			}
			return 200, map[string]any{"id": "p5", "state": "UNPROCESSED"}
		},
	}
	r, srv := mustRun(t, "edit-change-set", routes, `{"network_id":"n1","change_set_id":"cs9","lifecycle":{"op":"predict"}}`)
	if r.Mode != result.ModeDryRun || started != 0 || writes(srv) != 0 || !strings.Contains(r.Finding, "cs9") {
		t.Fatalf("dry run: %s %d %s", r.Mode, started, r.Finding)
	}
	r, _ = mustRun(t, "edit-change-set", routes, `{"network_id":"n1","change_set_id":"cs9","lifecycle":{"op":"predict"},"apply":true}`)
	if r.Status != result.OK || started != 1 || !strings.Contains(r.Finding, "p5") || !strings.Contains(r.Finding, "fwdctl wait snapshot") || !strings.Contains(jsonOf(r), `"predicted_snapshot_id":"p5"`) {
		t.Errorf("apply: %s %d %s", r.Status, started, r.Finding)
	}
}

// Forward can answer a predict request with a snapshot that already existed for the change set (reported live: the id of an earlier PROCESSED prediction, with no state). The finding must not say "Started" then: it says no new prediction was visible, names the existing one, and fills the state from the listing.
func TestLifecyclePredictSaysWhenForwardReturnedAnExistingSnapshot(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/change-sets":                         fwdtest.Const(200, []any{map[string]any{"id": "cs9", "name": "withdraw", "snapshotId": "s1"}}),
		"GET /api/networks/n1/change-sets/cs9/predicted-snapshots": fwdtest.Const(200, map[string]any{"snapshots": []any{map[string]any{"id": "p5", "state": "PROCESSED", "createdAt": "2026-10-20T11:25:58Z"}}}),
		"POST /api/networks/n1/change-sets/cs9":                    fwdtest.Const(200, map[string]any{"id": "p5"}),
	}
	r, _ := mustRun(t, "edit-change-set", routes, `{"network_id":"n1","change_set_id":"cs9","lifecycle":{"op":"predict"},"apply":true}`)
	if strings.Contains(r.Finding, "Started") || !strings.Contains(r.Finding, "already existed") || !strings.Contains(r.Finding, "PROCESSED") || !strings.Contains(jsonOf(r), `"new_prediction":false`) {
		t.Errorf("existing: %s", r.Finding)
	}
}
