package skills_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/fwdtest"
)

func historyRoutes(applied *[]string) map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/change-sets/c1/commits": fwdtest.Const(200, []any{
			map[string]any{"commitId": "k1", "note": "first", "committedAt": "2026-10-01T00:00:00Z", "deviceToChanges": map[string]any{"r1": map[string]any{}}},
			map[string]any{"commitId": "k2", "note": "second", "committedAt": "2026-10-02T00:00:00Z", "deviceToChanges": map[string]any{"r1": map[string]any{}, "r2": map[string]any{}}}}),
		"GET /api/networks/n1/change-sets/c1":                              fwdtest.Const(200, map[string]any{"name": "cs", "snapshotId": "s1", "tags": []any{"wan"}}),
		"GET /api/networks/n1/change-set-directories":                      fwdtest.Const(200, map[string]any{"/team/wan": []any{"c1"}}),
		"GET /api/networks/n1/change-sets":                                 fwdtest.Const(200, []any{map[string]any{"id": "c1", "name": "cs", "snapshotId": "s1"}}),
		"GET /api/networks/n1/change-sets/c1/draft/devices/r1/config":      func(*http.Request, []byte) (int, any) { return 200, []byte("hostname r1\ninterface Gi1\n") },
		"GET /api/networks/n1/change-sets/c1/commits/k1/devices/r1/config": func(*http.Request, []byte) (int, any) { return 200, []byte("hostname r1-old\n") },
		"GET /api/internal/snapshots/p1/protocol-modeling":                 fwdtest.Const(200, []any{"r2", "r1"}),
		"POST /api/networks/n1/change-sets/c1/commits": func(_ *http.Request, _ []byte) (int, any) {
			*applied = append(*applied, "commit")
			return 200, map[string]any{"commitId": "k3"}
		},
		"DELETE /api/networks/n1/change-sets/c1/commits/k1": func(*http.Request, []byte) (int, any) {
			*applied = append(*applied, "delete_commit")
			return 204, nil
		},
	}
}

func TestVerifyChangeHistoryConfigAndAffectedReadAChangeSet(t *testing.T) {
	var applied []string
	routes := historyRoutes(&applied)
	r, _ := mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"history","change_set_id":"c1"}`)
	b := jsonOf(r)
	for _, want := range []string{`"versions_total":2`, `"newest_version":"k2"`, `"directory":"/team/wan"`, `"devices_edited":2`} {
		if !strings.Contains(b, want) {
			t.Errorf("history missing %q: %s", want, b)
		}
	}
	r, _ = mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"config","change_set_id":"c1","device":"r1"}`)
	if b = jsonOf(r); !strings.Contains(b, "hostname r1") || !strings.Contains(b, "the uncommitted draft") {
		t.Errorf("draft config: %s", b)
	}
	r, _ = mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"config","change_set_id":"c1","device":"r1","commit_id":"k1"}`)
	if b = jsonOf(r); !strings.Contains(b, "r1-old") || !strings.Contains(b, "version k1") {
		t.Errorf("committed config: %s", b)
	}
	r, _ = mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"affected","after_snapshot_id":"p1"}`)
	if b = jsonOf(r); !strings.Contains(b, `"count":2`) || strings.Index(b, "r1") > strings.Index(b, "r2") {
		t.Errorf("affected devices, sorted: %s", b)
	}
	if _, _, err := runSkill(t, "verify-change", routes, `{"network_id":"n1","view":"history","change_set_id":"c1","device":"r1"}`); err == nil {
		t.Errorf("device is not an input of view history")
	}
}

func TestEditChangeSetVersionOpsAreDryRunAndDeleteCommitNeedsItsCommitIDAsConfirm(t *testing.T) {
	var applied []string
	routes := historyRoutes(&applied)
	in := func(op, extra string) string {
		return `{"network_id":"n1","change_set_id":"c1","lifecycle":{"op":"` + op + `"` + extra + `}`
	}
	if r, _ := mustRun(t, "edit-change-set", routes, in("commit", `,"note":"ready"`)+`}`); len(applied) != 0 || r.Changes[0].Applied {
		t.Fatalf("a dry run committed: %v", applied)
	}
	mustRun(t, "edit-change-set", routes, in("commit", `,"note":"ready"`)+`,"apply":true}`)
	if len(applied) != 1 || applied[0] != "commit" {
		t.Fatalf("commit not applied: %v", applied)
	}
	// delete_commit: refused without confirm = the commit id, then applied with it
	r, _ := mustRun(t, "edit-change-set", routes, in("delete_commit", `,"commit_id":"k1"`)+`,"apply":true}`)
	if r.Status == "ok" || len(applied) != 1 || !strings.Contains(r.Finding, `confirm="k1"`) {
		t.Fatalf("delete_commit without confirm must be refused: %s %s %v", r.Status, r.Finding, applied)
	}
	mustRun(t, "edit-change-set", routes, in("delete_commit", `,"commit_id":"k1"`)+`,"apply":true,"confirm":"k1"}`)
	if len(applied) != 2 || applied[1] != "delete_commit" {
		t.Fatalf("delete_commit not applied with confirm: %v", applied)
	}
	if _, _, err := runSkill(t, "edit-change-set", routes, in("move_to_directory", `,"directory":"team/wan"`)+`}`); err == nil {
		t.Errorf("a directory path without a leading slash should be refused")
	}
}

func TestEditChangeSetDirectoryOpIsDryRunAndDeletingChangeSetsNeedsConfirm(t *testing.T) {
	var calls []string
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/change-set-directories": fwdtest.Const(200, map[string]any{"/team": []any{"c1", "c2"}}),
	}
	rec := func(*http.Request, []byte) (int, any) { calls = append(calls, "write"); return 204, nil }
	for _, k := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		routes[k+" /api/networks/n1/change-set-directories/team"] = rec
	}
	run := func(extra string) (string, string) {
		r, _ := mustRun(t, "edit-change-set", routes, `{"network_id":"n1","directory_op":{`+extra+`}`+`}`)
		return string(r.Status), r.Finding
	}
	if _, f := run(`"op":"delete","path":"/team","also_delete_change_sets":true`); len(calls) != 0 || !strings.Contains(f, "Dry run") {
		t.Fatalf("dry run wrote: %v %s", calls, f)
	}
	if _, _, err := runSkill(t, "edit-change-set", routes, `{"network_id":"n1","directory_op":{"op":"delete","path":"team"}}`); err == nil {
		t.Errorf("a path without a leading slash should be refused")
	}
	r, _ := mustRun(t, "edit-change-set", routes, `{"network_id":"n1","apply":true,"directory_op":{"op":"delete","path":"/team","also_delete_change_sets":true}}`)
	if len(calls) != 0 || !strings.Contains(r.Finding, `confirm="/team"`) {
		t.Fatalf("deleting the change sets too must need confirm: %v %s", calls, r.Finding)
	}
}

func TestVerifyChangeReadsBGPDiffsObjectsChecksAndCommands(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/change-sets/c1/draft/devices/r1/bgp-advertisements": fwdtest.Const(200, []any{map[string]any{"change": "ADDED"}}),
		"GET /api/nqe/verificationQueries":                                        fwdtest.Const(200, []any{map[string]any{"queryId": "Q_1", "presentInHead": true}}),
		"GET /api/predict-oses":                                                   fwdtest.Const(200, map[string]any{"oses": []any{"IOS_XE", "EOS"}}),
	}
	r, _ := mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"bgp_diffs","change_set_id":"c1","device":"r1"}`)
	if b := jsonOf(r); !strings.Contains(b, `"count":1`) {
		t.Errorf("bgp_diffs: %s", b)
	}
	r, _ = mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"checks","change_set_id":"c1"}`)
	if b := jsonOf(r); !strings.Contains(b, "Q_1") {
		t.Errorf("checks: %s", b)
	}
	r, _ = mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"commands"}`)
	if b := jsonOf(r); !strings.Contains(b, "EOS") || !strings.Contains(b, "IOS_XE") {
		t.Errorf("commands lists the OSes: %s", b)
	}
	if _, _, err := runSkill(t, "verify-change", routes, `{"network_id":"n1","view":"objects","change_set_id":"c1","device":"fw1","object_kind":"bogus"}`); err == nil {
		t.Errorf("an unknown object_kind should be refused")
	}
}

func TestEditChangeSetBulkDeleteNeedsTheCountAsConfirm(t *testing.T) {
	var calls []string
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/change-sets": fwdtest.Const(200, []any{map[string]any{"id": "c1", "name": "a"}, map[string]any{"id": "c2", "name": "b"}}),
	}
	for _, k := range []string{"POST", "DELETE"} {
		routes[k+" /api/networks/n1/change-sets"] = func(*http.Request, []byte) (int, any) { calls = append(calls, "write"); return 204, nil }
	}
	r, _ := mustRun(t, "edit-change-set", routes, `{"network_id":"n1","bulk_delete":["c1","c2"]}`)
	if len(calls) != 0 || !strings.Contains(r.Finding, "Dry run") {
		t.Fatalf("dry run wrote: %v %s", calls, r.Finding)
	}
	r, _ = mustRun(t, "edit-change-set", routes, `{"network_id":"n1","apply":true,"bulk_delete":["c1","c2"]}`)
	if len(calls) != 0 || !strings.Contains(r.Finding, `confirm="2"`) {
		t.Fatalf("needs confirm: %v %s", calls, r.Finding)
	}
	r, _ = mustRun(t, "edit-change-set", routes, `{"network_id":"n1","bulk_delete":["c1","nope"]}`)
	if r.Status != "unknown" {
		t.Errorf("a missing id must give unknown, got %s", r.Status)
	}
}

// An empty command list is explained per OS (PAN-OS and Junos have no command tree), and a 500 from Forward is unknown, not an empty list.
func TestVerifyChangeCommandsExplainsAnEmptyListAndAFailedOne(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		"GET /api/predict-oses/pan_os/allowed-commands":        fwdtest.Const(200, []byte("")),
		"GET /api/predict-oses/juniper_junos/allowed-commands": fwdtest.Const(200, []byte("")),
		"GET /api/predict-oses/arista_eos/allowed-commands":    fwdtest.Const(500, map[string]any{"message": "boom"}),
	}
	for os, want := range map[string]string{"pan_os": "security rules", "juniper_junos": "no command tree"} {
		r, _ := mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"commands","os":"`+os+`"}`)
		if r.Status != "unknown" || !strings.Contains(strings.Join(r.Limits, " "), want) {
			t.Errorf("%s: an empty list should be unknown and say why (%q): %s %v", os, want, r.Status, r.Limits)
		}
	}
	r, _ := mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"commands","os":"arista_eos"}`)
	if r.Status != "unknown" || !strings.Contains(r.Finding, "HTTP 500") || !strings.Contains(strings.Join(r.Limits, " "), "not an empty list") {
		t.Errorf("a 500 is unknown and says it is not an empty list: %s %s %v", r.Status, r.Finding, r.Limits)
	}
}

// After edit-change-set applies, this login has no draft (Forward answers 404 for the draft config). View config without commit_id then reads the
// newest saved version, says so and names its commit id. It falls back ONLY when the draft is truly absent: a draft that exists without a change for
// the device keeps the 404 (a stale saved version must not stand in for it), an explicit commit_id never falls back, and a device the newest version
// lacks keeps the original 404.
func TestChangeSetConfigFallsBackToTheNewestVersionOnlyWhenThereIsNoDraft(t *testing.T) {
	var applied []string
	noDraft := func() map[string]fwdtest.Handler {
		routes := historyRoutes(&applied)
		routes["GET /api/networks/n1/change-sets/c1/draft"] = fwdtest.Const(200, map[string]any{})
		routes["GET /api/networks/n1/change-sets/c1/draft/devices/r1/config"] = fwdtest.Const(404, map[string]any{"message": "No draft exists for change set c1"})
		routes["GET /api/networks/n1/change-sets/c1/draft/devices/r2/config"] = fwdtest.Const(404, map[string]any{"message": "No draft exists for change set c1"})
		routes["GET /api/networks/n1/change-sets/c1/commits/k2/devices/r1/config"] = func(*http.Request, []byte) (int, any) { return 200, []byte("hostname r1-new\n") }
		routes["GET /api/networks/n1/change-sets/c1/commits/k2/devices/r2/config"] = fwdtest.Const(404, map[string]any{"message": "no such device in the version"})
		return routes
	}
	r, _ := mustRun(t, "verify-change", noDraft(), `{"network_id":"n1","view":"config","change_set_id":"c1","device":"r1"}`)
	b := jsonOf(r)
	if r.Status != "ok" || !strings.Contains(b, "hostname r1-new") || !strings.Contains(b, `"commit_id":"k2"`) || !strings.Contains(b, "newest saved version") || !strings.Contains(strings.Join(r.Limits, "|"), "another user") {
		t.Fatalf("fallback to the newest version: %s %s", r.Status, b)
	}
	// the version lacks the device: the draft's own 404 stands
	if _, _, err := runSkill(t, "verify-change", noDraft(), `{"network_id":"n1","view":"config","change_set_id":"c1","device":"r2"}`); err == nil || fwd.Status(err) != 404 {
		t.Errorf("a device the newest version lacks keeps the original 404, got %v", err)
	}
	// a draft exists (without this device): no fallback, the 404 stands and no commit is listed
	withDraft := noDraft()
	withDraft["GET /api/networks/n1/change-sets/c1/draft"] = fwdtest.Const(200, map[string]any{"draft": map[string]any{"name": "cs"}})
	_, srv, err := runSkill(t, "verify-change", withDraft, `{"network_id":"n1","view":"config","change_set_id":"c1","device":"r1"}`)
	if err == nil || fwd.Status(err) != 404 || len(callsMatching(srv, "/commits")) != 0 {
		t.Errorf("an existing draft must not fall back to a saved version: err %v", err)
	}
	// an explicit commit_id reads that version and never looks at drafts
	r, srv = mustRun(t, "verify-change", noDraft(), `{"network_id":"n1","view":"config","change_set_id":"c1","device":"r1","commit_id":"k2"}`)
	if r.Status != "ok" || len(callsMatching(srv, "/draft")) != 0 {
		t.Errorf("an explicit commit_id reads that version only: %s", r.Status)
	}
}

func callsMatching(srv *fwdtest.Server, part string) []fwdtest.Call {
	var out []fwdtest.Call
	for _, c := range srv.Calls() {
		if strings.Contains(c.Path, part) {
			out = append(out, c)
		}
	}
	return out
}
