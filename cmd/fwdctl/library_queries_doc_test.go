package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/forwardnetworks/fwdctl/nqelint"
)

// skills/check-network-compliance/reference/library-queries.md tells an agent how to run Forward's saved STIG, CIS and unused-rule queries. Its recipes must be runnable as written: the query ids
// have the library's shape (FQ_ and 40 hex digits) and appear in the table, every flag of a `fwdctl nqe run` recipe exists on that command, and the NQE it embeds lints clean against the schema.
func TestLibraryQueriesReferenceRecipesAreRunnableAsWritten(t *testing.T) {
	b, err := os.ReadFile("../../skills/check-network-compliance/reference/library-queries.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	idRe := regexp.MustCompile("FQ_[0-9a-f]{40}")
	table := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "| ") {
			for _, id := range idRe.FindAllString(line, -1) {
				table[id] = true
			}
		}
	}
	if len(table) != 7 {
		t.Errorf("the table lists %d distinct query ids, want 7 (all STIG, Cat I, II, III, CIS findings, CIS rules, unused rules)", len(table))
	}
	for _, id := range idRe.FindAllString(text, -1) {
		if !table[id] {
			t.Errorf("%s is used in a recipe but is not in the table", id)
		}
	}
	if bare := regexp.MustCompile(`\bQ_[0-9a-f]{40}`).FindString(text); bare != "" {
		t.Errorf("%s: a bare Q_ id would 404 on the live library: ids are FQ_", bare)
	}
	// every flag of a `fwdctl nqe run` line exists on that command
	var run *cobra.Command
	nqeCmd, _, _ := (&app{}).newRoot().Find([]string{"nqe", "run"})
	run = nqeCmd
	if run == nil || run.Name() != "run" {
		t.Fatal("fwdctl nqe run not found in the command tree")
	}
	flagRe := regexp.MustCompile(`--[a-z][a-z-]*`)
	runs := 0
	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(line, "fwdctl nqe run") {
			continue
		}
		runs++
		for _, f := range flagRe.FindAllString(line, -1) {
			if run.Flags().Lookup(strings.TrimPrefix(f, "--")) == nil && run.InheritedFlags().Lookup(strings.TrimPrefix(f, "--")) == nil {
				t.Errorf("recipe uses %s, which fwdctl nqe run does not have: %s", f, strings.TrimSpace(line))
			}
		}
	}
	if runs < 4 {
		t.Errorf("only %d fwdctl nqe run recipes found", runs)
	}
	// the embedded coverage query lints clean
	i := strings.Index(text, "      foreach device in network.devices")
	if i < 0 {
		t.Fatal("the coverage query is gone")
	}
	var q []string
	for _, line := range strings.Split(text[i:], "\n") {
		if !strings.HasPrefix(line, "      ") {
			break
		}
		q = append(q, strings.TrimPrefix(line, "      "))
	}
	if ds := nqelint.LintWith(strings.Join(q, "\n"), nqelint.Modules); nqelint.HasErrors(ds) {
		t.Errorf("the coverage query does not lint: %v", ds)
	}
	for _, want := range []string{"Not Reviewed", "unknown, not compliant", "withLastUsed", "expectedUnusedRules", "--count-by Outcome", "never in rows", "--snapshot BEFORE", "comm -13", "not as a `check-network-compliance` policy", "neither reads nor changes it here", "This is a hypothesis"} {
		if !strings.Contains(text, want) {
			t.Errorf("the reference lost %q", want)
		}
	}
}
