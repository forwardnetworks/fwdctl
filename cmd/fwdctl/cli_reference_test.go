package main

import (
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The command reference is generated from the cobra tree: guide/cli.md (embedded, `fwdctl docs cli`) and docs/cli.md must equal it. After changing a command or flag:
// UPDATE_CLI_DOC=1 go test ./cmd/fwdctl -run CLIReference
func TestCLIReferenceMatchesTheCommandTree(t *testing.T) {
	want := cliReference((&app{}).newRoot())
	if os.Getenv("UPDATE_CLI_DOC") != "" {
		for _, p := range []string{"guide/cli.md", "../../docs/cli.md"} {
			if err := os.WriteFile(p, []byte(want), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, p := range []string{"guide/cli.md", "../../docs/cli.md"} {
		got, err := os.ReadFile(p)
		if err != nil || string(got) != want {
			t.Errorf("%s is out of date with the command tree: run UPDATE_CLI_DOC=1 go test ./cmd/fwdctl -run CLIReference (%v)", p, err)
		}
	}
	for _, topic := range []string{"overview", "cli"} {
		if code, out, _ := call(t, []string{"docs", topic}, "", nil); code != 0 || strings.TrimSpace(out) == "" {
			t.Errorf("fwdctl docs %s: %d", topic, code)
		}
	}
}

// Sufficient help: every command a person or agent can run says what it is for and shows how to call it.
func TestEveryCommandHasADescriptionAndAnExample(t *testing.T) {
	var check func(c *cobra.Command)
	check = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.Hidden || sub.Name() == "help" || sub.Name() == "completion" || strings.Contains(sub.Short, "DOGFOOD-TEMP") {
				continue
			}
			if strings.TrimSpace(sub.Short) == "" {
				t.Errorf("%s has no short description", sub.CommandPath())
			}
			if len(sub.Commands()) == 0 && strings.TrimSpace(sub.Example) == "" {
				t.Errorf("%s has no example", sub.CommandPath())
			}
			check(sub)
		}
	}
	check((&app{}).newRoot())
}
