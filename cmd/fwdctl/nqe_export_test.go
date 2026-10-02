package main

import (
	"archive/zip"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
)

// exportLibrary is a synthetic library: /Lib/Entry imports /Lib/Sub/Helpers and @fwd/L3/IpAddressUtils; Helpers imports /Lib/Sub/Shapes and reads a data file.
func exportLibrary() map[string]fwdtest.Handler {
	src := map[string]string{
		"/Lib/Entry":       "import \"Lib/Sub/Helpers\";\nimport \"@fwd/L3/IpAddressUtils\";\n@query\nq = foreach d in network.devices select {n: helper(d.name)};",
		"/Lib/Sub/Helpers": "import \"Lib/Sub/Shapes\";\nexport helper(x: String) = shape(x) + network.extensions.ownerTable.status;",
		"/Lib/Sub/Shapes":  "export shape(x: String) = x;",
	}
	ids := map[string]string{"/Lib/Entry": "Q_entry", "/Lib/Sub/Helpers": "Q_help", "/Lib/Sub/Shapes": "Q_shape"}
	var listing []any
	for p, id := range ids {
		listing = append(listing, map[string]any{"path": p, "lastCommitId": "c1", "queryId": id})
	}
	return map[string]fwdtest.Handler{
		"GET /api/nqe/repos/org/commits/head":         fwdtest.Const(200, "c1"),
		"GET /api/nqe/repos/org/commits/head/queries": fwdtest.Const(200, map[string]any{"queries": listing}),
		"GET /api/nqe/repos/org/commits/c1/queries": func(r *http.Request, _ []byte) (int, any) {
			p := r.URL.Query().Get("path")
			if s, ok := src[p]; ok {
				return 200, map[string]any{"sourceCode": s, "queryId": ids[p]}
			}
			return 404, map[string]any{"message": "not found"}
		},
		"GET /api/nqe/queries/Q_entry/source-code": fwdtest.Const(200, map[string]any{"sourceCode": src["/Lib/Entry"]}),
		"GET /api/data-files":                      fwdtest.Const(200, []any{map[string]any{"name": "owners.csv", "nqeName": "ownerTable", "type": "CSV", "networkIds": []string{}}}),
	}
}

func TestExportWritesAFolderTreeThatMirrorsTheLibraryWithAManifest(t *testing.T) {
	out := filepath.Join(t.TempDir(), "tree")
	zipPath := filepath.Join(t.TempDir(), "x.zip")
	code, _, errb := call(t, []string{"nqe", "export", "--query-id", "Q_entry", "--out", out, "--zip", zipPath}, "", exportLibrary())
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	for _, f := range []string{"Lib/Entry.nqe", "Lib/Sub/Helpers.nqe", "Lib/Sub/Shapes.nqe", "manifest.json"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
	// imports are untouched, so the tree re-imports as it is
	b, _ := os.ReadFile(filepath.Join(out, "Lib/Sub/Helpers.nqe"))
	if !strings.Contains(string(b), `import "Lib/Sub/Shapes";`) {
		t.Errorf("the import must be left exactly as written: %s", b)
	}
	var m exportManifest
	mb, _ := os.ReadFile(filepath.Join(out, "manifest.json"))
	if err := json.Unmarshal(mb, &m); err != nil {
		t.Fatal(err)
	}
	if m.Entry != "/Lib/Entry" || m.CommitID != "c1" || len(m.Files) != 3 || !m.Files[0].Entry || m.Files[0].QueryID != "Q_entry" || m.Files[0].SHA256 == "" {
		t.Errorf("manifest: %+v", m)
	}
	if len(m.Files[0].Imports) != 1 || m.Files[0].Imports[0] != "/Lib/Sub/Helpers" || len(m.Files[0].BuiltinImports) != 1 {
		t.Errorf("import graph: %+v", m.Files[0])
	}
	if len(m.DataFiles) != 1 || m.DataFiles[0].NQEName != "ownerTable" || m.DataFiles[0].DataFile != "owners.csv" || m.DataFiles[0].Status != "found" || m.DataFiles[0].UsedBy[0] != "/Lib/Sub/Helpers" {
		t.Errorf("data files: %+v", m.DataFiles)
	}
	if !strings.Contains(errb, "owners.csv") {
		t.Errorf("the data file dependency must be said on stderr: %s", errb)
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	if len(zr.File) != 4 {
		t.Errorf("zip holds %d files, want 3 queries + manifest", len(zr.File))
	}
}

func TestExportRefusesANonEmptyDirectoryAnUnusedOverrideAndAMissingModule(t *testing.T) {
	full := t.TempDir()
	os.WriteFile(filepath.Join(full, "keep.txt"), []byte("x"), 0o644)
	if code, _, errb := call(t, []string{"nqe", "export", "--path", "/Lib/Entry", "--out", full}, "", exportLibrary()); code != 64 || !strings.Contains(errb, "not empty") {
		t.Errorf("a non-empty --out is refused: %d %s", code, errb)
	}
	over := writeFile(t, "export unused() = 1;")
	out := filepath.Join(t.TempDir(), "o")
	if code, _, errb := call(t, []string{"nqe", "export", "--path", "/Lib/Entry", "--out", out, "--override", "/Lib/Nope=" + over}, "", exportLibrary()); code != 1 || !strings.Contains(errb, "matched neither") {
		t.Errorf("an override that matches nothing is an error: %d %s", code, errb)
	}
	if _, err := os.Stat(filepath.Join(out, "manifest.json")); err == nil {
		t.Error("nothing may be written when the export is refused")
	}
	routes := exportLibrary()
	routes["GET /api/nqe/repos/org/commits/c1/queries"] = func(r *http.Request, _ []byte) (int, any) {
		if r.URL.Query().Get("path") == "/Lib/Entry" {
			return 200, map[string]any{"sourceCode": "import \"Lib/Gone\";\n@query\nq = [{a: 1}];", "queryId": "Q_entry"}
		}
		return 404, map[string]any{"message": "not found"}
	}
	if code, _, errb := call(t, []string{"nqe", "export", "--path", "/Lib/Entry", "--out", filepath.Join(t.TempDir(), "m")}, "", routes); code != 1 || !strings.Contains(errb, "/Lib/Gone") {
		t.Errorf("an import the library does not hold is named: %d %s", code, errb)
	}
}

func TestExportAnOverrideReplacesAModuleAndIsRecordedInTheManifest(t *testing.T) {
	over := writeFile(t, `export shape(x: String) = x + "1";`)
	out := filepath.Join(t.TempDir(), "tree")
	if code, _, errb := call(t, []string{"nqe", "export", "--path", "/Lib/Entry", "--out", out, "--override", "/Lib/Sub/Shapes=" + over}, "", exportLibrary()); code != 0 {
		t.Fatalf("%d %s", code, errb)
	}
	b, _ := os.ReadFile(filepath.Join(out, "Lib/Sub/Shapes.nqe"))
	if !strings.Contains(string(b), `x + "1"`) {
		t.Errorf("the override text must be exported: %s", b)
	}
	var m exportManifest
	mb, _ := os.ReadFile(filepath.Join(out, "manifest.json"))
	json.Unmarshal(mb, &m)
	for _, f := range m.Files {
		if f.LibraryPath == "/Lib/Sub/Shapes" && (f.LocalFile == "" || f.QueryID != "") {
			t.Errorf("a local file is recorded as such, with no library query id: %+v", f)
		}
	}
}

func packJSON(t *testing.T, args ...string) (map[string]any, string, int) {
	t.Helper()
	code, out, errb := call(t, append([]string{"nqe", "pack"}, args...), "", nil)
	var m map[string]any
	if code == 0 {
		if err := json.Unmarshal([]byte(out), &m); err != nil {
			t.Fatalf("pack must print JSON: %v\n%s", err, out)
		}
	}
	return m, errb, code
}

func exportedTree(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "tree")
	if code, _, errb := call(t, []string{"nqe", "export", "--path", "/Lib/Entry", "--out", out}, "", exportLibrary()); code != 0 {
		t.Fatalf("export: %d %s", code, errb)
	}
	return out
}

func TestPackTurnsAnExportedTreeIntoTheEditInputAndReportsWhatChanged(t *testing.T) {
	tree := exportedTree(t)
	os.WriteFile(filepath.Join(tree, "Lib/Sub/Shapes.nqe"), []byte(`export shape(x: String) = x + "2";`), 0o644) // edited
	os.WriteFile(filepath.Join(tree, "Lib/Sub/Extra.nqe"), []byte(`export extra(x: String) = x;`), 0o644)        // new
	os.Remove(filepath.Join(tree, "Lib/Sub/Helpers.nqe"))                                                        // gone
	in, errb, code := packJSON(t, tree, "--message", "load", "--create-directory", "--typecheck", "--basis-commit-id", "manifest")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	ch := in["changes"].([]any)
	if len(ch) != 3 || ch[0].(map[string]any)["path"] != "/Lib/Entry" || ch[1].(map[string]any)["path"] != "/Lib/Sub/Extra" {
		t.Errorf("changes must be library paths, sorted: %v", ch)
	}
	if in["message"] != "load" || in["create_directory"] != true || in["typecheck"] != true || in["basis_commit_id"] != "c1" {
		t.Errorf("flags must reach the input (and 'manifest' must mean the export's commit): %v", in)
	}
	for _, want := range []string{"1 modified, 1 new, 1 unchanged", "modified /Lib/Sub/Shapes", "new      /Lib/Sub/Extra", "no longer in the tree", "/Lib/Sub/Helpers"} {
		if !strings.Contains(errb, want) {
			t.Errorf("stderr must say %q:\n%s", want, errb)
		}
	}
}

func TestPackChangedOnlySendsJustTheEditedFilesAndNeedsAManifest(t *testing.T) {
	tree := exportedTree(t)
	os.WriteFile(filepath.Join(tree, "Lib/Sub/Shapes.nqe"), []byte(`export shape(x: String) = x + "2";`), 0o644)
	in, _, code := packJSON(t, tree, "--changed-only")
	if code != 0 || len(in["changes"].([]any)) != 1 || in["changes"].([]any)[0].(map[string]any)["path"] != "/Lib/Sub/Shapes" {
		t.Errorf("only the edited file: %d %v", code, in)
	}
	os.Remove(filepath.Join(tree, "manifest.json"))
	if _, errb, code := packJSON(t, tree, "--changed-only"); code != 64 || !strings.Contains(errb, "manifest.json") {
		t.Errorf("--changed-only without a manifest is a usage error: %d %s", code, errb)
	}
	// without a manifest a plain tree still packs: paths come from the file layout
	if in, _, code := packJSON(t, tree); code != 0 || len(in["changes"].([]any)) != 3 {
		t.Errorf("a tree without a manifest packs from its layout: %d %v", code, in)
	}
}

func TestPackRefusesWhatItCannotPackSafely(t *testing.T) {
	if _, errb, code := packJSON(t, filepath.Join(t.TempDir(), "nope")); code != 64 || !strings.Contains(errb, "not a directory") {
		t.Errorf("%d %s", code, errb)
	}
	empty := t.TempDir()
	if _, errb, code := packJSON(t, empty); code != 1 || !strings.Contains(errb, "no queries") {
		t.Errorf("%d %s", code, errb)
	}
	tree := exportedTree(t)
	os.Symlink("/etc/hostname", filepath.Join(tree, "Lib/Link.nqe"))
	if _, errb, code := packJSON(t, tree); code != 1 || !strings.Contains(errb, "symbolic link") {
		t.Errorf("a symlink is refused, never followed: %d %s", code, errb)
	}
}

// The bytes of one query, worked out by hand from NqeLibProtos.proto: path "/a" source "x".
//
//	QueryPathPB {path="/a"}      0a 02 2f 61
//	NqeQuerySourcePB {src="x"}   0a 01 78
//	ExportedQueryPB              0a 04 <path 4> 12 03 <source 3>          (11 bytes)
//	NqeLibExportPB               0a 0b <exported 11>
func TestUIPackageBytesMatchTheProtoSchemaByHand(t *testing.T) {
	got := encodeUIPackage([]uiQuery{{Path: "/a", Source: "x"}})
	want := []byte{0x0a, 0x0b, 0x0a, 0x04, 0x0a, 0x02, '/', 'a', 0x12, 0x03, 0x0a, 0x01, 'x'}
	if string(got) != string(want) {
		t.Fatalf("got % x want % x", got, want)
	}
	// a long source needs a multi-byte length: 300 bytes is varint ac 02
	long := strings.Repeat("y", 300)
	back, err := decodeUIPackage(encodeUIPackage([]uiQuery{{Path: "/Team/Sub/Name with space", Source: long}, {Path: "/b", Source: "é→"}}))
	if err != nil || len(back) != 2 || back[0].Source != long || back[0].Path != "/Team/Sub/Name with space" || back[1].Source != "é→" {
		t.Fatalf("round trip: %v %v", back, err)
	}
	z, err := zipUIPackage([]uiQuery{{Path: "/a", Source: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	qs, ok, err := readUIPackage(z)
	if err != nil || !ok || len(qs) != 1 || qs[0].Path != "/a" {
		t.Errorf("zip round trip: %v %v %v", qs, ok, err)
	}
	if _, err := decodeUIPackage([]byte{0x0a, 0x05, 0x01}); err == nil {
		t.Error("a truncated field is an error, not a silent empty package")
	}
}

func TestExportUIZipIsTheForwardImportFormatAndPackReadsItBack(t *testing.T) {
	out := filepath.Join(t.TempDir(), "tree")
	uiZip := filepath.Join(t.TempDir(), "ui.zip")
	treeZip := filepath.Join(t.TempDir(), "tree.zip")
	code, _, errb := call(t, []string{"nqe", "export", "--path", "/Lib/Entry", "--out", out, "--ui-zip", uiZip, "--zip", treeZip}, "", exportLibrary())
	if code != 0 || !strings.Contains(errb, "queries-export.proto") {
		t.Fatalf("%d %s", code, errb)
	}
	// the UI package holds exactly the tree's queries, entry first, with library paths that start with a slash
	zb, _ := os.ReadFile(uiZip)
	qs, ok, err := readUIPackage(zb)
	if err != nil || !ok || len(qs) != 3 || qs[0].Path != "/Lib/Entry" {
		t.Fatalf("ui package: %v %v %v", qs, ok, err)
	}
	for _, q := range qs {
		b, _ := os.ReadFile(filepath.Join(out, filepath.FromSlash(exportFileName(q.Path))))
		if string(b) != q.Source {
			t.Errorf("%s differs from the tree", q.Path)
		}
	}
	// the Forward UI refuses the tree zip; the manifest and --help say so
	tz, _ := os.ReadFile(treeZip)
	if _, ok, _ := readUIPackage(tz); ok {
		t.Error("the tree zip must not pretend to be a UI package")
	}
	mb, _ := os.ReadFile(filepath.Join(out, "manifest.json"))
	if !strings.Contains(string(mb), "NOT what the Forward UI imports") {
		t.Errorf("the manifest must say the tree is not the UI format")
	}
	if _, help, _ := call(t, []string{"nqe", "export", "--help"}, "", nil); !strings.Contains(help, "missing file queries-export.proto") {
		t.Errorf("--help must say the UI refuses the tree")
	}
	// pack reads the UI package: same changes as packing the tree
	fromZip, _, code := packJSON(t, uiZip, "--create-directory")
	fromDir, _, code2 := packJSON(t, out, "--create-directory")
	if code != 0 || code2 != 0 || len(fromZip["changes"].([]any)) != 3 {
		t.Fatalf("pack of a UI zip: %d %d %v", code, code2, fromZip)
	}
	a, _ := json.Marshal(fromZip["changes"])
	b, _ := json.Marshal(fromDir["changes"])
	if string(a) != string(b) {
		t.Errorf("a UI package and the tree it came from must pack to the same changes")
	}
	// a fwdctl tree zip is not a UI export, and pack says what to do
	if _, errb, code := packJSON(t, treeZip); code != 1 || !strings.Contains(errb, "not a Forward UI export") {
		t.Errorf("%d %s", code, errb)
	}
	if _, errb, code := packJSON(t, uiZip, "--changed-only"); code != 64 || !strings.Contains(errb, "manifest.json") {
		t.Errorf("--changed-only needs a manifest: %d %s", code, errb)
	}
}
