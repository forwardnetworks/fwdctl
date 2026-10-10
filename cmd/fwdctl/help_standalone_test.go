package main

import (
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/skills"
)

// An agent with only `fwdctl --help` (no skills installed) must be able to use every command and every runnable name. These are the
// floor for that: each command says what it is for and shows a real invocation, and each runnable name's help shows how to run it, what it
// takes, and, for a name that changes Forward, that it is a dry run until apply. evals/standalone measures whether that is enough.
func TestEveryCommandSaysWhatItIsForAndShowsAnInvocation(t *testing.T) {
	root := (&app{}).newRoot()
	var cmds [][]string
	walk(root, nil, &cmds)
	for _, c := range cmds {
		name := "fwdctl " + strings.Join(c, " ")
		sub, _, err := root.Find(c)
		if err != nil || sub == nil {
			t.Fatalf("%s: not found: %v", name, err)
		}
		if strings.TrimSpace(sub.Short) == "" {
			t.Errorf("%s has no one-line purpose (Short)", name)
		}
		if strings.TrimSpace(sub.Example) == "" {
			t.Errorf("%s has no example: add Example with a copy-pasteable invocation", name)
		}
		_, out, _ := call(t, append(append([]string{}, c...), "--help"), "", nil)
		if sub.Example != "" && !strings.Contains(out, "Examples:") {
			t.Errorf("%s --help does not print its examples", name)
		}
		if !strings.Contains(sub.Example, "fwdctl") {
			t.Errorf("%s: the example should show the full command, starting fwdctl", name)
		}
	}
}

func TestEveryRunnableNameHelpShowsHowToRunItAndWhatItTakes(t *testing.T) {
	all, err := skills.All()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, m := range all {
		if !m.Runnable {
			continue
		}
		n++
		code, out, errb := call(t, []string{"run", m.Name, "--help"}, "", nil)
		if code != 0 {
			t.Errorf("fwdctl run %s --help: exit %d: %s", m.Name, code, errb)
			continue
		}
		if !strings.Contains(out, "fwdctl run "+m.Name) || !strings.Contains(out, "echo '") {
			t.Errorf("fwdctl run %s --help shows no runnable example", m.Name)
		}
		if !strings.Contains(out, "| Input |") {
			t.Errorf("fwdctl run %s --help lists no inputs", m.Name)
		}
		if m.Class == "write" && !strings.Contains(out, "apply") {
			t.Errorf("fwdctl run %s --help does not say it is a dry run until apply", m.Name)
		}
	}
	if n < 30 {
		t.Fatalf("only %d runnable names checked; the registry looks wrong", n)
	}
}

// A reader of `run NAME --help` has no skill files: the help must not send them to SKILL.md or schema.json, and a name that takes an
// object and an action must show which fields each takes (edit-source is the one an agent most often has to get right).
func TestRunnableNameHelpNeverPointsAtFilesAReaderCannotSee(t *testing.T) {
	all, _ := skills.All()
	for _, m := range all {
		if !m.Runnable {
			continue
		}
		_, out, _ := call(t, []string{"run", m.Name, "--help"}, "", nil)
		for _, bad := range []string{"SKILL.md", "schema.json", "`reference/"} {
			if strings.Contains(out, bad) {
				t.Errorf("fwdctl run %s --help mentions %s, which a reader of --help cannot open", m.Name, bad)
			}
		}
	}
	_, out, _ := call(t, []string{"run", "edit-source", "--help"}, "", nil)
	for _, want := range []string{"Input details", "`classic_device`", "`collectorId`", "`jumpServerId`", "fwdctl describe edit-source reference/objects.md"} {
		if !strings.Contains(out, want) {
			t.Errorf("fwdctl run edit-source --help lacks %s", want)
		}
	}
}
