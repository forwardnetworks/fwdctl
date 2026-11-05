package main

import (
	"os"
	"path/filepath"
	"testing"
)

// --meta pointing at the file stdout is redirected to would overwrite the rows: sameFile spots it, and a different file is not the same.
func TestMetaSameFileAsRedirectedStdout(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "out.json")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if !sameFile(f, p) {
		t.Errorf("the open file and its own path must be the same file")
	}
	if sameFile(f, filepath.Join(dir, "other.json")) {
		t.Errorf("a path that does not exist is not the same file")
	}
}
