package main

import (
	"fmt"
	"io"
	"regexp"
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
	if d := inputDetails(m.Name, m.Body); d != "" {
		fmt.Fprintf(w, "## Input details\n\n%s\n\n", d)
	}
	fmt.Fprintf(w, "Procedure and evidence: fwdctl describe %s\n", m.Name)
	return 0
}

// inputDetails is the "## Inputs" section of the skill's own SKILL.md: what each input means and, for a name that takes an object and an
// action, which fields each pair takes. The generated table above lists the input names; this is the part only a person who had read the
// skill would know, so a standalone `run NAME --help` carries it too (one copy, the SKILL.md).
var refPointer = regexp.MustCompile("`reference/([A-Za-z0-9_.-]+)`")

func inputDetails(name, body string) string {
	var sec string
	if ref, err := skills.Reference(name, "reference/inputs.md"); err == nil {
		// the long form lives beside the skill so SKILL.md stays short; this help is where an agent without the skill reads it
		sec = ref
		if k := strings.Index(sec, "\n"); k >= 0 && strings.HasPrefix(sec, "# ") {
			sec = sec[k+1:]
		}
	} else {
		i := strings.Index(body, "\n## Inputs\n")
		if i < 0 {
			return ""
		}
		sec = body[i+len("\n## Inputs\n"):]
		if j := strings.Index(sec, "\n## "); j >= 0 {
			sec = sec[:j]
		}
	}
	var keep []string
	for _, l := range strings.Split(strings.TrimSpace(sec), "\n") {
		// the pointer to schema.json means nothing to a reader of --help, which already shows the generated table
		l = strings.TrimSpace(strings.NewReplacer(" See `schema.json`.", "", "See `schema.json`.", "").Replace(l)) + ""
		keep = append(keep, l)
	}
	// a pointer to a reference file becomes the command that prints it
	out := refPointer.ReplaceAllString(strings.TrimSpace(strings.Join(keep, "\n")), "`fwdctl describe "+name+" reference/$1`")
	return out
}
