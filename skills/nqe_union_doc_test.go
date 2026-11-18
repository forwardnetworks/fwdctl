package skills_test

import (
	"os"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/nqelint"
	"github.com/forwardnetworks/fwdctl/nqeschema"
)

// The union section of reference/gotchas.md teaches how to read a OneOf value (VlanIdOrRange) with `when ... is`. Its examples were run on a live Forward; this keeps them honest against the offline
// linter: every indented example that starts with "// reads a union" must lint clean, and every one that starts with "// does not" must be rejected.
func TestUnionSectionExamplesLintAsTheyClaim(t *testing.T) {
	if !nqeschema.Available() {
		t.Skip("this build carries no schema")
	}
	b, err := os.ReadFile("author-nqe-query/reference/gotchas.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	i := strings.Index(text, "## Reading a union value")
	j := strings.Index(text, "\n## Time")
	if i < 0 || j < i {
		t.Fatal("the union section is gone")
	}
	var blocks [][]string
	var cur []string
	for _, line := range strings.Split(text[i:j], "\n") {
		if strings.HasPrefix(line, "    ") {
			cur = append(cur, strings.TrimPrefix(line, "    "))
			continue
		}
		if line == "" && len(cur) > 0 {
			cur = append(cur, "")
			continue
		}
		if len(cur) > 0 {
			blocks = append(blocks, cur)
			cur = nil
		}
	}
	if len(cur) > 0 {
		blocks = append(blocks, cur)
	}
	// a block can hold several examples separated by a blank line followed by a comment line: split on lines that start with "// "
	var examples [][]string
	for _, bl := range blocks {
		var ex []string
		for _, l := range bl {
			if strings.HasPrefix(l, "// ") && len(ex) > 0 {
				examples = append(examples, ex)
				ex = nil
			}
			ex = append(ex, l)
		}
		if len(ex) > 0 {
			examples = append(examples, ex)
		}
	}
	good, bad := 0, 0
	for _, ex := range examples {
		src := strings.TrimSpace(strings.Join(ex, "\n"))
		switch {
		case strings.HasPrefix(src, "// reads a union"):
			good++
			if ds := nqelint.LintWith(src, nqelint.Modules); nqelint.HasErrors(ds) {
				t.Errorf("an example that should lint clean does not: %v\n%s", ds, src)
			}
		case strings.HasPrefix(src, "// does not"):
			bad++
			if ds := nqelint.LintWith(src, nqelint.Modules); !nqelint.HasErrors(ds) {
				t.Errorf("an example that should be rejected lints clean:\n%s", src)
			}
		}
	}
	if good < 3 || bad < 2 {
		t.Errorf("found %d examples that should pass and %d that should fail; the section changed shape", good, bad)
	}
}
