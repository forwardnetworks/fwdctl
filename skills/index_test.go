package skills

import (
	"os"
	"strings"
	"testing"
)

const (
	indexBegin = "<!-- skill-index:begin (generated: UPDATE_SKILL_INDEX=1 go test ./skills -run TestSkillIndex) -->"
	indexEnd   = "<!-- skill-index:end -->"
)

// skillIndex is the A to Z table in docs/skills.md: every skill with its kind and what it answers, from the SKILL.md
// frontmatter, so the index cannot drift from the skills that ship.
func skillIndex() string {
	var b strings.Builder
	b.WriteString("| Skill | Kind | What it does |\n|---|---|---|\n")
	for _, name := range Names() {
		m, err := Describe(name)
		if err != nil {
			continue
		}
		kind := "read"
		switch {
		case strings.HasPrefix(name, "plan-"):
			kind = "playbook"
		case m.Class == "write":
			kind = "edit"
		}
		what := m.Description
		if i := strings.Index(what, " Use "); i > 0 {
			what = what[:i]
		}
		b.WriteString("| `" + name + "` | " + kind + " | " + strings.ReplaceAll(what, "|", "/") + " |\n")
	}
	return b.String()
}

func TestSkillIndexInDocsMatchesTheSkills(t *testing.T) {
	const path = "../docs/skills.md"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	i, j := strings.Index(doc, indexBegin), strings.Index(doc, indexEnd)
	if i < 0 || j < i {
		t.Fatalf("docs/skills.md has no %q ... %q block", indexBegin, indexEnd)
	}
	want := doc[:i+len(indexBegin)] + "\n\n" + skillIndex() + "\n" + doc[j:]
	if want == doc {
		return
	}
	if os.Getenv("UPDATE_SKILL_INDEX") == "1" {
		if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatal("the A to Z index in docs/skills.md is out of date: run UPDATE_SKILL_INDEX=1 go test ./skills -run TestSkillIndex")
}
