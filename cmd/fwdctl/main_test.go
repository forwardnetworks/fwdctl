package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

func call(t *testing.T, args []string, stdin string, routes map[string]fwdtest.Handler) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, strings.NewReader(stdin), &out, &errb, func() (*fwd.Session, error) {
		s, _ := fwdtest.New(t, routes)
		return s, nil
	})
	return code, out.String(), errb.String()
}

func routes(outcome string) map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/snapshots": fwdtest.Snapshots(fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")),
		"GET /api/networks/n1/paths": fwdtest.Const(200, map[string]any{"timedOut": false, "info": map[string]any{
			"totalHits": map[string]any{"type": "EXACT", "value": 1},
			"paths":     []map[string]any{{"forwardingOutcome": outcome, "securityOutcome": "PERMITTED", "hops": []map[string]any{{"deviceName": "r1"}}}}}}),
	}
}

const in = `{"network_id":"n1","src_ip":"10.0.0.1","dst_ip":"10.0.1.1"}`

func TestListNamesEverySkillWithAMaturity(t *testing.T) {
	code, out, _ := call(t, []string{"list"}, "", nil)
	var all []map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &all) != nil || len(all) < 4 {
		t.Fatalf("code %d out %.200s", code, out)
	}
	seen := map[string]bool{}
	for _, s := range all {
		seen[s["name"].(string)] = true
	}
	for _, want := range []string{"investigate-reachability", "validate-nqe-query", "author-nqe-query", "plan-investigation"} {
		if !seen[want] {
			t.Errorf("%s missing from list", want)
		}
	}
}

func TestRunPrintsTheEnvelopeAndTheExitCodeFollowsTheStatus(t *testing.T) {
	for outcome, want := range map[string]struct {
		status result.Status
		code   int
	}{"DELIVERED": {result.OK, 0}, "BLACKHOLE": {result.Failed, 1}} {
		code, out, _ := call(t, []string{"run", "investigate-reachability"}, in, routes(outcome))
		var r result.Result
		if err := json.Unmarshal([]byte(out), &r); err != nil {
			t.Fatal(err)
		}
		if p := r.Validate(); len(p) != 0 || r.Status != want.status || code != want.code {
			t.Errorf("%s: status %s code %d problems %v", outcome, r.Status, code, p)
		}
	}
}

func TestUnknownNeverExitsZero(t *testing.T) {
	r := map[string]fwdtest.Handler{"GET /api/networks/n1/snapshots": fwdtest.Snapshots()}
	code, out, _ := call(t, []string{"run", "investigate-reachability"}, in, r)
	if code != 2 || !strings.Contains(out, `"status": "unknown"`) {
		t.Fatalf("code %d out %.200s", code, out)
	}
}

// Bad input is exit 64 and a usage line on stderr; when the input parsed as an object and the name is a skill, stdout also carries an error envelope so a JSON consumer never sees an empty or plain-text stdout.
func TestBadInputAndUnknownSkillAreUsageErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		args  []string
		stdin string
	}{
		"missing required": {[]string{"run", "investigate-reachability"}, `{"network_id":"n1"}`},
		"unknown skill":    {[]string{"run", "nope"}, `{}`},
		"not an object":    {[]string{"run", "investigate-reachability"}, `[1]`},
		"not json":         {[]string{"run", "investigate-reachability"}, `nope`},
		"no command":       {nil, ""},
		"procedure only":   {[]string{"run", "author-nqe-query"}, `{"network_id":"n1"}`},
	} {
		code, out, _ := call(t, tc.args, tc.stdin, map[string]fwdtest.Handler{})
		if code != 64 {
			t.Errorf("%s: code %d out %q", name, code, out)
		}
		if out != "" {
			var res result.Result
			if json.Unmarshal([]byte(out), &res) != nil || res.Status != result.Error {
				t.Errorf("%s: stdout, when present, is an error envelope: %q", name, out)
			}
		}
	}
}

func TestATransportFailureIsAnErrorResultNotAFinding(t *testing.T) {
	r := map[string]fwdtest.Handler{"GET /api/networks/n1/snapshots": fwdtest.Const(503, map[string]any{"message": "down"})}
	code, out, _ := call(t, []string{"run", "investigate-reachability"}, in, r)
	var res result.Result
	if json.Unmarshal([]byte(out), &res) != nil || res.Status != result.Error || code != 3 {
		t.Fatalf("code %d out %.200s", code, out)
	}
}

func TestDescribeCarriesTheProcedureAndContextReturnsExamples(t *testing.T) {
	needCorpora(t)
	code, out, _ := call(t, []string{"describe", "author-nqe-query"}, "", nil)
	if code != 0 || !strings.Contains(out, "DeviceType.ROUTER") {
		t.Errorf("describe: code %d", code)
	}
	code, out, _ = call(t, []string{"context", "nqe", "BGP neighbors not established", "-k", "2"}, "", nil)
	var doc struct{ Examples []any }
	if code != 0 || json.Unmarshal([]byte(out), &doc) != nil || len(doc.Examples) == 0 || len(doc.Examples) > 2 {
		t.Errorf("context: code %d out %.200s", code, out)
	}
}

func TestContextSchemaFindsRealFieldsAndListsEachEnumOnce(t *testing.T) {
	needCorpora(t)
	code, out, _ := call(t, []string{"context", "schema", "cve severity", "-k", "3"}, "", nil)
	var doc struct {
		Fields []struct{ Path string }
		Enums  []struct{ Name string }
	}
	if code != 0 || json.Unmarshal([]byte(out), &doc) != nil || len(doc.Fields) == 0 {
		t.Fatalf("code %d out %.200s", code, out)
	}
	seen := map[string]bool{}
	for _, e := range doc.Enums {
		if seen[e.Name] {
			t.Errorf("enum %s listed twice", e.Name)
		}
		seen[e.Name] = true
	}
}

func TestTheOperationLogIsOmittedUnlessAskedFor(t *testing.T) {
	var without, with result.Result
	_, out, _ := call(t, []string{"run", "investigate-reachability"}, in, routes("DELIVERED"))
	_ = json.Unmarshal([]byte(out), &without)
	_, out, _ = call(t, []string{"run", "investigate-reachability", "--ops"}, in, routes("DELIVERED"))
	_ = json.Unmarshal([]byte(out), &with)
	if len(without.Operations) != 0 {
		t.Errorf("operations present without --ops: %d", len(without.Operations))
	}
	if len(with.Operations) < 2 {
		t.Errorf("--ops returned %d operations", len(with.Operations))
	}
}

func TestVersionSaysWhatItWasBuiltFrom(t *testing.T) {
	code, out, _ := call(t, []string{"version"}, "", nil)
	if code != 0 || !strings.HasPrefix(out, "fwdctl ") || !strings.Contains(out, "commit") {
		t.Errorf("code %d out %q", code, out)
	}
}

func TestHelpEverywhere(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"--help"}, {"-h"}, {"run", "--help"}, {"help", "run"}, {"list", "-h"}, {"run", "inspect-history", "--help"}, {"help", "inspect-history"}} {
		var out, errb bytes.Buffer
		if code := run(args, strings.NewReader(""), &out, &errb, nil); code != 0 || out.Len() == 0 {
			t.Errorf("%v: exit %d, stdout %q stderr %q", args, code, out.String(), errb.String())
		}
	}
	var out bytes.Buffer
	run([]string{"run", "edit-checks", "--help"}, strings.NewReader(""), &out, &bytes.Buffer{}, nil)
	if !strings.Contains(out.String(), "dry run unless") || !strings.Contains(out.String(), "network_id") {
		t.Errorf("a write skill's help must say it is a dry run and list its inputs: %s", out.String())
	}
	var errb bytes.Buffer
	if code := run(nil, strings.NewReader(""), &out, &errb, nil); code != 64 || !strings.Contains(errb.String(), "Usage:") {
		t.Errorf("no arguments must print usage and exit 64, got %d %q", code, errb.String())
	}
}

func TestNQELintIsOfflineAndExitsOneOnASyntaxError(t *testing.T) {
	needCorpora(t)
	var out, errb bytes.Buffer
	if code := run([]string{"nqe", "lint"}, strings.NewReader("foreach d in network.devices select {n: d.name}"), &out, &errb, nil); code != 0 || !strings.Contains(out.String(), `"valid": true`) {
		t.Fatalf("valid query: %d %s %s", code, out.String(), errb.String())
	}
	out.Reset()
	if code := run([]string{"nqe", "lint"}, strings.NewReader("foreach d in network.devices select {"), &out, &errb, nil); code != 1 || !strings.Contains(out.String(), `"valid": false`) {
		t.Fatalf("syntax error: %d %s", code, out.String())
	}
}

func TestBuiltInGuideCoversEveryTopicAndTheBinaryNamesIt(t *testing.T) {
	for _, topic := range []string{"overview", "skills", "nqe", "install", "troubleshooting"} {
		var out, errb bytes.Buffer
		if code := run([]string{"docs", topic}, strings.NewReader(""), &out, &errb, nil); code != 0 || out.Len() < 200 {
			t.Errorf("docs %s: exit %d, %d bytes, %s", topic, code, out.Len(), errb.String())
		}
	}
	var out bytes.Buffer
	run([]string{"docs"}, strings.NewReader(""), &out, &bytes.Buffer{}, nil)
	if !strings.Contains(out.String(), "nqe lint") || !strings.Contains(out.String(), "Exit status") {
		t.Errorf("the overview must name nqe lint and the exit codes")
	}
	out.Reset()
	run([]string{"docs", "nqe"}, strings.NewReader(""), &out, &bytes.Buffer{}, nil)
	for _, want := range []string{"fwdctl nqe lint", "gradual", "validate-nqe-query", "fwdctl context schema"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the nqe topic must mention %q", want)
		}
	}
	if code := run([]string{"docs", "nope"}, strings.NewReader(""), &out, &bytes.Buffer{}, nil); code != 64 {
		t.Errorf("an unknown topic is bad usage, got %d", code)
	}
}

func TestNQEFmtFiltersChecksAndRewrites(t *testing.T) {
	needCorpora(t)
	var out, errb bytes.Buffer
	if code := run([]string{"nqe", "fmt"}, strings.NewReader("foreach d in network.devices select {n:d.name}"), &out, &errb, nil); code != 0 ||
		out.String() != "foreach d in network.devices\nselect { n: d.name }\n" {
		t.Fatalf("filter: %d %q %q", code, out.String(), errb.String())
	}
	dir := t.TempDir()
	f := dir + "/q.nqe"
	os.WriteFile(f, []byte("foreach d in network.devices select {n:d.name}"), 0o644)
	out.Reset()
	if code := run([]string{"nqe", "fmt", "--check", f}, strings.NewReader(""), &out, &errb, nil); code != 1 || !strings.Contains(out.String(), "q.nqe") {
		t.Errorf("--check must list the file and exit 1: %d %q", code, out.String())
	}
	if code := run([]string{"nqe", "fmt", "-w", f}, strings.NewReader(""), &out, &errb, nil); code != 0 {
		t.Errorf("-w: %d", code)
	}
	b, _ := os.ReadFile(f)
	if string(b) != "foreach d in network.devices\nselect { n: d.name }\n" {
		t.Errorf("rewritten: %q", b)
	}
	if code := run([]string{"nqe", "fmt", "--check", f}, strings.NewReader(""), &out, &errb, nil); code != 0 {
		t.Errorf("a formatted file passes --check: %d", code)
	}
	if code := run([]string{"nqe", "fmt"}, strings.NewReader("select ("), &out, &errb, nil); code != 1 {
		t.Errorf("a syntax error is exit 1: %d", code)
	}
}

func TestNQEParamsTakeTypedJSONAndFileThenFlags(t *testing.T) {
	dir := t.TempDir()
	f := dir + "/p.json"
	if err := os.WriteFile(f, []byte(`{"ids":["a","b"],"strict":false,"n":3}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := nqeParams(f, []string{`n=5`, `name=plain text`, `flag=true`})
	if err != nil {
		t.Fatal(err)
	}
	if got["n"] != float64(5) || got["name"] != "plain text" || got["flag"] != true || got["strict"] != false || len(got["ids"].([]any)) != 2 {
		t.Fatalf("%v", got)
	}
	if _, err := nqeParams("", []string{"noequals"}); err == nil {
		t.Error("a --param without = must be refused")
	}
}
