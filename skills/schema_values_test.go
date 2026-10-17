package skills_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Every view, kind and object of a skill says in one line what it answers (the x-values annotation of its schema, shown by `fwdctl run NAME --help`). This is how a new view cannot be bolted on without being described where
// an agent reads first, and the description in the always-loaded list does not have to carry it.
func TestEveryViewKindAndObjectSaysWhatItAnswers(t *testing.T) {
	schemas, _ := filepath.Glob("*/schema.json")
	if len(schemas) < 40 {
		t.Fatalf("found %d schemas", len(schemas))
	}
	checked := 0
	for _, p := range schemas {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var s struct {
			Properties map[string]struct {
				Enum    []string          `json:"enum"`
				XValues map[string]string `json:"x-values"`
			} `json:"properties"`
		}
		if json.Unmarshal(b, &s) != nil {
			continue
		}
		for _, name := range []string{"view", "kind", "object"} {
			prop, ok := s.Properties[name]
			if !ok || len(prop.Enum) == 0 {
				continue
			}
			checked++
			for _, v := range prop.Enum {
				if prop.XValues[v] == "" {
					t.Errorf("%s: %s %q has no x-values line (one line saying what it answers)", p, name, v)
				}
			}
			for v := range prop.XValues {
				found := false
				for _, e := range prop.Enum {
					if e == v {
						found = true
					}
				}
				if !found {
					t.Errorf("%s: x-values names %q, which is not a %s", p, v, name)
				}
			}
		}
	}
	if checked < 12 {
		t.Errorf("only %d view/kind/object properties were checked", checked)
	}
}
