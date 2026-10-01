package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/skills"
)

func TestInstallClaudeWritesEverySkillWithValidFrontmatterAndHowToRunIt(t *testing.T) {
	dir := t.TempDir()
	code, out, errb := call(t, []string{"install", "claude", "--dir", dir}, "", nil)
	if code != 0 {
		t.Fatalf("code %d %s", code, errb)
	}
	all, _ := skills.All()
	if !strings.Contains(out, "wrote") {
		t.Errorf("out %q", out)
	}
	for _, m := range all {
		b, err := os.ReadFile(filepath.Join(dir, m.Name, "SKILL.md"))
		if err != nil {
			t.Fatalf("%s not written: %v", m.Name, err)
		}
		text := string(b)
		lines := strings.Split(text, "\n")
		if lines[0] != "---" || lines[1] != "name: "+m.Name || !strings.HasPrefix(lines[2], "description: \"") ||
			!strings.HasPrefix(lines[3], "compatibility: \"") || lines[4] != "---" {
			t.Errorf("%s frontmatter = %q", m.Name, lines[:5])
		}
		var desc string
		if json.Unmarshal([]byte(strings.TrimPrefix(lines[2], "description: ")), &desc) != nil || desc != m.Description {
			t.Errorf("%s description does not round-trip as a quoted scalar", m.Name)
		}
		hasRun := strings.Contains(text, "## Running this skill")
		if hasRun != m.Runnable {
			t.Errorf("%s: run section = %v, runnable = %v", m.Name, hasRun, m.Runnable)
		}
		if m.Runnable && !strings.Contains(text, "fwdctl run "+m.Name) {
			t.Errorf("%s does not say how to run it", m.Name)
		}
		if strings.Contains(text, "maturity:") || strings.Contains(text, "metadata:") {
			t.Errorf("%s carries frontmatter keys Claude Code does not read", m.Name)
		}
	}
}

func TestInstallClaudeListsTheInputsOfARunnableSkill(t *testing.T) {
	dir := t.TempDir()
	call(t, []string{"install", "claude", "--dir", dir}, "", nil)
	b, _ := os.ReadFile(filepath.Join(dir, "verify-change", "SKILL.md"))
	for _, want := range []string{"| `network_id` | string | yes |", "`change_set_id`", "FORWARD_URL"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("verify-change SKILL.md is missing %q", want)
		}
	}
}

func TestInstallClaudeIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	call(t, []string{"install", "claude", "--dir", dir}, "", nil)
	first, _ := os.ReadFile(filepath.Join(dir, "investigate-reachability", "SKILL.md"))
	call(t, []string{"install", "claude", "--dir", dir}, "", nil)
	second, _ := os.ReadFile(filepath.Join(dir, "investigate-reachability", "SKILL.md"))
	if string(first) != string(second) {
		t.Error("a second install changed the file")
	}
}

func TestInstallAgentsPrintsABlockNamingEverySkill(t *testing.T) {
	code, out, _ := call(t, []string{"install", "agents"}, "", nil)
	all, _ := skills.All()
	if code != 0 || !strings.Contains(out, beginMarker) || !strings.Contains(out, endMarker) {
		t.Fatalf("code %d out %.200s", code, out)
	}
	for _, m := range all {
		if !strings.Contains(out, "`"+m.Name+"`") {
			t.Errorf("%s missing from the block", m.Name)
		}
	}
	for _, want := range []string{"unknown is never a pass", "apply: true", "plan-safe-write", "### Skills by area", "DOGFOOD-TEMP", "redact-check"} {
		if !strings.Contains(out, want) {
			t.Errorf("the block lacks %q", want)
		}
	}
	if strings.Contains(out, "they change nothing") {
		t.Error("the block must not say the skills change nothing: the edit skills change Forward's own data")
	}
}

// The managed block is spent in every conversation of every project that installs it: it is the rules, the router pointer and a compact
// list of names, never the descriptions. Raise the cap only with a reason (see "Adding a skill" in docs/internal.md).
func TestInstallAgentsBlockStaysSmall(t *testing.T) {
	_, out, _ := call(t, []string{"install", "agents"}, "", nil)
	const maxBytes = 6144
	if len(out) > maxBytes {
		t.Errorf("the agents block is %d bytes, over the %d cap; drop words from the rules or the skill summaries, do not add descriptions", len(out), maxBytes)
	}
	all, _ := skills.All()
	for _, m := range all {
		if strings.Contains(out, m.Description) {
			t.Errorf("the block repeats the description of %s", m.Name)
		}
	}
}

// The rules an agent reads in `fwdctl docs agents` and the rules written into AGENTS.md are one text.
func TestAgentsDocsPageCarriesTheSameRulesAsTheBlock(t *testing.T) {
	code, out, _ := call(t, []string{"docs", "agents"}, "", nil)
	if code != 0 || !strings.Contains(out, strings.TrimSpace(agentsRules)) {
		t.Fatalf("docs agents (%d) does not carry the rules of the install block", code)
	}
}

func TestInstallAgentsKeepsTheUsersTextAndReplacesItsOwnBlock(t *testing.T) {
	f := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(f, []byte("# My rules\n\nBe brief.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	call(t, []string{"install", "agents", "--file", f}, "", nil)
	call(t, []string{"install", "agents", "--file", f}, "", nil)
	b, _ := os.ReadFile(f)
	text := string(b)
	if !strings.HasPrefix(text, "# My rules\n\nBe brief.\n") {
		t.Errorf("the user's text was changed: %.80q", text)
	}
	if strings.Count(text, beginMarker) != 1 || strings.Count(text, endMarker) != 1 {
		t.Errorf("the block was duplicated: %d begins", strings.Count(text, beginMarker))
	}
	// Text added after the block survives a refresh.
	_ = os.WriteFile(f, []byte(text+"\n## After\nkeep me\n"), 0o644)
	call(t, []string{"install", "agents", "--file", f}, "", nil)
	b, _ = os.ReadFile(f)
	if !strings.Contains(string(b), "## After\nkeep me") {
		t.Error("text after the managed block was lost")
	}
}

func TestInstallCreatesAMissingAgentsFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "sub", "AGENTS.md")
	if code, _, e := call(t, []string{"install", "agents", "--file", f}, "", nil); code != 0 {
		t.Fatalf("code %d %s", code, e)
	}
	if _, err := os.Stat(f); err != nil {
		t.Error(err)
	}
}

func TestInstallRejectsAnUnknownTarget(t *testing.T) {
	if code, out, _ := call(t, []string{"install", "vscode"}, "", nil); code != 64 || out != "" {
		t.Errorf("code %d out %q", code, out)
	}
}

func TestInstallClaudeCopiesTheReferenceFilesBesideSKILLmd(t *testing.T) {
	needCorpora(t)
	dir := t.TempDir()
	call(t, []string{"install", "claude", "--dir", dir}, "", nil)
	for _, f := range []string{"cheatsheet.md", "rules.md", "syntax-and-types.md"} {
		b, err := os.ReadFile(filepath.Join(dir, "author-nqe-query", "reference", f))
		if err != nil || len(b) < 200 {
			t.Errorf("reference %s not installed: %v", f, err)
		}
	}
	skill, _ := os.ReadFile(filepath.Join(dir, "author-nqe-query", "SKILL.md"))
	if !strings.Contains(string(skill), "reference/rules.md") {
		t.Error("SKILL.md does not point at its references")
	}
}

func TestDescribeReadsAReferenceFileAsPlainMarkdown(t *testing.T) {
	needCorpora(t)
	code, out, _ := call(t, []string{"describe", "author-nqe-query", "reference/rules.md"}, "", nil)
	if code != 0 || !strings.Contains(out, "DeviceType.ROUTER") || strings.HasPrefix(out, "{") {
		t.Errorf("code %d out %.120q", code, out)
	}
	if code, _, _ := call(t, []string{"describe", "author-nqe-query", "rules"}, "", nil); code != 0 {
		t.Error("the short file name did not resolve")
	}
	if code, out, e := call(t, []string{"describe", "author-nqe-query", "nope.md"}, "", nil); code != 64 || out != "" || !strings.Contains(e, "reference/rules.md") {
		t.Errorf("code %d err %.160q", code, e)
	}
}

// The reference architecture is published as docs/architecture.md and printed by `fwdctl docs architecture`: one text, two places.
func TestArchitecturePageIsTheSameInTheRepoAndTheBinary(t *testing.T) {
	repo, err := os.ReadFile("../../docs/architecture.md")
	if err != nil {
		t.Fatal(err)
	}
	code, out, _ := call(t, []string{"docs", "architecture"}, "", nil)
	if code != 0 || out != string(repo) {
		t.Fatalf("docs architecture (%d) differs from docs/architecture.md: run cp cmd/fwdctl/guide/architecture.md docs/architecture.md", code)
	}
	// every skill or playbook the page names in backticks with a verb prefix is real
	all, _ := skills.All()
	known := map[string]bool{}
	for _, m := range all {
		known[m.Name] = true
	}
	for _, m := range regexp.MustCompile("`((?:inspect|investigate|check|verify|compare|find|review|author|plan|edit)-[a-z0-9-]+)`").FindAllStringSubmatch(out, -1) {
		if !known[m[1]] {
			t.Errorf("the architecture page names %s, which is not a skill", m[1])
		}
	}
}

// The install and NQE guides are published as docs/install.md and docs/nqe.md and printed by `fwdctl docs`: one text, two places.
func TestInstallAndNQEGuidesAreTheSameInTheRepoAndTheBinary(t *testing.T) {
	for _, name := range []string{"install", "nqe"} {
		repo, err := os.ReadFile("../../docs/" + name + ".md")
		if err != nil {
			t.Fatal(err)
		}
		code, out, _ := call(t, []string{"docs", name}, "", nil)
		if code != 0 || out != string(repo) {
			t.Fatalf("docs %s (%d) differs from docs/%s.md: run cp cmd/fwdctl/guide/%s.md docs/%s.md", name, code, name, name, name)
		}
	}
}

// Rows print as a table or CSV, and the largest list of rows in a result's evidence is the one chosen.
func TestRenderRowsAndPickTheLargestEvidenceList(t *testing.T) {
	rows := []map[string]any{{"vrf": "INET", "count": float64(3), "x": nil}, {"vrf": "ASU", "count": float64(10), "nested": map[string]any{"a": 1}}}
	var csvOut, tableOut strings.Builder
	if err := renderRows(&csvOut, rows, "csv"); err != nil || !strings.HasPrefix(csvOut.String(), "count,nested,vrf,x\n") || !strings.Contains(csvOut.String(), "3,,INET,") || !strings.Contains(csvOut.String(), `"{""a"":1}"`) {
		t.Fatalf("csv: %v %q", err, csvOut.String())
	}
	if err := renderRows(&tableOut, rows, "table"); err != nil || !strings.Contains(tableOut.String(), "count") || !strings.Contains(tableOut.String(), "INET") {
		t.Fatalf("table: %v %q", err, tableOut.String())
	}
	res := map[string]any{"evidence": []any{map[string]any{"detail": map[string]any{"small": []any{map[string]any{"a": 1}}, "big": []any{map[string]any{"a": 1}, map[string]any{"a": 2}, map[string]any{"a": 3}}}}}}
	got, where := largestRows(res)
	if len(got) != 3 || !strings.HasSuffix(where, ".big") {
		t.Fatalf("largest list: %d rows at %s", len(got), where)
	}
}

// A retired skill name still answers --help and describe, and says what it is now.
func TestRetiredSkillNamesSayWhatTheyAreNow(t *testing.T) {
	code, out, _ := call(t, []string{"run", "find-trace-source", "--help"}, "", nil)
	if code != 0 || !strings.Contains(out, "retired name") || !strings.Contains(out, "inspect-edge with view=trace_sources") {
		t.Errorf("run --help (%d): %.300s", code, out)
	}
	code, out, _ = call(t, []string{"describe", "review-change-set"}, "", nil)
	if code != 0 || !strings.Contains(out, `"alias_of"`) || !strings.Contains(out, "verify-change with view=describe") {
		t.Errorf("describe (%d): %.300s", code, out)
	}
	if skills.AliasNote("verify-change") != "" {
		t.Error("a current skill is not an alias")
	}
}
