package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
)

// bundleRoutes is a library with one entry query (/Team/Entry, id Q_e) that imports nothing.
func bundleRoutes() map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		"GET /api/nqe/repos/org/commits/head": fwdtest.Const(200, "c1"),
		"GET /api/nqe/repos/org/commits/head/queries": fwdtest.Const(200, map[string]any{"queries": []any{
			map[string]any{"path": "/Team/Entry", "lastCommitId": "c1", "queryId": "Q_e"}}}),
		"GET /api/nqe/queries/Q_e/source-code": fwdtest.Const(200, map[string]any{"sourceCode": "foreach d in network.devices select {original: d.name}"}),
		"GET /api/nqe/repos/org/commits/c1/queries": func(*http.Request, []byte) (int, any) {
			return 200, map[string]any{"sourceCode": "foreach d in network.devices select {original: d.name}", "queryId": "Q_e"}
		},
	}
}

func writeFile(t *testing.T, content string) string {
	p := filepath.Join(t.TempDir(), "o.nqe")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBundleAppliesAnOverrideOfTheEntryByIDAndByPathAndRefusesOneThatMatchedNothing(t *testing.T) {
	over := writeFile(t, "foreach d in network.devices select {replaced: d.name}")
	for name, args := range map[string][]string{
		"by id":   {"nqe", "bundle", "--query-id", "Q_e", "--override", "/Team/Entry=" + over},
		"by path": {"nqe", "bundle", "--path", "/Team/Entry", "--override", "/Team/Entry=" + over},
	} {
		code, out, errb := call(t, args, "", bundleRoutes())
		if code != 0 || !strings.Contains(out, "replaced") || strings.Contains(out, "original") {
			t.Fatalf("%s: the entry override must replace the entry body: exit %d out=%q err=%s", name, code, out, errb)
		}
	}
	code, out, _ := call(t, []string{"nqe", "bundle", "--query-id", "Q_e"}, "", bundleRoutes())
	if code != 0 || !strings.Contains(out, "original") {
		t.Fatalf("without an override the library's entry is used: %d %q", code, out)
	}
	// an override that matches neither the entry nor an import is an error, never a silent no-op
	code, out, errb := call(t, []string{"nqe", "bundle", "--query-id", "Q_e", "--override", "/Team/Nope=" + over}, "", bundleRoutes())
	if code != 1 || out != "" || !strings.Contains(errb, "not used by the bundle") || !strings.Contains(errb, "/Team/Nope") {
		t.Fatalf("an unused override must fail: exit %d out=%q err=%s", code, out, errb)
	}
}
