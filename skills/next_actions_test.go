package skills

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A next action is a recommendation to a harness: it must name a skill that exists, and never the skill that made it.
func TestNextActionsNameRealSkillsAndNeverThemselves(t *testing.T) {
	known := map[string]bool{}
	for _, n := range Names() {
		known[n] = true
	}
	files, _ := filepath.Glob("*.go")
	list := regexp.MustCompile(`NextActions:\s*\[\]string\{([^}]*)\}`)
	str := regexp.MustCompile(`"([a-z][a-z0-9-]+)"`)
	name := regexp.MustCompile(`Name\s*=\s*"([a-z0-9-]+)"`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		self := ""
		if m := name.FindSubmatch(b); m != nil {
			self = string(m[1])
		}
		for _, m := range list.FindAllSubmatch(b, -1) {
			for _, s := range str.FindAllSubmatch(m[1], -1) {
				n := string(s[1])
				if !known[n] {
					t.Errorf("%s: next action %q is not a skill", f, n)
				}
				if n == self {
					t.Errorf("%s: a skill recommends itself (%q)", f, n)
				}
			}
		}
	}
}

// plan-investigation is the router: every runnable skill must appear in it, or a consumer is never sent there.
func TestEveryRunnableSkillIsRoutedByPlanInvestigation(t *testing.T) {
	b, err := os.ReadFile("plan-investigation/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range all {
		if m.Runnable && m.Name != "plan-investigation" && !strings.Contains(string(b), "`"+m.Name+"`") {
			t.Errorf("plan-investigation does not route to %s", m.Name)
		}
	}
}

// A retired name keeps working: it must resolve to a real skill and describe it, so no design or harness is broken by a merge. An alias of a
// runner resolves to a runner; an alias of a procedure (a playbook folded into a skill's guide) resolves to a procedure.
func TestEveryAliasResolvesToARealRunnableSkill(t *testing.T) {
	procedureAliases := map[string]bool{"plan-author-query": true}
	for old, to := range Aliases() {
		m, err := Describe(old)
		if err != nil || m.Name != to || (!m.Runnable && !procedureAliases[old]) {
			t.Errorf("alias %s -> %s: %+v %v", old, to, m, err)
		}
		for _, n := range Names() {
			if n == old {
				t.Errorf("%s is both a skill and an alias", old)
			}
		}
	}
}

// The naming rule: the first word says what the skill does, and edit- is reserved for skills that write (and only they).
func TestSkillNamesFollowTheVerbRule(t *testing.T) {
	verbs := map[string]bool{"inspect": true, "investigate": true, "check": true, "verify": true, "validate": true, "compare": true,
		"find": true, "edit": true, "review": true, "author": true, "plan": true}
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range all {
		verb := strings.SplitN(m.Name, "-", 2)[0]
		if !verbs[verb] {
			t.Errorf("%s: %q is not one of the skill verbs", m.Name, verb)
		}
		if (verb == "edit") != (m.Class == "write") {
			t.Errorf("%s: edit- is for write skills and only them (class %q)", m.Name, m.Class)
		}
	}
}
