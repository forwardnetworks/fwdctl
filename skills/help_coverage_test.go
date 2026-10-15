package skills_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The help a person reads is built from each skill's reference notes, so a new value of an input enum (an area, object, kind, view or action) that no note mentions is invisible: `fwdctl run NAME --help`
// would not explain it. This fails the build when a value is in a skill's schema and in none of its notes, so the next addition must document itself.
func TestEveryEnumValueOfAnInputIsDocumentedInTheSkillsNotes(t *testing.T) {
	schemas, _ := filepath.Glob("*/schema.json")
	if len(schemas) < 40 {
		t.Fatalf("found only %d skill schemas; the test would prove nothing", len(schemas))
	}
	checked := 0
	for _, sp := range schemas {
		dir := filepath.Dir(sp)
		var sc struct {
			Properties map[string]struct {
				Enum []any `json:"enum"`
			} `json:"properties"`
		}
		b, _ := os.ReadFile(sp)
		if json.Unmarshal(b, &sc) != nil {
			t.Errorf("%s does not parse", sp)
			continue
		}
		var docs strings.Builder
		files, _ := filepath.Glob(filepath.Join(dir, "*.md"))
		more, _ := filepath.Glob(filepath.Join(dir, "reference", "*.md"))
		for _, f := range append(files, more...) {
			d, _ := os.ReadFile(f)
			docs.Write(d)
		}
		text := docs.String()
		for _, prop := range []string{"area", "object", "kind", "view", "action"} {
			for _, v := range sc.Properties[prop].Enum {
				s, ok := v.(string)
				if !ok || s == "" {
					continue
				}
				checked++
				if !strings.Contains(text, s) {
					t.Errorf("%s: %s value %q is in the schema but in none of the skill's notes (SKILL.md, reference/*.md), so --help cannot explain it", filepath.Base(dir), prop, s)
				}
			}
		}
	}
	if checked < 100 {
		t.Fatalf("checked only %d enum values; the test would prove nothing", checked)
	}
}
