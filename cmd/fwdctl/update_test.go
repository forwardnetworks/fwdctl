package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
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
	rest, err := applyConnection([]string{"--config", cfg, "--url", "https://flag.example", "run", "x"}, io.Discard)
	if err != nil || len(rest) != 2 || rest[0] != "run" {
		t.Fatalf("%v %v", rest, err)
	}
	if os.Getenv("FORWARD_URL") != "https://flag.example" || os.Getenv("FORWARD_USERNAME") != "envuser" || os.Getenv("FORWARD_PASSWORD") != "secret" {
		t.Errorf("flag > env > file: %s %s %s", os.Getenv("FORWARD_URL"), os.Getenv("FORWARD_USERNAME"), os.Getenv("FORWARD_PASSWORD"))
	}
	// a password file other users can read is refused
	os.WriteFile(pw, []byte("secret"), 0o600)
	os.Chmod(pw, 0o644)
	t.Setenv("FORWARD_PASSWORD", "")
	if _, err := applyConnection([]string{"--config", cfg, "list"}, io.Discard); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Errorf("want a refusal, got %v", err)
	}
}

func TestCompletionScriptsNameTheSkills(t *testing.T) {
	for _, sh := range []string{"bash", "zsh", "fish", "powershell"} {
		var out, errb bytes.Buffer
		if code := completionCmd([]string{sh}, &out, &errb); code != 0 || !strings.Contains(out.String(), "inspect-networks") || !strings.Contains(out.String(), "whoami") {
			t.Errorf("%s: %d %s", sh, code, errb.String())
		}
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
	if _, err := applyConnection([]string{"--config", cfg, "whoami"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("FORWARD_URL") != "https://fwd.example.com" || os.Getenv("FORWARD_USERNAME") != "me@example.com" || os.Getenv("FORWARD_PASSWORD") != "s3cret" {
		t.Errorf("token file: %s %s %s", os.Getenv("FORWARD_URL"), os.Getenv("FORWARD_USERNAME"), os.Getenv("FORWARD_PASSWORD"))
	}
	// the environment still wins
	t.Setenv("FORWARD_URL", "https://other.example")
	t.Setenv("FORWARD_USERNAME", "")
	t.Setenv("FORWARD_PASSWORD", "")
	applyConnection([]string{"--config", cfg, "whoami"}, io.Discard)
	if os.Getenv("FORWARD_URL") != "https://other.example" {
		t.Errorf("FORWARD_URL from the environment must win: %s", os.Getenv("FORWARD_URL"))
	}
	os.Chmod(tf, 0o644)
	t.Setenv("FORWARD_URL", "")
	if _, err := applyConnection([]string{"--config", cfg, "whoami"}, io.Discard); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Errorf("a world-readable token file must be refused: %v", err)
	}
	os.Chmod(tf, 0o600)
	os.WriteFile(tf, []byte("a\nb\n"), 0o600)
	if _, err := applyConnection([]string{"--token-file", tf, "whoami"}, io.Discard); err == nil {
		t.Error("a token file without three lines must be refused")
	}
}
