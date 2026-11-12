package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
)

func deviceFileRoutes(content string) map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/snapshots":                   fwdtest.Snapshots(fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")),
		"GET /api/networks/n1/devices/r1/files/config.txt": fwdtest.Const(200, []byte(content)),
	}
}

// a long line must come back whole (the read modes cut it at 400 characters), and the secret on another line must not.
func TestDeviceFileDownloadWritesTheWholeFileAndRedactsByDefault(t *testing.T) {
	long := "list [ " + strings.Repeat("site.example.invalid ", 60) + "];"
	content := "hostname r1\n" + long + "\nsnmp-server community s3cr3tvalue RO\n"
	out := filepath.Join(t.TempDir(), "r1.txt")
	code, stdout, errb := call(t, []string{"device-file", "download", "n1", "r1", "config.txt", "--out", out}, "", deviceFileRoutes(content))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), long) {
		t.Errorf("the long line (%d characters) must be written whole", len(long))
	}
	if strings.Contains(string(got), "s3cr3tvalue") || !strings.Contains(string(got), "<redacted>") {
		t.Errorf("the community must be redacted by default: %q", got)
	}
	if st, _ := os.Stat(out); st.Mode().Perm() != 0o600 {
		t.Errorf("the file must be readable by its owner only, got %v", st.Mode().Perm())
	}
	var res map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &res); err != nil || res["redacted"] != true || res["redacted_lines"] != float64(1) || res["snapshot_id"] != "s1" {
		t.Errorf("result line: %s (%v)", stdout, err)
	}
}

func TestDeviceFileDownloadNoRedactIsRawAndSaysSo(t *testing.T) {
	out := filepath.Join(t.TempDir(), "r1.txt")
	code, stdout, errb := call(t, []string{"device-file", "download", "n1", "r1", "config.txt", "--out", out, "--no-redact"}, "", deviceFileRoutes("snmp-server community s3cr3tvalue RO\n"))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	got, _ := os.ReadFile(out)
	if !strings.Contains(string(got), "s3cr3tvalue") || !strings.Contains(stdout, "unredacted") {
		t.Errorf("--no-redact must write the raw text and warn: file=%q stdout=%s", got, stdout)
	}
}

func TestDeviceFileDownloadNeverOverwrites(t *testing.T) {
	out := filepath.Join(t.TempDir(), "r1.txt")
	if err := os.WriteFile(out, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errb := call(t, []string{"device-file", "download", "n1", "r1", "config.txt", "--out", out}, "", deviceFileRoutes("new\n"))
	got, _ := os.ReadFile(out)
	if code == 0 || string(got) != "keep me" || !strings.Contains(errb, "already exists") {
		t.Fatalf("an existing path must be refused and left alone: exit %d err=%s file=%q", code, errb, got)
	}
}

// a file over the limit is not written: a cut file must never pass for the whole one.
func TestDeviceFileDownloadRefusesAFileOverTheLimit(t *testing.T) {
	big := strings.Repeat("x\n", 1<<20+1) // a little over 2 MiB
	out := filepath.Join(t.TempDir(), "r1.txt")
	code, _, errb := call(t, []string{"device-file", "download", "n1", "r1", "config.txt", "--out", out, "--max-mib", "1"}, "", deviceFileRoutes(big))
	if code == 0 || !strings.Contains(errb, "larger than 1 MiB") {
		t.Fatalf("an oversize file must be refused and say why: exit %d err=%s", code, errb)
	}
	if _, err := os.Stat(out); err == nil {
		t.Errorf("no file may be left behind")
	}
}
