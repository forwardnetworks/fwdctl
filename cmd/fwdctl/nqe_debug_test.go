package main

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
)

// `fwdctl nqe explain` prints Forward's own optimized plan for a saved query (the Query Debug Panel's text: parallel_foreach, totality and cardinality marks); `nqe metrics` lists the slow query runs Forward
// recorded. Both are Forward's debug features, so they need its permissions and say so when missing.
func TestNqeExplainPrintsTheOptimizedPlanOfASavedQuery(t *testing.T) {
	var sawParam bool
	r := map[string]fwdtest.Handler{
		"POST /api/internal/nqe/queries/Q_abc/explain": func(_ *http.Request, b []byte) (int, any) {
			sawParam = strings.Contains(string(b), `"threshold"`)
			return 200, []byte("parallel_foreach device in network.devices\n select {}")
		},
	}
	code, out, _ := call(t, []string{"nqe", "explain", "--query-id", "Q_abc", "--param", "threshold=3"}, "", r)
	if code != 0 || !strings.Contains(out, "parallel_foreach") || !sawParam {
		t.Fatalf("code %d out %q param %v", code, out, sawParam)
	}
	if code, _, errs := call(t, []string{"nqe", "explain"}, "", r); code != usage || !strings.Contains(errs, "--query-id") {
		t.Errorf("no query: %d %s", code, errs)
	}
}

func TestNqeMetricsListsSlowRunsAndExplainsAMissingPermission(t *testing.T) {
	r := map[string]fwdtest.Handler{
		"GET /api/nqe-run-metrics": fwdtest.Const(200, []any{map[string]any{"taskId": "1", "networkId": "n1", "networkName": "lab", "snapshotId": "s1", "executedAt": 1790000000000, "runDuration": 42000, "queryId": "Q_x", "sourceSetId": "NqeSourceSetId_1", "trackingId": "t"}}),
	}
	code, out, _ := call(t, []string{"nqe", "metrics", "--min-seconds", "5"}, "", r)
	if code != 0 || !strings.Contains(out, `"run_seconds": 42`) || !strings.Contains(out, "Q_x") {
		t.Fatalf("code %d out %s", code, out)
	}
	denied := map[string]fwdtest.Handler{"GET /api/nqe-run-metrics": fwdtest.Const(403, map[string]any{"message": "Missing permission: OrgOperation.DEBUG_NQE"})}
	if code, _, errs := call(t, []string{"nqe", "metrics"}, "", denied); code == 0 || !strings.Contains(errs, "DEBUG_NQE") {
		t.Errorf("a missing permission should be named: %d %s", code, errs)
	}
}

// `nqe explain --file` explains query text by saving it as a throwaway draft in the caller's own workspace (Forward explains only saved queries). It is a dry run until --apply, and it always names the draft
// path it wrote and removes the draft again, so nothing is left in the library.
func TestNqeExplainFileSavesADraftExplainsItAndDiscardsIt(t *testing.T) {
	src := "foreach d in network.devices select {n: d.name}"
	dir := t.TempDir()
	file := dir + "/q.nqe"
	if err := os.WriteFile(file, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls []string
	var added string
	r := map[string]fwdtest.Handler{
		"GET /api/users/current/nqe/changes": func(r *http.Request, _ []byte) (int, any) {
			calls = append(calls, "get-draft")
			return 404, map[string]any{"message": "No draft query at " + r.URL.Query().Get("path")}
		},
		"POST /api/users/current/nqe/changes": func(r *http.Request, b []byte) (int, any) {
			calls = append(calls, "add "+r.URL.Query().Get("action"))
			added = string(b)
			return 204, nil
		},
		"POST /api/internal/users/current/nqe/queries/explain": func(r *http.Request, _ []byte) (int, any) {
			calls = append(calls, "explain "+r.URL.Query().Get("path"))
			return 200, []byte("parallel_foreach device in network.devices")
		},
		"DELETE /api/users/current/nqe/changes": func(r *http.Request, _ []byte) (int, any) {
			calls = append(calls, "discard "+r.URL.Query().Get("path"))
			return 204, nil
		},
	}
	code, out, errs := call(t, []string{"nqe", "explain", "--file", file}, "", r)
	if code != 0 || len(calls) != 0 || !strings.Contains(errs+out, "dry run") || !strings.Contains(errs+out, "--apply") {
		t.Fatalf("dry run must write nothing: code %d calls %v out %q err %q", code, calls, out, errs)
	}
	code, out, errs = call(t, []string{"nqe", "explain", "--file", file, "--apply"}, "", r)
	if code != 0 || !strings.Contains(out, "parallel_foreach") {
		t.Fatalf("apply: code %d out %q err %q", code, out, errs)
	}
	if len(calls) != 4 || calls[1] != "add addQuery" || !strings.HasPrefix(calls[2], "explain /fwdctl-explain-") || !strings.HasPrefix(calls[3], "discard /fwdctl-explain-") || !strings.Contains(added, "network.devices") {
		t.Errorf("calls %v body %s", calls, added)
	}
	if !strings.Contains(errs, "/fwdctl-explain-") || !strings.Contains(errs, "discarded") {
		t.Errorf("the draft path and its removal must be reported: %q", errs)
	}
	if code, _, errs := call(t, []string{"nqe", "explain", "--file", file, "--query-id", "Q_abc"}, "", r); code != usage || !strings.Contains(errs, "--file") {
		t.Errorf("--file with --query-id: %d %s", code, errs)
	}
}

func TestNqeExplainFileRefusesToOverwriteAnExistingDraft(t *testing.T) {
	file := t.TempDir() + "/q.nqe"
	if err := os.WriteFile(file, []byte("foreach d in network.devices select {n: d.name}"), 0o600); err != nil {
		t.Fatal(err)
	}
	var wrote bool
	r := map[string]fwdtest.Handler{
		"GET /api/users/current/nqe/changes":  fwdtest.Const(200, map[string]any{"path": "/x", "sourceCode": "old"}),
		"POST /api/users/current/nqe/changes": func(*http.Request, []byte) (int, any) { wrote = true; return 204, nil },
	}
	code, _, errs := call(t, []string{"nqe", "explain", "--file", file, "--apply", "--draft-path", "/x"}, "", r)
	if code == 0 || wrote || !strings.Contains(errs, "/x") {
		t.Errorf("an existing draft must not be replaced: code %d wrote %v err %q", code, wrote, errs)
	}
}

func TestNqeExplainFileDiscardsTheDraftWhenTheExplainFailsAndNamesItWhenTheDiscardFails(t *testing.T) {
	file := t.TempDir() + "/q.nqe"
	if err := os.WriteFile(file, []byte("foreach d in network.devices select {n: d.name}"), 0o600); err != nil {
		t.Fatal(err)
	}
	var discarded bool
	r := map[string]fwdtest.Handler{
		"GET /api/users/current/nqe/changes":                   fwdtest.Const(404, map[string]any{"message": "No draft query"}),
		"POST /api/users/current/nqe/changes":                  fwdtest.Const(204, nil),
		"POST /api/internal/users/current/nqe/queries/explain": fwdtest.Const(400, map[string]any{"message": "does not compile"}),
		"DELETE /api/users/current/nqe/changes":                func(*http.Request, []byte) (int, any) { discarded = true; return 204, nil },
	}
	if code, _, _ := call(t, []string{"nqe", "explain", "--file", file, "--apply"}, "", r); code != 3 || !discarded {
		t.Errorf("a failed explain must still discard the draft: code %d discarded %v", code, discarded)
	}
	r["DELETE /api/users/current/nqe/changes"] = fwdtest.Const(500, map[string]any{"message": "boom"})
	r["POST /api/internal/users/current/nqe/queries/explain"] = fwdtest.Const(200, []byte("plan"))
	if code, out, errs := call(t, []string{"nqe", "explain", "--file", file, "--apply"}, "", r); code != 0 || !strings.Contains(out, "plan") || !strings.Contains(errs, "could not be discarded") || !strings.Contains(errs, "/fwdctl-explain-") {
		t.Errorf("a failed discard must name the draft: code %d err %q", code, errs)
	}
	if code, _, errs := call(t, []string{"nqe", "explain", "--query-id", "Q_abc", "--apply"}, "", r); code != usage || !strings.Contains(errs, "--file") {
		t.Errorf("--apply without --file: %d %s", code, errs)
	}
}

func TestNqeExplainFileSaysUpFrontWhichParametersAreMissing(t *testing.T) {
	file := t.TempDir() + "/q.nqe"
	src := "@query\nexport q(Device_Name: String) = foreach d in network.devices where d.name == Device_Name select {n: d.name};"
	if err := os.WriteFile(file, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	var wrote bool
	r := map[string]fwdtest.Handler{"POST /api/users/current/nqe/changes": func(*http.Request, []byte) (int, any) { wrote = true; return 204, nil }}
	for _, args := range [][]string{{"nqe", "explain", "--file", file}, {"nqe", "explain", "--file", file, "--apply"}} {
		code, _, errs := call(t, args, "", r)
		if code != usage || wrote || !strings.Contains(errs, "Device_Name") || !strings.Contains(errs, "--param") {
			t.Errorf("%v: missing parameters must be named before any write: code %d wrote %v err %q", args, code, wrote, errs)
		}
	}
}

// The plan marks a lookup as `_lookup(<collection>, "<field>", <expr>)`; explain says on stderr how many each collection got (stdout stays the plan), so a rewrite that did not produce one is seen without grepping.
func TestNqeExplainCountsLookupsPerCollection(t *testing.T) {
	plan := "foreach id#41 in ⸤objectIds#2⸥\n  foreach catalogEntry4\n    in ⸤_lookup(⸢addressObjectsCatalog#1⸣, ⸤\"name\"⸥, ⸤⸤id#41⸥.name⸥)⸥\n  foreach catalogEntry5\n    in ⸤_lookup(dynamicAddressObjectsCatalog, ⸤\"name\"⸥, id#45.name)⸥\n  foreach catalogEntry6\n    in ⸤_lookup(⸢addressObjectsCatalog#1⸣, ⸤\"name\"⸥, id#46.name)⸥\n"
	r := map[string]fwdtest.Handler{"POST /api/internal/nqe/queries/Q_abc/explain": fwdtest.Const(200, []byte(plan))}
	code, out, errs := call(t, []string{"nqe", "explain", "--query-id", "Q_abc"}, "", r)
	if code != 0 || out != plan || !strings.Contains(errs, "addressObjectsCatalog x2") || !strings.Contains(errs, "dynamicAddressObjectsCatalog x1") {
		t.Fatalf("code %d out %q err %q", code, out, errs)
	}
	r = map[string]fwdtest.Handler{"POST /api/internal/nqe/queries/Q_abc/explain": fwdtest.Const(200, []byte("parallel_foreach device in network.devices\n select {}"))}
	if _, _, errs := call(t, []string{"nqe", "explain", "--query-id", "Q_abc"}, "", r); !strings.Contains(errs, "no _lookup") {
		t.Errorf("a plan without a lookup says so: %q", errs)
	}
}
