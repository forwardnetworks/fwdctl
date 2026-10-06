package skills

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// Source is what a host serves verbatim, so for EVERY skill it must be the file as shipped: frontmatter first, naming
// the skill, with the same description Describe reports.
func TestSourceIsTheShippedSkillMd(t *testing.T) {
	names := Names()
	if len(names) == 0 {
		t.Fatal("no skills registered: the test would assert nothing")
	}
	for _, n := range names {
		src, err := Source(n)
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		if !bytes.HasPrefix(src, []byte("---\n")) {
			t.Errorf("%s: SKILL.md must begin with YAML frontmatter", n)
		}
		if !strings.Contains(string(src), "\nname: "+n+"\n") {
			t.Errorf("%s: frontmatter must name the skill", n)
		}
		m, err := Describe(n)
		if err != nil || !strings.Contains(string(src), m.Description[:min(len(m.Description), 40)]) {
			t.Errorf("%s: Source and Describe disagree (%v)", n, err)
		}
	}
}

func TestSourceOfAnUnknownSkillIsErrUnknown(t *testing.T) {
	if _, err := Source("no-such-skill-xyz"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("want ErrUnknown, got %v", err)
	}
}
