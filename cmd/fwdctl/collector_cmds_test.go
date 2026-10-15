package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	forward "github.com/forwardnetworks/forward-go-sdk"
)

func fakePkg(name, body string, err error) packageDownloader {
	return func(_ context.Context, _ forward.ClientPackageType, w io.Writer) (*forward.ClientPackage, error) {
		if _, werr := io.WriteString(w, body); werr != nil {
			return nil, werr
		}
		if err != nil {
			return nil, err
		}
		return &forward.ClientPackage{FileName: name, Size: int64(len(body)), SHA256: "abc"}, nil
	}
}

func TestCollectorDownloadWritesTheNamedFileAndPrintsItsDigest(t *testing.T) {
	dir := t.TempDir()
	var out, errb bytes.Buffer
	code := downloadCollector(context.Background(), fakePkg("fwd-unix-26.9.0-18.tar.gz", "PKG", nil), forward.ClientPackageHeadlessLinux, filepath.Join(dir, "new"), false, "26.9.0-18", &out, &errb)
	b, rerr := os.ReadFile(filepath.Join(dir, "new", "fwd-unix-26.9.0-18.tar.gz"))
	if code != 0 || rerr != nil || string(b) != "PKG" || !strings.Contains(out.String(), `"sha256":"abc"`) || !strings.Contains(out.String(), `"bytes":3`) {
		t.Fatalf("code %d err %v out %s %s", code, rerr, out.String(), errb.String())
	}
}

func TestCollectorDownloadLeavesNothingWhenItFailsAndDoesNotOverwriteWithoutForce(t *testing.T) {
	dir := t.TempDir()
	var out, errb bytes.Buffer
	if code := downloadCollector(context.Background(), fakePkg("x", "HALF", errors.New("403 Non-collector user required")), forward.ClientPackageHeadlessLinux, dir, false, "", &out, &errb); code != 3 || !strings.Contains(errb.String(), "403") {
		t.Fatalf("code %d %s", code, errb.String())
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Fatalf("a failed download left files: %v", ents)
	}
	_ = os.WriteFile(filepath.Join(dir, "p.tgz"), []byte("OLD"), 0o644)
	if code := downloadCollector(context.Background(), fakePkg("p.tgz", "NEW", nil), forward.ClientPackageHeadlessLinux, dir, false, "", &out, &errb); code != 64 {
		t.Fatalf("an existing file was not protected: %d", code)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "p.tgz")); string(b) != "OLD" {
		t.Fatalf("overwritten: %s", b)
	}
	if code := downloadCollector(context.Background(), fakePkg("../../p.tgz", "NEW", nil), forward.ClientPackageHeadlessLinux, dir, true, "", &out, &errb); code != 0 {
		t.Fatalf("force: %d", code)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "p.tgz")); string(b) != "NEW" {
		t.Fatalf("a server-supplied path must be reduced to its base name and --force replaces: %s", b)
	}
}
