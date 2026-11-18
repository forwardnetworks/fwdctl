package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/forwardnetworks/fwdctl/skills"
)

// `install agent-skills` writes the Agent Skills standard's cross-client layout: <dir>/<name>/SKILL.md for every skill, whose frontmatter a real YAML parser reads back to exactly the skill's name,
// description and compatibility, with only specified fields; the default folder is ~/.agents/skills.
func TestInstallAgentSkillsWritesStrictYAMLFrontmatterToTheCrossClientFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	code, out, errb := call(t, []string{"install", "agent-skills"}, "", nil)
	if code != 0 || !strings.Contains(out, filepath.Join(home, ".agents", "skills")) {
		t.Fatalf("code %d out %q err %s", code, out, errb)
	}
	all, _ := skills.All()
	if len(all) < 50 {
		t.Fatalf("only %d skills", len(all))
	}
	for _, m := range all {
		b, err := os.ReadFile(filepath.Join(home, ".agents", "skills", m.Name, "SKILL.md"))
		if err != nil {
			t.Fatalf("%s not written: %v", m.Name, err)
		}
		rest := strings.TrimPrefix(string(b), "---\n")
		end := strings.Index(rest, "\n---\n")
		if end < 0 {
			t.Fatalf("%s: no closing fence", m.Name)
		}
		var fm map[string]any
		if err := yaml.Unmarshal([]byte(rest[:end]), &fm); err != nil {
			t.Fatalf("%s: the installed frontmatter is not valid YAML: %v", m.Name, err)
		}
		if fm["name"] != m.Name || fm["description"] != m.Description || (m.Compatibility != "" && fm["compatibility"] != m.Compatibility) {
			t.Errorf("%s: installed frontmatter differs from the skill: %v", m.Name, fm)
		}
		for k := range fm {
			if k != "name" && k != "description" && k != "compatibility" {
				t.Errorf("%s: field %q is not one the installer writes", m.Name, k)
			}
		}
	}
	dir := t.TempDir()
	if code, _, _ := call(t, []string{"install", "agent-skills", "--dir", dir}, "", nil); code != 0 {
		t.Fatalf("--dir failed: %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "verify-change", "SKILL.md")); err != nil {
		t.Errorf("--dir ignored: %v", err)
	}
}

// Shell completion offers the words of a command's subcommands: a subcommand missing from completionSub is never offered (agent-skills was missed once). For every parent that has a list, every
// visible subcommand is in it.
func TestCompletionOffersEverySubcommandOfTheParentsItLists(t *testing.T) {
	root := (&app{}).newRoot()
	for _, parent := range root.Commands() {
		words, listed := completionSub[parent.Name()]
		if !listed {
			continue
		}
		have := map[string]bool{}
		for _, w := range words {
			have[w] = true
		}
		for _, sub := range parent.Commands() {
			if sub.Hidden || sub.Name() == "help" {
				continue
			}
			if !have[sub.Name()] {
				t.Errorf("fwdctl %s %s is not in completionSub[%q]", parent.Name(), sub.Name(), parent.Name())
			}
		}
	}
}
