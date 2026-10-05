package skills

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var (
	numberedStep = regexp.MustCompile(`(?m)^(\d+)\. `)
	freedomLine  = regexp.MustCompile(`(?m)^- \*\*(Fixed|Guided|Open)\*\* \(steps ([0-9, ]+)\):`)
)

// Every playbook gives each numbered step one degree of freedom (Fixed, Guided or Open), and a step that writes through an
// edit-* skill (it names one and says to follow plan-safe-write) is Fixed. A model of any size then knows where it may improvise and where it may not.
func TestEveryPlaybookStepHasOneDegreeOfFreedomAndWritesAreFixed(t *testing.T) {
	for _, name := range Names() {
		if !strings.HasPrefix(name, "plan-") || name == "plan-investigation" {
			continue
		}
		b, _ := docs.ReadFile(name + "/SKILL.md")
		doc := string(b)
		i := strings.Index(doc, "\n## Freedom\n")
		if i < 0 {
			t.Errorf("%s: no ## Freedom section", name)
			continue
		}
		end := strings.Index(doc[i+1:], "\n## ")
		section, rest := doc[i:], doc[:i]
		if end >= 0 {
			section, rest = doc[i:i+1+end], doc[:i]+doc[i+1+end:]
		}
		level := map[int]string{}
		for _, m := range freedomLine.FindAllStringSubmatch(section, -1) {
			for _, f := range strings.Split(m[2], ",") {
				n, _ := strconv.Atoi(strings.TrimSpace(f))
				if prev, dup := level[n]; dup {
					t.Errorf("%s: step %d is both %s and %s", name, n, prev, m[1])
				}
				level[n] = m[1]
			}
		}
		// the numbered steps, each with its block of text up to the next one (or the next heading)
		idx := numberedStep.FindAllStringSubmatchIndex(rest, -1)
		steps := map[int]string{}
		for k, loc := range idx {
			n, _ := strconv.Atoi(rest[loc[2]:loc[3]])
			stop := len(rest)
			if k+1 < len(idx) {
				stop = idx[k+1][0]
			}
			if h := strings.Index(rest[loc[0]:], "\n## "); h >= 0 && loc[0]+h < stop {
				stop = loc[0] + h
			}
			steps[n] = rest[loc[0]:stop]
		}
		for n, text := range steps {
			lv, ok := level[n]
			if !ok {
				t.Errorf("%s: step %d has no degree of freedom", name, n)
				continue
			}
			if strings.Contains(text, "`edit-") && strings.Contains(text, "plan-safe-write") && lv != "Fixed" {
				t.Errorf("%s: step %d calls an edit-* skill, so it must be Fixed (it is %s)", name, n, lv)
			}
		}
		for n := range level {
			if _, ok := steps[n]; !ok {
				t.Errorf("%s: Freedom names step %d, which does not exist", name, n)
			}
		}
	}
}

func TestRouterDefinesTheDegreesOfFreedom(t *testing.T) {
	b, _ := docs.ReadFile("plan-investigation/SKILL.md")
	for _, w := range []string{"**Fixed:**", "**Guided:**", "**Open:**"} {
		if !strings.Contains(string(b), w) {
			t.Errorf("plan-investigation does not define %s", w)
		}
	}
}
