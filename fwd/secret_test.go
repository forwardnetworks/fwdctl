package fwd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretNeverPrintsOrMarshalsItsValue(t *testing.T) {
	s := Secret{v: "hunter2-value"}
	b, _ := json.Marshal(map[string]any{"s": s, "p": &s})
	out := fmt.Sprintf("%s %v %+v %#v %q", b, s, s, s, s)
	if strings.Contains(out, "hunter2") || !strings.Contains(out, "<secret>") || s.Reveal() != "hunter2-value" {
		t.Errorf("%s", out)
	}
}

func TestReadSecretRefusesALooseFileAndBothOrNeither(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "s")
	_ = os.WriteFile(f, []byte("abc\n"), 0o644)
	if _, err := ReadSecret(f, ""); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Errorf("a group-readable file is refused: %v", err)
	}
	_ = os.Chmod(f, 0o600)
	if s, err := ReadSecret(f, ""); err != nil || s.Reveal() != "abc" {
		t.Errorf("%v %q", err, s.Reveal())
	}
	t.Setenv("FWD_TEST_SECRET", "from-env")
	if s, err := ReadSecret("", "FWD_TEST_SECRET"); err != nil || s.Reveal() != "from-env" {
		t.Errorf("%v", err)
	}
	if _, err := ReadSecret(f, "FWD_TEST_SECRET"); err == nil {
		t.Errorf("both is refused")
	}
	if _, err := ReadSecret("", ""); err == nil {
		t.Errorf("neither is refused")
	}
}

func TestRedactSecretsHidesByFieldNameAtAnyDepthAndShortensLongBodies(t *testing.T) {
	v := map[string]any{"name": "cred1", "password": "p", "nested": []any{map[string]any{"privacyPassword": "x", "communityString": "public", "ok": 1}}, "cert": strings.Repeat("A", 400), "accessKey": "", "signedLicenseKey": "k"}
	n := RedactSecrets(v)
	b, _ := json.Marshal(v)
	s := string(b)
	for _, leak := range []string{`"p"`, `"x"`, `public`, `"k"`} {
		if strings.Contains(s, leak) {
			t.Errorf("%s leaked in %s", leak, s)
		}
	}
	if n != 4 || !strings.Contains(s, `"name":"cred1"`) || !strings.Contains(s, "400 characters") || !strings.Contains(s, `"accessKey":""`) {
		t.Errorf("n=%d %s", n, s)
	}
}
