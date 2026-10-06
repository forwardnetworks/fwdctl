package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"v0.4.6", "v0.4.5", true}, {"v0.5.0", "v0.4.9", true}, {"v0.4.5", "v0.4.5", false}, {"v0.4.4", "v0.4.5", false}, {"v0.4.5", "v0.4.5-rc1", true}, {"v1.0.0", "dev", false}} {
		if got := newer(c.a, c.b); got != c.want {
			t.Errorf("newer(%s,%s)=%v", c.a, c.b, got)
		}
	}
}

func tarball(t *testing.T, content string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "fwdctl", Mode: 0o755, Size: int64(len(content))})
	tw.Write([]byte(content))
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestUpdateInstallsVerifiedBinary(t *testing.T) {
	u := newUpdater()
	arc := tarball(t, "NEWBINARY")
	name := u.archiveName("v9.9.9")
	sum := sha256.Sum256(arc)
	sums := hex.EncodeToString(sum[:]) + "  " + name + "\n"
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			fmt.Fprintf(w, `{"tag_name":"v9.9.9","assets":[{"name":%q,"url":"%s/a"},{"name":"SHA256SUMS","url":"%s/s"}]}`, name, srv.URL, srv.URL)
		case r.URL.Path == "/a":
			w.Write(arc)
		case r.URL.Path == "/s":
			w.Write([]byte(sums))
		}
	}))
	defer srv.Close()
	u.api = srv.URL
	rel, err := u.release(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "fwdctl")
	os.WriteFile(exe, []byte("OLD"), 0o755)
	if _, err := u.updateTo(t.Context(), rel, exe); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "NEWBINARY" {
		t.Fatalf("binary not replaced: %q", b)
	}
	// a tampered checksum must refuse and leave the old binary alone
	sums = strings.Repeat("0", 64) + "  " + name + "\n"
	os.WriteFile(exe, []byte("OLD"), 0o755)
	if _, err := u.updateTo(t.Context(), rel, exe); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("want checksum refusal, got %v", err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "OLD" {
		t.Fatalf("binary changed despite bad checksum: %q", b)
	}
}

func TestConnectionFlagsFileAndEnvironmentPrecedence(t *testing.T) {
	dir := t.TempDir()
	pw := filepath.Join(dir, "pw")
	os.WriteFile(pw, []byte("secret\n"), 0o600)
	cfg := filepath.Join(dir, "c.json")
	os.WriteFile(cfg, []byte(`{"url":"https://file.example","username":"fileuser","password_file":"`+pw+`"}`), 0o600)
	for _, k := range []string{"FORWARD_URL", "FORWARD_USERNAME", "FORWARD_PASSWORD", "FORWARD_INSECURE"} {
		t.Setenv(k, "")
	}
	t.Setenv("FORWARD_USERNAME", "envuser")
	if code, _, errb := call(t, []string{"--config", cfg, "--url", "https://flag.example", "version"}, "", nil); code != 0 {
		t.Fatalf("%d %s", code, errb)
	}
	if os.Getenv("FORWARD_URL") != "https://flag.example" || os.Getenv("FORWARD_USERNAME") != "envuser" || os.Getenv("FORWARD_PASSWORD") != "secret" {
		t.Errorf("flag > env > file: %s %s %s", os.Getenv("FORWARD_URL"), os.Getenv("FORWARD_USERNAME"), os.Getenv("FORWARD_PASSWORD"))
	}
	// a password file other users can read is refused
	os.WriteFile(pw, []byte("secret"), 0o600)
	os.Chmod(pw, 0o644)
	t.Setenv("FORWARD_PASSWORD", "")
	if code, _, errb := call(t, []string{"--config", cfg, "version"}, "", nil); code != usage || !strings.Contains(errb, "chmod 600") {
		t.Errorf("want a refusal, got %d %s", code, errb)
	}
}

func TestCompletionScriptsNameTheSkills(t *testing.T) {
	for _, sh := range []string{"bash", "zsh", "fish", "powershell"} {
		code, out, errb := call(t, []string{"completion", sh}, "", nil)
		if code != 0 || !strings.Contains(out, "fwdctl") {
			t.Errorf("%s: %d %s", sh, code, errb)
		}
	}
	// the shell asks the binary for the words (cobra's dynamic completion): skill names after run and describe, commands first
	_, out, _ := call(t, []string{"__complete", "run", ""}, "", nil)
	if !strings.Contains(out, "inspect-networks") || !strings.Contains(out, "edit-org-property") {
		t.Errorf("run completes skill names: %q", out)
	}
	_, out, _ = call(t, []string{"__complete", ""}, "", nil)
	if !strings.Contains(out, "whoami") || !strings.Contains(out, "nqe") {
		t.Errorf("the first word completes commands: %q", out)
	}
	_, out, _ = call(t, []string{"__complete", "nqe", "run", "--format", ""}, "", nil)
	if !strings.Contains(out, "jsonl") {
		t.Errorf("--format completes its values: %q", out)
	}
}

func TestTokenFileFillsTheLoginAndIsRefusedWhenOthersCanReadIt(t *testing.T) {
	dir := t.TempDir()
	tf := filepath.Join(dir, "cust.token")
	os.WriteFile(tf, []byte("fwd.example.com\nme@example.com\ns3cret\n\n"), 0o600)
	for _, k := range []string{"FORWARD_URL", "FORWARD_USERNAME", "FORWARD_PASSWORD"} {
		t.Setenv(k, "")
	}
	cfg := filepath.Join(dir, "c.json")
	os.WriteFile(cfg, []byte(`{"token_file":"`+tf+`"}`), 0o600)
	if code, _, errb := call(t, []string{"--config", cfg, "version"}, "", nil); code != 0 {
		t.Fatalf("%d %s", code, errb)
	}
	if os.Getenv("FORWARD_URL") != "https://fwd.example.com" || os.Getenv("FORWARD_USERNAME") != "me@example.com" || os.Getenv("FORWARD_PASSWORD") != "s3cret" {
		t.Errorf("token file: %s %s %s", os.Getenv("FORWARD_URL"), os.Getenv("FORWARD_USERNAME"), os.Getenv("FORWARD_PASSWORD"))
	}
	// the environment still wins
	t.Setenv("FORWARD_URL", "https://other.example")
	t.Setenv("FORWARD_USERNAME", "")
	t.Setenv("FORWARD_PASSWORD", "")
	call(t, []string{"--config", cfg, "version"}, "", nil)
	if os.Getenv("FORWARD_URL") != "https://other.example" {
		t.Errorf("FORWARD_URL from the environment must win: %s", os.Getenv("FORWARD_URL"))
	}
	os.Chmod(tf, 0o644)
	t.Setenv("FORWARD_URL", "")
	if code, _, errb := call(t, []string{"--config", cfg, "version"}, "", nil); code != usage || !strings.Contains(errb, "chmod 600") {
		t.Errorf("a world-readable token file must be refused: %d %s", code, errb)
	}
	os.Chmod(tf, 0o600)
	os.WriteFile(tf, []byte("a\nb\n"), 0o600)
	if code, _, _ := call(t, []string{"--token-file", tf, "version"}, "", nil); code != usage {
		t.Error("a token file without three lines must be refused")
	}
}

func TestConfigURLAndTokenFileWorkTogetherAndTheOneLineFormIsAccepted(t *testing.T) {
	dir := t.TempDir()
	tf := filepath.Join(dir, "one.token")
	os.WriteFile(tf, []byte("fwd.example.com key123 s3cret\n"), 0o600)
	for _, k := range []string{"FORWARD_URL", "FORWARD_USERNAME", "FORWARD_PASSWORD"} {
		t.Setenv(k, "")
	}
	cfg := filepath.Join(dir, "c.json")
	// a config with both url and token_file: the config's url wins, the token file still supplies the login
	os.WriteFile(cfg, []byte(`{"url":"https://override.example","token_file":"`+tf+`"}`), 0o600)
	if code, _, errb := call(t, []string{"--config", cfg, "version"}, "", nil); code != 0 {
		t.Fatalf("%d %s", code, errb)
	}
	if os.Getenv("FORWARD_URL") != "https://override.example" || os.Getenv("FORWARD_USERNAME") != "key123" || os.Getenv("FORWARD_PASSWORD") != "s3cret" {
		t.Errorf("got %s %s %s", os.Getenv("FORWARD_URL"), os.Getenv("FORWARD_USERNAME"), os.Getenv("FORWARD_PASSWORD"))
	}
}
