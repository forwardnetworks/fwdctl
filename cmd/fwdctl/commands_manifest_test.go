package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A command with no class would reach a host that classifies by this manifest as an unknown: a new command must be classified before it ships.
func TestEveryCommandHasAClassInTheManifest(t *testing.T) {
	m, err := buildCommandsManifest((&app{}).newRoot())
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Commands) < 40 {
		t.Fatalf("the manifest lists %d commands: the tree was not walked", len(m.Commands))
	}
	for _, c := range m.Commands {
		if c.Unclassified {
			t.Errorf("fwdctl %s has no class in commandClasses (commands_manifest.go)", c.Path)
		}
	}
	// and a class names a command that exists: a renamed or removed command must not leave a stale class
	have := map[string]bool{}
	for _, c := range m.Commands {
		have[c.Path] = true
	}
	for k := range commandClasses {
		if !have[k] {
			t.Errorf("commandClasses has %q, which is not a command", k)
		}
	}
}

// Every `fwdctl X [Y]` that a skill's text tells an agent to run must be a command of this binary, so a skill cannot ship pointing at a command that does not exist (or one a host never mapped).
func TestEveryCommandASkillNamesIsInTheCommandTree(t *testing.T) {
	m, _ := buildCommandsManifest((&app{}).newRoot())
	have := map[string]bool{}
	for _, c := range m.Commands {
		have[c.Path] = true
	}
	// a command written as code: `fwdctl X [Y]` in backticks, or a line that starts with fwdctl (a code block). Prose that merely has the word fwdctl in it is not a command.
	re := regexp.MustCompile("(?m)(?:`|^[ \\t]*(?:\\$ )?)fwdctl (?:--[a-z-]+ [^ `]+ )?([a-z][a-z-]*)(?: ([a-z][a-z-]*))?")
	var files []string
	for _, pat := range []string{"../../skills/*/SKILL.md", "../../skills/*/reference/*.md"} {
		f, _ := filepath.Glob(pat)
		files = append(files, f...)
	}
	if len(files) < 100 {
		t.Fatalf("only %d skill files found", len(files))
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, mm := range re.FindAllStringSubmatch(string(b), -1) {
			first, second := mm[1], mm[2]
			if !have[first] && !have[first+" "+second] {
				t.Errorf("%s names `fwdctl %s %s`, which is not a command of this binary", f, first, second)
			}
		}
	}
}

func TestFwdctlCommandsPrintsAManifestWithClassesForCommandsAndSkills(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"commands"}, strings.NewReader(""), &out, &errb, nil); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	var m commandsManifest
	if err := json.Unmarshal(out.Bytes(), &m); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	cls := map[string]manifestCommand{}
	for _, c := range m.Commands {
		cls[c.Path] = c
	}
	if c := cls["device-file export"]; c.Class != "read" || !c.WritesLocal || !c.MaySecrets {
		t.Errorf("device-file export is a read that writes local files and may hold secrets: %+v", c)
	}
	if c := cls["source apply"]; c.Class != "write" {
		t.Errorf("source apply writes: %+v", c)
	}
	if c := cls["nqe params"]; c.Class != "local" {
		t.Errorf("nqe params is local: %+v", c)
	}
	sk := map[string]manifestSkill{}
	for _, s := range m.Skills {
		sk[s.Name] = s
	}
	if s := sk["edit-source"]; s.Class != "write" || !s.Runnable {
		t.Errorf("edit-source is a write skill: %+v", s)
	}
	if s := sk["inspect-inventory"]; s.Class != "read" {
		t.Errorf("inspect-inventory is a read skill: %+v", s)
	}
	if len(m.Skills) < 55 {
		t.Errorf("the skills are listed: %d", len(m.Skills))
	}
}
