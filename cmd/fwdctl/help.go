package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/forwardnetworks/fwdctl/skills"
)

// skillHelp is what a person or agent needs before running one skill: what it answers, its inputs, and how to call it.
func skillHelp(w io.Writer, name string) int {
	m, err := skills.Describe(name)
	if err != nil {
		fmt.Fprintf(w, "error: %v; run `fwdctl list` for the skills\n", err)
		return usage
	}
	fmt.Fprintf(w, "%s\n\n%s\n\n", m.Name, m.Description)
	if note := skills.AliasNote(name); note != "" {
		fmt.Fprintf(w, "`%s` is a retired name: it runs %s. The inputs below are the surviving skill's; the old inputs still work.\n\n", name, note)
	}
	if !m.Runnable {
		fmt.Fprintf(w, "This is a procedure, not something to run. Read it with: fwdctl describe %s\n", m.Name)
		return 0
	}
	fmt.Fprintln(w, strings.TrimPrefix(runSection(m), "\n"))
	fmt.Fprintf(w, "Procedure and evidence: fwdctl describe %s\n", m.Name)
	return 0
}
