package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManPagesAreWrittenForEveryCommandAndLookLikeManPages(t *testing.T) {
	dir := t.TempDir()
	n, err := writeManPages((&app{}).newRoot(), dir, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || n < 20 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "fwdctl-nqe-run.1"))
	if err != nil {
		t.Fatal(err)
	}
	page := string(b)
	for _, want := range []string{".TH \"FWDCTL-NQE-RUN\" \"1\"", ".SH NAME", `fwdctl\-nqe\-run \- `, ".SH SYNOPSIS", ".SH OPTIONS", `\-\-network`, `\-\-offset`, ".SH EXAMPLES", ".SH INHERITED OPTIONS", ".SH SEE ALSO", "fwdctl\\-nqe(1)"} {
		if !strings.Contains(page, want) {
			t.Errorf("fwdctl-nqe-run.1 lacks %q", want)
		}
	}
	for _, skip := range []string{"fwdctl-redact-check.1", "fwdctl-dogfood-note.1", "fwdctl-man.1"} {
		if _, err := os.Stat(filepath.Join(dir, skip)); err == nil {
			t.Errorf("%s is for a temporary or hidden command and must not be written", skip)
		}
	}
	if code, out, _ := call(t, []string{"man", filepath.Join(dir, "x")}, "", nil); code != 0 || !strings.Contains(out, "wrote") {
		t.Errorf("fwdctl man: %d %s", code, out)
	}
}
