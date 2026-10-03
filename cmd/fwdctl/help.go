package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/forwardnetworks/fwdctl/skills"
)

// skillHelp is what a person or agent needs before running one name: what it answers, its inputs, and how to call it.
func skillHelp(w io.Writer, name string) int {
	m, err := skills.Describe(name)
	if err != nil {
		fmt.Fprintf(w, "error: %v; run `fwdctl run` for the names\n", err)
		return usage
	}
	fmt.Fprintf(w, "%s\n\n%s\n\n", m.Name, m.Description)
	if note := skills.AliasNote(name); note != "" {
		fmt.Fprintf(w, "`%s` is a retired name: it runs %s. The inputs below are the current name's; the old inputs still work.\n\n", name, note)
	}
	if !m.Runnable {
		fmt.Fprintf(w, "This is a procedure, not something to run. Read it with: fwdctl describe %s\n", m.Name)
		return 0
	}
	// the section is shared with the installed SKILL.md; the CLI's own help does not name skills
	sec := strings.NewReplacer("## Running this skill", "## Running it", "This skill changes Forward", "This changes Forward", "this skill", "this name").Replace(strings.TrimPrefix(runSection(m), "\n"))
	fmt.Fprintln(w, sec)
	fmt.Fprintf(w, "Procedure and evidence: fwdctl describe %s\n", m.Name)
	return 0
}
