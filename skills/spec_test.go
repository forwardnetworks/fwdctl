package skills_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// Every SKILL.md conforms to the Agent Skills specification (https://agentskills.io/specification): the frontmatter is strict YAML (parsed here by a real YAML library, not the lenient reader the
// binary uses), only the specified fields appear, name is 1-64 characters of lowercase letters, digits and single hyphens and equals the directory, description is 1-1024 characters,
// compatibility at most 500, metadata maps strings to strings, and the body stays under the recommended 500 lines and 5,000 tokens (about 4 characters each).
func TestEverySkillMdConformsToTheAgentSkillsSpec(t *testing.T) {
	files, err := filepath.Glob("*/SKILL.md")
	if err != nil || len(files) < 50 {
		t.Fatalf("found %d SKILL.md files (%v)", len(files), err)
	}
	// the MCP-user plugin ships in the same distribution; the public source tree has no plugins/ folder, so the check then covers the skills only
	plugin, _ := filepath.Glob("../plugins/*/skills/*/SKILL.md")
	if len(plugin) == 0 {
		t.Log("no plugins/ folder here: only skills/*/SKILL.md are checked")
	}
	files = append(files, plugin...)
	nameRe := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	allowed := map[string]bool{"name": true, "description": true, "license": true, "compatibility": true, "metadata": true, "allowed-tools": true}
	for _, f := range files {
		dir := filepath.Base(filepath.Dir(f))
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		if !strings.HasPrefix(src, "---\n") {
			t.Errorf("%s: no opening --- fence", f)
			continue
		}
		rest := src[4:]
		end := strings.Index(rest, "\n---\n")
		if end < 0 {
			t.Errorf("%s: no closing --- fence", f)
			continue
		}
		var fm map[string]any
		if err := yaml.Unmarshal([]byte(rest[:end]), &fm); err != nil {
			t.Errorf("%s: the frontmatter is not valid YAML (quote a value that holds a colon): %v", f, err)
			continue
		}
		for k := range fm {
			if !allowed[k] {
				t.Errorf("%s: field %q is not in the specification (name, description, license, compatibility, metadata, allowed-tools)", f, k)
			}
		}
		name, _ := fm["name"].(string)
		switch {
		case name != dir:
			t.Errorf("%s: name %q must equal its directory %q", f, name, dir)
		case len(name) > 64 || !nameRe.MatchString(name):
			t.Errorf("%s: name %q must be 1-64 lowercase letters, digits and single hyphens, not starting or ending with one", f, name)
		}
		desc, _ := fm["description"].(string)
		if desc == "" || len(desc) > 1024 {
			t.Errorf("%s: description is %d characters, want 1 to 1024", f, len(desc))
		}
		if c, present := fm["compatibility"]; present {
			if s, ok := c.(string); !ok || s == "" || len(s) > 500 {
				t.Errorf("%s: compatibility must be a string of 1 to 500 characters", f)
			}
		}
		if md, present := fm["metadata"]; present {
			m, ok := md.(map[string]any)
			if !ok {
				t.Errorf("%s: metadata must be a map", f)
				continue
			}
			for k, v := range m {
				if _, isString := v.(string); !isString {
					t.Errorf("%s: metadata.%s is %T, the specification wants a string (quote it)", f, k, v)
				}
			}
		}
		body := rest[end+5:]
		if n := strings.Count(body, "\n") + 1; n > 500 {
			t.Errorf("%s: the body is %d lines, the specification recommends under 500", f, n)
		}
		if tok := len(src) / 4; tok > 5000 {
			t.Errorf("%s: about %d tokens, the specification recommends under 5,000", f, tok)
		}
	}
}
