package skills

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// These are the checkable rules from Anthropic's skill authoring best practices and "The Complete Guide to Building
// Skills for Claude". They are cheap to keep true and expensive to discover in a harness one skill at a time.

var nameRule = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func TestSkillsFollowTheAuthoringGuidelines(t *testing.T) {
	names := Names()
	if len(names) < 8 {
		t.Fatalf("only %d skills found", len(names))
	}
	for _, name := range names {
		m, err := Describe(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		// A skill that writes must be dry-run by default: it takes apply, says so, and says how each change is undone.
		if m.Class == "write" {
			var props struct {
				Properties map[string]any `json:"properties"`
			}
			_ = json.Unmarshal(m.InputSchema, &props)
			if _, ok := props.Properties["apply"]; !ok {
				t.Errorf("%s: a write skill must take an apply input (dry run unless true)", name)
			}
			body := strings.ToLower(m.Body)
			for _, need := range []string{"dry run", "undo"} {
				if !strings.Contains(body, need) {
					t.Errorf("%s: a write skill's SKILL.md must explain %q", name, need)
				}
			}
		} else if strings.Contains(string(m.InputSchema), `"apply"`) {
			t.Errorf("%s: takes apply but is not class write", name)
		}
		// name: kebab-case, at most 64 characters, no reserved words, equal to its folder.
		if !nameRule.MatchString(name) || len(name) > 64 {
			t.Errorf("%s: name must be lowercase kebab-case, at most 64 characters", name)
		}
		if strings.Contains(name, "claude") || strings.Contains(name, "anthropic") {
			t.Errorf("%s: 'claude' and 'anthropic' are reserved in skill names", name)
		}
		// description: what it does AND when to use it, third person, under 1024 characters, no angle brackets.
		d := m.Description
		if len(d) == 0 || len(d) > 1024 {
			t.Errorf("%s: description is %d characters (want 1 to 1024)", name, len(d))
		}
		if strings.ContainsAny(d, "<>") {
			t.Errorf("%s: description contains angle brackets, which are forbidden in frontmatter", name)
		}
		first := strings.Fields(d)[0]
		if !strings.HasSuffix(first, "s") || strings.EqualFold(first, "use") {
			t.Errorf("%s: description should be third person (\"Finds ...\"), it starts with %q", name, first)
		}
		if !strings.Contains(d, "Use when") && !strings.Contains(d, "Use for") && !strings.Contains(d, "Use before") && !strings.Contains(d, "Use after") && !strings.Contains(d, "Use at") {
			t.Errorf("%s: description does not say when to use the skill (\"Use when ...\")", name)
		}
		if strings.Contains(d, " I ") || strings.Contains(strings.ToLower(d), "you can") {
			t.Errorf("%s: description is not in the third person", name)
		}
		// body: under 500 lines.
		if n := strings.Count(m.Body, "\n"); n > 500 {
			t.Errorf("%s: SKILL.md body is %d lines (want under 500)", name, n)
		}
		// no Windows-style paths, no reserved README inside a skill.
		if regexp.MustCompile(`\b[a-z_]+\\[a-z_]+\.(md|json|py)\b`).MatchString(m.Body) {
			t.Errorf("%s: uses a Windows-style path", name)
		}
	}
	// A skill folder holds SKILL.md (exactly that name), optional schema.json, and no README.
	entries, _ := fs.ReadDir(docs, ".")
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub, _ := fs.ReadDir(docs, e.Name())
		for _, f := range sub {
			if strings.EqualFold(f.Name(), "README.md") || (strings.EqualFold(f.Name(), "skill.md") && f.Name() != "SKILL.md") {
				t.Errorf("%s/%s: skill folders take SKILL.md and no README", e.Name(), f.Name())
			}
		}
	}
}

// Frontmatter allows name, description, license, compatibility and metadata, and nothing else at the top level.
func TestFrontmatterUsesOnlyTheAllowedKeys(t *testing.T) {
	allowed := map[string]bool{"name": true, "description": true, "license": true, "compatibility": true, "metadata": true}
	for _, name := range Names() {
		b, _ := docs.ReadFile(name + "/SKILL.md")
		rest := strings.TrimPrefix(string(b), "---\n")
		front := rest[:strings.Index(rest, "\n---")]
		for _, line := range strings.Split(front, "\n") {
			if line == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "#") {
				continue
			}
			key := line[:strings.Index(line, ":")]
			if !allowed[key] {
				t.Errorf("%s: frontmatter key %q is not allowed (use metadata: for custom keys)", name, key)
			}
		}
		if c := regexp.MustCompile(`(?m)^compatibility:\s*(.*)$`).FindStringSubmatch(front); c != nil && (len(c[1]) == 0 || len(c[1]) > 500) {
			t.Errorf("%s: compatibility must be 1 to 500 characters", name)
		}
	}
}

// Every skill has at least three evaluations, including one that must NOT trigger it, in the guide's structure.
func TestEverySkillHasThreeEvaluationsIncludingANegativeTrigger(t *testing.T) {
	needCorpora(t)
	for _, name := range Names() {
		p := filepath.Join("..", "evals", "skills", name+".json")
		b, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("%s: no evaluations at evals/skills/%s.json", name, name)
			continue
		}
		var doc struct {
			Skill string `json:"skill"`
			Cases []struct {
				Query            string   `json:"query"`
				ShouldTrigger    *bool    `json:"should_trigger"`
				ExpectedBehavior []string `json:"expected_behavior"`
			} `json:"cases"`
		}
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Errorf("%s: %v", p, err)
			continue
		}
		neg, pos := 0, 0
		for i, c := range doc.Cases {
			if c.Query == "" || c.ShouldTrigger == nil {
				t.Errorf("%s case %d: needs a query and should_trigger", name, i)
				continue
			}
			if *c.ShouldTrigger {
				pos++
				if len(c.ExpectedBehavior) == 0 {
					t.Errorf("%s case %d: a triggering case needs expected_behavior", name, i)
				}
			} else {
				neg++
			}
		}
		if doc.Skill != name || len(doc.Cases) < 3 || neg < 1 || pos < 2 {
			t.Errorf("%s: %d cases (%d trigger, %d must-not), want at least 3 with 2+ triggering and 1+ not", name, len(doc.Cases), pos, neg)
		}
	}
}

// Reference files stay one level deep: SKILL.md links to them, and they link to nothing else in the skill. A file over 100
// lines opens with a contents list so a partial read still shows what it holds. SKILL.md names every reference file.
func TestReferenceFilesFollowProgressiveDisclosure(t *testing.T) {
	needCorpora(t)
	link := regexp.MustCompile(`\]\(([^)]+\.md)\)`)
	for _, name := range Names() {
		m, _ := Describe(name)
		for _, ref := range m.References {
			if !strings.Contains(m.Body, ref) {
				t.Errorf("%s: SKILL.md does not point to %s", name, ref)
			}
			text, err := Reference(name, ref)
			if err != nil {
				t.Errorf("%s: %v", name, err)
				continue
			}
			if strings.Count(text, "\n") > 100 && !strings.Contains(text, "## Contents") {
				t.Errorf("%s/%s: over 100 lines without a contents list", name, ref)
			}
			if link.MatchString(text) {
				t.Errorf("%s/%s: links to another file; references must be one level deep", name, ref)
			}
		}
		for _, l := range link.FindAllStringSubmatch(m.Body, -1) {
			if _, err := docs.ReadFile(name + "/" + l[1]); err != nil {
				t.Errorf("%s: SKILL.md links to %s, which does not exist", name, l[1])
			}
		}
	}
}
