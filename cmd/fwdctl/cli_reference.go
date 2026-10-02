package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// cliReference renders the whole command tree as Markdown: every command's usage, description, examples and flags, generated from the cobra tree so the reference cannot drift
// from the commands. It is the embedded guide topic `fwdctl docs cli` and docs/cli.md (cli_reference_test.go compares them; UPDATE_CLI_DOC=1 go test ./cmd/fwdctl rewrites both).
func cliReference(root *cobra.Command) string {
	var b strings.Builder
	b.WriteString("# fwdctl command reference\n\n")
	b.WriteString("Generated from the command tree (`fwdctl <command> --help` shows the same for one command). Every command answers `--help`.\n\n")
	b.WriteString("Connection flags work on every command that talks to Forward:\n\n```\n")
	b.WriteString(root.PersistentFlags().FlagUsages())
	b.WriteString("```\n\nExit status: 0 ok, 1 failed (a finding), 2 unknown (never a pass), 3 error, 64 bad usage or input.\n")
	var cmds []*cobra.Command
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		subs := append([]*cobra.Command{}, c.Commands()...)
		sort.Slice(subs, func(i, j int) bool { return subs[i].Name() < subs[j].Name() })
		for _, sub := range subs {
			if sub.Hidden || sub.Name() == "help" || sub.Name() == "completion" || strings.Contains(sub.Short, "DOGFOOD-TEMP") {
				continue
			}
			cmds = append(cmds, sub)
			walk(sub)
		}
	}
	walk(root)
	for _, c := range cmds {
		fmt.Fprintf(&b, "\n## %s\n\n", c.CommandPath())
		if c.Long != "" {
			b.WriteString(strings.TrimSpace(c.Long) + "\n\n")
		} else {
			b.WriteString(c.Short + "\n\n")
		}
		fmt.Fprintf(&b, "```\n%s\n```\n", c.UseLine())
		if c.Example != "" {
			fmt.Fprintf(&b, "\nExamples:\n\n```\n%s\n```\n", strings.TrimRight(c.Example, "\n"))
		}
		if fl := c.LocalFlags().FlagUsages(); strings.TrimSpace(fl) != "" {
			fmt.Fprintf(&b, "\nFlags:\n\n```\n%s```\n", fl)
		}
	}
	b.WriteString("\n## fwdctl completion\n\nPrints a shell completion script: `source <(fwdctl completion bash)`, or for zsh, fish and PowerShell the line in the script's first comment (`fwdctl completion zsh --help`). It completes commands, skill names, flag values, and with a working login `--network` and `--snapshot`.\n")
	return b.String()
}
