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
