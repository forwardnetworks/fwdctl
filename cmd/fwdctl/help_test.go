package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// walk lists every command in the tree with the words that reach it.
func walk(c *cobra.Command, path []string, out *[][]string) {
	for _, sub := range c.Commands() {
		if sub.Hidden || sub.Name() == "help" || sub.Name() == "completion" {
			continue
		}
		p := append(append([]string{}, path...), sub.Name())
		*out = append(*out, p)
		walk(sub, p, out)
	}
}

// Help is how a person, and an agent that finds a skill too much, discovers the CLI one level at a time: every command answers --help with its own usage and exit 0, and the
// commands that take flags list every flag (cobra generates them from the flag set, so the text cannot drift from what the command accepts).
func TestEveryCommandAnswersHelpWithItsOwnUsageAndExitZero(t *testing.T) {
	var cmds [][]string
	walk((&app{}).newRoot(), nil, &cmds)
	if len(cmds) < 20 {
		t.Fatalf("the command tree looks too small: %v", cmds)
	}
	for _, c := range cmds {
		code, out, errb := call(t, append(append([]string{}, c...), "--help"), "", nil)
		if code != 0 || !strings.Contains(out, "Usage:") || !strings.Contains(out, "fwdctl "+c[0]) {
			t.Errorf("fwdctl %s --help: exit %d, stdout %q, stderr %q", strings.Join(c, " "), code, out, errb)
		}
	}
	for _, c := range [][]string{{"--help"}, {"help"}, {"help", "nqe", "run"}, {"help", "inspect-networks"}, {"run", "inspect-networks", "--help"}} {
		if code, out, _ := call(t, c, "", nil); code != 0 || strings.TrimSpace(out) == "" {
			t.Errorf("fwdctl %s: exit %d, stdout %q", strings.Join(c, " "), code, out)
		}
	}
}

func TestNQERunAndBundleHelpListEveryFlagTheyAccept(t *testing.T) {
	_, out, _ := call(t, []string{"nqe", "run", "--help"}, "", nil)
	for _, want := range []string{"--network", "--file", "--query-id", "--commit-id", "--param", "--params", "--snapshot", "--format", "--count-by", "--max", "--offset", "--limit", "--async", "--meta", "--timeout", "FORWARD_NQE_MODE"} {
		if !strings.Contains(out, want) {
			t.Errorf("nqe run --help does not mention %s", want)
		}
	}
	_, out, _ = call(t, []string{"nqe", "bundle", "--help"}, "", nil)
	for _, want := range []string{"--query-id", "--path", "--commit-id", "--override", "--add-module", "--out"} {
		if !strings.Contains(out, want) {
			t.Errorf("nqe bundle --help does not mention %s", want)
		}
	}
	_, out, _ = call(t, []string{"--help"}, "", nil)
	for _, want := range []string{"--url", "--username", "--password-file", "--insecure", "FORWARD_TIMEOUT", "Exit status"} {
		if !strings.Contains(out, want) {
			t.Errorf("the top-level help does not mention %s", want)
		}
	}
	if code, _, _ := call(t, []string{"nqe", "nope"}, "", nil); code != usage {
		t.Errorf("an unknown nqe command is a usage error, got %d", code)
	}
	if code, _, _ := call(t, []string{"nqe", "run", "--offset", "2", "--network", "1"}, "x", nil); code != usage {
		t.Errorf("--offset without --limit is a usage error, got %d", code)
	}
}

func TestSkillHelpHasARealExampleAndNotes(t *testing.T) {
	_, out, _ := call(t, []string{"run", "edit-access", "--help"}, "", nil)
	if !strings.Contains(out, `echo '{"action": "create_user"}' | fwdctl run edit-access`) || !strings.Contains(out, "Notes") {
		t.Errorf("run edit-access --help:\n%s", out)
	}
}
