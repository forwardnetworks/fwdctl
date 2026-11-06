package fwd

import (
	"encoding/json"
	"strings"
	"testing"

	forward "github.com/forwardnetworks/forward-go-sdk"
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

// NQE numbers beyond 2^53 (a 64-bit counter, a large id) come back exact, not rounded; ordinary numbers stay float64 as every reader expects.
func TestRecordsKeepLargeWholeNumbersExact(t *testing.T) {
	raw := forward.NQERecord{
		"small":  json.RawMessage(`42`),
		"frac":   json.RawMessage(`1.5`),
		"edge":   json.RawMessage(`9007199254740992`),
		"big":    json.RawMessage(`9007199254740993`),
		"neg":    json.RawMessage(`-9007199254740993`),
		"u64":    json.RawMessage(`18446744073709551615`),
		"nested": json.RawMessage(`{"octets":[9007199254740993,7]}`),
		"text":   json.RawMessage(`"x"`),
	}
	row := Records([]forward.NQERecord{raw})[0]
	for k, want := range map[string]float64{"small": 42, "frac": 1.5, "edge": 9007199254740992} {
		if f, ok := row[k].(float64); !ok || f != want {
			t.Fatalf("%s = %#v, want float64 %v", k, row[k], want)
		}
	}
	out, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"big":9007199254740993`, `"neg":-9007199254740993`, `"u64":18446744073709551615`, `"octets":[9007199254740993,7]`, `"text":"x"`} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("missing %s in %s", want, out)
		}
	}
	if n, ok := row["nested"].(map[string]any)["octets"].([]any)[1].(float64); !ok || n != 7 {
		t.Fatalf("a small nested number stays float64: %#v", row["nested"])
	}
}
