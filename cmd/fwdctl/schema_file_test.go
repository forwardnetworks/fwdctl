package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/nqelint"
	"github.com/forwardnetworks/fwdctl/nqeschema"
)

// Forward's schema JSON in a checkout holds raw newlines inside its docString values, which strict JSON refuses. The repair escapes a control
// character inside a string and changes nothing else: valid JSON comes back byte for byte, an escaped quote does not end the string, and text
// outside a string is untouched.
func TestEscapeRawControlsInStrings(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{`{"a":"x\ny"}`, `{"a":"x\ny"}`},                     // already valid: unchanged
		{"{\"a\":\"line1\nline2\"}", `{"a":"line1\nline2"}`}, // a raw newline inside a string
		{"{\"a\":\"t\tc\rd\"}", `{"a":"t\tc\rd"}`},           // tab and carriage return
		{"{\"a\":\"q\\\"\nz\"}", "{\"a\":\"q\\\"\\nz\"}"},    // an escaped quote does not close the string
		{"{\n  \"a\" : \"x\"\n}", "{\n  \"a\" : \"x\"\n}"},   // whitespace between tokens is not touched
		{"{\"a\":\"b\u0001c\"}", `{"a":"b\u0001c"}`},         // another control character
	} {
		if got := string(escapeRawControlsInStrings([]byte(c.in))); got != c.want {
			t.Errorf("escape(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLintSchemaFileFlagRefusals(t *testing.T) {
	t.Cleanup(nqelint.ResetOrgSchema) // the schema is process-wide: never leave a test file's schema in place for the next test
	dir := t.TempDir()
	q := filepath.Join(dir, "q.nqe")
	notSchema := filepath.Join(dir, "x.json")
	empty := filepath.Join(dir, "empty.json")
	for path, body := range map[string]string{
		q:         "foreach device in network.devices\nselect {name: device.name}\n",
		notSchema: `{"not":"a schema"}`,
		empty:     `{}`,
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"both flags":   {[]string{"nqe", "lint", "--org", "--schema-file", notSchema, q}, "not both"},
		"missing file": {[]string{"nqe", "lint", "--schema-file", filepath.Join(dir, "nope"), q}, "could not read the schema file"},
		"not a schema": {[]string{"nqe", "lint", "--schema-file", notSchema, q}, "not an NQE schema"},
		"empty object": {[]string{"nqe", "lint", "--schema-file", empty, q}, "not an NQE schema"},
		"a directory":  {[]string{"nqe", "lint", "--schema-file", dir, q}, "regular file"},
	} {
		code, out, errs := call(t, c.args, "", nil)
		// refused with its own message, and it did not lint against an empty schema (no schema block, no unknown-field noise)
		if code == 0 || !strings.Contains(errs, c.want) || strings.Contains(out, `"source": "file"`) || strings.Contains(out, "does not have field") || nqeschema.UsingOrgSchema() {
			t.Errorf("%s: want %q, exit %d, stdout %q, stderr %q, schema switched %v", name, c.want, code, out, errs, nqeschema.UsingOrgSchema())
		}
	}
}
