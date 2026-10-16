package fwd

import (
	"strings"
	"testing"
)

func TestAnnotateDiagnosticsShowsTheImportLineAndSaysWhatInvalidModulePathLeavesOut(t *testing.T) {
	src := "import \"Some/Module/Path\";\n\nforeach d in network.devices select d.name"
	l, c := int32(0), int32(0)
	d := AnnotateDiagnostics(src, []QueryDiagnostic{{Message: "Invalid module path", Line: &l, Column: &c}, {Message: "other"}})
	if d[0].SourceLine != `import "Some/Module/Path";` || !strings.Contains(d[0].Hint, "found no module at the path") || d[1].SourceLine != "" || d[1].Hint != "" {
		t.Errorf("%+v", d)
	}
}

// Forward's "missing 'as' at 'by'" on a group clause is correct but does not say what the clause looks like; the diagnostic carries the form.
func TestAnnotateDiagnosticsGivesTheGroupFormForAMissingAs(t *testing.T) {
	src := "foreach d in network.devices\ngroup d by d.name as k\nselect {k}"
	l, c := int32(1), int32(8)
	d := AnnotateDiagnostics(src, []QueryDiagnostic{{Message: "missing 'as' at 'by'", Line: &l, Column: &c}})
	if !strings.Contains(d[0].Hint, "group <expr> as <name> by <key> as <keyName>") {
		t.Errorf("hint %q", d[0].Hint)
	}
	if d2 := AnnotateDiagnostics("select {1}", []QueryDiagnostic{{Message: "missing 'as' at 'by'"}}); d2[0].Hint != "" {
		t.Errorf("no group on the line, no hint: %q", d2[0].Hint)
	}
}
