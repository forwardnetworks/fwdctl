package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func eaFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A numbered file decodes: the address bytes, the MAC integer and the cloud id come out readable, and the one fault in the fixture (two assets on one MAC) is the only problem.
func TestEdgeAssetsCheckDecodesANumberedFileAndFindsTheSharedMAC(t *testing.T) {
	rep, err := checkEdgeAssets(eaFixture(t, "edge-assets.valid.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Assets) != 4 {
		t.Fatalf("assets = %d, want 4", len(rep.Assets))
	}
	if rep.Assets[0].IPs[0] != "10.0.0.1" || rep.Assets[1].MAC != "02:00:00:00:00:01" || rep.Assets[2].CloudHostID != "i-0123456789abcdef0" {
		t.Fatalf("decoded wrong: %+v", rep.Assets)
	}
	if len(rep.NamedOnly) != 0 {
		t.Fatalf("a numbered file has no name-only lines: %v", rep.NamedOnly)
	}
	if len(rep.DuplicateMACs) != 1 || rep.Problems != 1 {
		t.Fatalf("want exactly the shared MAC flagged, got %+v (problems %d)", rep.DuplicateMACs, rep.Problems)
	}
}

// Fields written by name only are the silent-ignore case: every one is reported with its line, and the asset's own problems say which number is missing.
func TestEdgeAssetsCheckFlagsEveryNameOnlyField(t *testing.T) {
	rep, err := checkEdgeAssets(eaFixture(t, "edge-assets.named-ignored.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.NamedOnly) != 11 || rep.Problems != 11 {
		t.Fatalf("named-only lines = %v, problems = %d, want 11 each", rep.NamedOnly, rep.Problems)
	}
	if !strings.Contains(strings.Join(rep.Assets[0].Problems, "\n"), "field number (1)") {
		t.Fatalf("the problem should name the missing number: %v", rep.Assets[0].Problems)
	}
}

func TestEdgeAssetsCheckFlagsNamelessUnmappableAndShortAddress(t *testing.T) {
	rep, err := checkEdgeAssets("edge_assets 1 {\n  ips 3 { address 1: \"\\012\\000\" }\n}\nedge_assets 1 {\n  name 2: \"x\"\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(append(rep.Assets[0].Problems, rep.Assets[1].Problems...), "\n")
	for _, want := range []string{"2 bytes", "no name", "cannot map it to a host"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in %s", want, all)
		}
	}
}

func TestEdgeAssetsCheckRejectsBrokenText(t *testing.T) {
	for _, src := range []string{"edge_assets 1 {\n name 2: \"x\"\n", "edge_assets 1 { name 2: \"x }", "}"} {
		if _, err := checkEdgeAssets(src); err == nil {
			t.Errorf("%q should not parse", src)
		}
	}
}

// The file inside a snapshot package is found by its name wherever it sits; a package without one says what that means.
func TestEdgeAssetsFileIsReadFromAZipAndAMissingEntryExplains(t *testing.T) {
	dir := t.TempDir()
	mk := func(name string, entry string) string {
		p := filepath.Join(dir, name)
		f, _ := os.Create(p)
		w := zip.NewWriter(f)
		e, _ := w.Create(entry)
		e.Write([]byte(eaFixture(t, "edge-assets.valid.txt")))
		w.Close()
		f.Close()
		return p
	}
	text, src, err := readEdgeAssetsFile(mk("a.zip", "snap/edge-assets.txt"))
	if err != nil || src != "snap/edge-assets.txt" || !strings.Contains(text, "synthetic-web-01") {
		t.Fatalf("zip read: %v %q", err, src)
	}
	if _, _, err := readEdgeAssetsFile(mk("b.zip", "other.txt")); err == nil || !strings.Contains(err.Error(), "no edge assets") {
		t.Fatalf("a package without the file should say so: %v", err)
	}
}
