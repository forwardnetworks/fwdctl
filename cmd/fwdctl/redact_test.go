package main

// DOGFOOD-TEMP: tests for redact-check and dogfood-note.

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func init() { machineIdentity = func() []string { return nil } } // tests must not depend on who runs them

func kindsOf(t *testing.T, text string, deny ...string) []string {
	t.Helper()
	var ks []string
	for _, f := range newRedactor(deny).check(text) {
		ks = append(ks, f.Kind)
	}
	return ks
}

func TestRedactDetectors(t *testing.T) {
	cases := []struct {
		name, text, kind string // kind "" means the text must be clean
	}{
		{"doc ipv4", "peer 192.0.2.7 and 198.51.100.0/24 and 203.0.113.9", ""},
		{"doc ipv6", "prefix 2001:db8::1 and 2001:db8:abcd::/48", ""},
		{"private ipv4", "the device at 10.1.2.3 answered", "ip"},
		{"cgnat ipv4", "100.64.1.1", "ip"},
		{"loopback", "127.0.0.1", "ip"},
		{"prefix", "route 172.16.0.0/12", "ip"},
		{"public ipv6", "2606:4700:4700::1111", "ip"},
		{"link-local v6", "fe80::1", "ip"},
		{"not an ip", "version 1.0.0.300 and time 12:30:45", ""},
		{"fqdn", "host nycdc1-core-01.net.example.corp", "hostname"},
		{"lan", "printer.lan", "hostname"},
		{"internal 3 labels", "x.y.internal", "hostname"},
		{"allowed hosts", "see github.com and docs.forwardnetworks.com, example.org, claude.ai, raw.githubusercontent.com", ""},
		{"file names", "edit docs/internal.md, main.go, config.json and SKILL.md and go.mod", ""},
		{"versions", "fwdctl v0.5.21 and 1.0.0-261001 and 26.9.0", ""},
		{"eg", "e.g. this, i.e. that", ""},
		{"device", "on abc-rtr-01 the link", "device-name"},
		{"device2", "nycdc1-core-01", "device-name"},
		{"skill names", "inspect-edge, plan-report-skill-gap, compare-link-overrides, investigate-collection-failure", ""},
		{"placeholders", "`<device>` at <ip> in <network-id> snapshot <snapshot-id> for <customer> by <user> on <forward-url>", ""},
		{"placeholder not exempt for real", "`abc-rtr-01` in a code span", "device-name"},
		{"date-ish", "release-2026-10-01 notes", ""},
		{"plain prose", "The router picked the wrong skill and the result said unknown with no reason.", ""},
		{"url", "see https://forward.acme.example.net/app/ for it", "url"},
		{"own repo url", "see https://github.com/forwardnetworks/fwdctl/issues/12", ""},
		{"other github", "https://github.com/someone/else", "url"},
		{"email", "ask jo.smith@acme-corp.com", "email"},
		{"bearer", "Authorization: Bearer abcdef0123456789xyz", "secret"},
		{"basic", "Authorization: Basic dXNlcjpwYXNzd29yZA==", "secret"},
		{"password kw", "password=hunter2hunter2", "secret"},
		{"token json", `{"token": "s3cr3tvalue"}`, "secret"},
		{"api key", "api_key: Zk9xLmM2", "secret"},
		{"keyword placeholder", "token: <token> and password: <password>", ""},
		{"pem", "-----BEGIN RSA PRIVATE KEY-----", "secret"},
		{"aws", "AKIAABCDEFGHIJKLMNOP", "secret"},
		{"long hex", "deadbeefdeadbeefdeadbeefdeadbeef", "secret"},
		{"long b64", "aGVsbG8gd29ybGQgdGhpcyBpcyBhIHRlc3Q9", "secret"},
		{"long lowercase word", "internationalizationandlocalizationfoo", ""},
		{"network id", "network 1234", "numeric-id"},
		{"snapshot_id", "snapshot_id: 1001", "numeric-id"},
		{"json id", `"network_id":"1234"`, "numeric-id"},
		{"org id", "org #1234", "numeric-id"},
		{"task", "task 123456 failed", "numeric-id"},
		{"no id", "network 3 or task one, network 1.0", ""},
		{"home", "file /home/jsmith/notes.txt", "home-path"},
		{"mac path", "/Users/jane/Library", "home-path"},
		{"windows", `C:\Users\jane\x`, "home-path"},
		{"generic home", "~/file and /home/<user>/x", ""},
		{"mac", "aa:bb:cc:dd:ee:ff", "mac"},
		{"note ref", "private reproduction details saved locally by the reporter, ref link-overrides-409", ""},
	}
	for _, c := range cases {
		got := kindsOf(t, c.text)
		if c.kind == "" && len(got) != 0 {
			t.Errorf("%s: %q should be clean, got %v", c.name, c.text, got)
		}
		if c.kind != "" {
			ok := false
			for _, k := range got {
				ok = ok || k == c.kind
			}
			if !ok {
				t.Errorf("%s: %q should be flagged %s, got %v", c.name, c.text, c.kind, got)
			}
		}
	}
}

func TestRedactDenyList(t *testing.T) {
	if got := kindsOf(t, "the Acme Corp network", "acme"); len(got) != 1 || got[0] != "deny-list" {
		t.Errorf("deny word: %v", got)
	}
	if got := kindsOf(t, "acmetools and academy", "acme"); len(got) != 0 {
		t.Errorf("word boundary: %v", got)
	}
	if got := kindsOf(t, "ACME", "acme"); len(got) != 1 {
		t.Errorf("case: %v", got)
	}
}

const secretLine = "S3cr3t-Pa55word-NEVER-READ"

func writeLogin(t *testing.T) (cfg string) {
	t.Helper()
	d := t.TempDir()
	tok := filepath.Join(d, "x.token")
	if err := os.WriteFile(tok, []byte("https://globex.fwd.example.net/\nsvc-globexuser\n"+secretLine+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg = filepath.Join(d, "config.json")
	b, _ := json.Marshal(connFile{TokenFile: tok})
	if err := os.WriteFile(cfg, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestRedactCmdAutomaticDenyFromLogin(t *testing.T) {
	t.Setenv("FORWARD_URL", "")
	t.Setenv("FORWARD_USERNAME", "")
	t.Setenv("FWDCTL_REDACT_DENY", "zzcust")
	cfg := writeLogin(t)
	for _, text := range []string{"the globex lab", "user svc-globexuser did it", "fwd.example.net? no: globex.fwd.example.net", "ZZCUST said"} {
		var out, errb bytes.Buffer
		code := redactCheckCmd([]string{"-"}, strings.NewReader(text), &out, &errb, cfg)
		if code != 1 || !strings.Contains(out.String(), "deny-list") {
			t.Errorf("%q: code %d out %s", text, code, out.String())
		}
		for _, leak := range []string{"globex", "svc-globexuser", "zzcust", secretLine} {
			if strings.Contains(strings.ToLower(out.String()), strings.ToLower(leak)) {
				t.Errorf("%q: output leaks %q: %s", text, leak, out.String())
			}
		}
	}
	var out bytes.Buffer
	if code := redactCheckCmd([]string{"-"}, strings.NewReader("a clean sentence about <customer>"), &out, io.Discard, cfg); code != 0 {
		t.Errorf("clean text: %d %s", code, out.String())
	}
	// --deny
	out.Reset()
	if code := redactCheckCmd([]string{"--deny", "foo,bar", "-"}, strings.NewReader("a Bar here"), &out, io.Discard, ""); code != 1 {
		t.Errorf("--deny: %d", code)
	}
}

// recordingReader remembers how far the consumer read.
type recordingReader struct {
	data []byte
	pos  int
}

func (r *recordingReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:r.pos+1]) // one byte at a time even if asked for more
	r.pos += n
	return n, nil
}

func TestTokenIdentityNeverReadsThirdLine(t *testing.T) {
	data := "https://x.example.net\nuser1\n" + secretLine + "\n"
	rr := &recordingReader{data: []byte(data)}
	u, n := readTokenIdentity(rr)
	if u != "https://x.example.net" || n != "user1" {
		t.Fatalf("got %q %q", u, n)
	}
	if rr.pos > strings.Index(data, secretLine) {
		t.Errorf("read %d bytes, the third line starts at %d", rr.pos, strings.Index(data, secretLine))
	}
	words := strings.Join(identityWords(u, n), " ")
	if strings.Contains(words, secretLine) {
		t.Error("the secret reached the deny words")
	}
	// and through the whole command: the secret is not a deny word either (it would be flagged if it were)
	cfg := writeLogin(t)
	w, _ := savedLoginDeny(cfg)
	for _, x := range w {
		if strings.Contains(x, strings.ToLower(secretLine)) {
			t.Error("secret in deny list")
		}
	}
	if got := kindsOf(t, "mentions "+secretLine, w...); len(got) == 0 {
		// flagged by its own shape (long mixed string) is fine; what matters is it is not a deny-list word
		t.Log("secret-shaped text not flagged by shape; ok")
	} else if got[0] == "deny-list" {
		t.Error("third line was stored as a deny word")
	}
}

func TestRedactOutputNeverContainsOffendingText(t *testing.T) {
	offending := []string{"nycdc1-core-01.net.example.corp", "10.1.2.3", "1234", "Zk9xLmM2PqrsTuvw", "jo.smith@acme-corp.com", "abc-rtr-01", "/home/jsmith", "AKIAABCDEFGHIJKLMNOP", "deadbeefdeadbeefdeadbeefdeadbeef", "https://forward.acme.example.net/app", "hunter2hunter2", "dXNlcjpwYXNzd29yZA=="}
	draft := strings.Join([]string{
		"host nycdc1-core-01.net.example.corp at 10.1.2.3", "network 1234", "token=Zk9xLmM2PqrsTuvw", "ask jo.smith@acme-corp.com", "and abc-rtr-01", "in /home/jsmith",
		"AKIAABCDEFGHIJKLMNOP", "deadbeefdeadbeefdeadbeefdeadbeef", "https://forward.acme.example.net/app", "password: hunter2hunter2", "Basic dXNlcjpwYXNzd29yZA==",
	}, "\n")
	var out bytes.Buffer
	if code := redactCheckCmd([]string{"-"}, strings.NewReader(draft), &out, io.Discard, ""); code != 1 {
		t.Fatalf("code %d", code)
	}
	var rep redactReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.OK || len(rep.Findings) < 10 {
		t.Fatalf("findings: %+v", rep.Findings)
	}
	for _, f := range rep.Findings {
		if !strings.HasSuffix(f.Excerpt, "***") || len(f.Excerpt) > 5 || f.Line < 1 || f.Advice == "" {
			t.Errorf("bad finding %+v", f)
		}
	}
	for _, o := range offending {
		if strings.Contains(out.String(), o) {
			t.Errorf("output contains %q", o)
		}
		if len(o) > 5 && strings.Contains(out.String(), o[:6]) {
			t.Errorf("output contains the first 6 chars of %q", o)
		}
	}
}

func TestRedactUsage(t *testing.T) {
	for _, args := range [][]string{{}, {"--bogus"}, {"--file"}, {"--file", "a", "-"}} {
		if code := redactCheckCmd(args, strings.NewReader(""), io.Discard, io.Discard, ""); code != usage {
			t.Errorf("%v: %d", args, code)
		}
	}
	if code := redactCheckCmd([]string{"--file", "/nonexistent/x"}, nil, io.Discard, io.Discard, ""); code != usage {
		t.Error("missing file")
	}
	f := filepath.Join(t.TempDir(), "d.md")
	os.WriteFile(f, []byte("fine <device>\n"), 0o600)
	var out bytes.Buffer
	if code := redactCheckCmd([]string{"--file", f}, nil, &out, io.Discard, ""); code != 0 || !strings.Contains(out.String(), `"ok": true`) || !strings.Contains(out.String(), `"findings": []`) {
		t.Errorf("clean file: %d %s", code, out.String())
	}
}

func TestMaskExcerpt(t *testing.T) {
	for in, want := range map[string]string{"nycdc1-core-01": "ny***", "10.1.2.3": "10***", "1234": "1***", "abc": "***", "": "***"} {
		if got := maskExcerpt(in); got != want {
			t.Errorf("%q: %q want %q", in, got, want)
		}
	}
}

func TestDogfoodNote(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "share", "dogfood-private")
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	const body = "network 1234 at 10.1.2.3 nycdc1-core-01 token=abc123abc123\n"
	var paths []string
	for i := 0; i < 2; i++ {
		var out, errb bytes.Buffer
		if code := dogfoodNoteCmd([]string{"--ref", "link-overrides-409", "-"}, strings.NewReader(body), &out, &errb, dir, now); code != 0 {
			t.Fatalf("code %d %s", code, errb.String())
		}
		lines := strings.Split(strings.TrimSpace(out.String()), "\n")
		if len(lines) != 2 || strings.Contains(out.String(), "1234") || strings.Contains(out.String(), "10.1.2.3") || strings.Contains(out.String(), "txdadc") {
			t.Fatalf("output must be the path and one instruction line only: %q", out.String())
		}
		paths = append(paths, lines[0])
	}
	if paths[0] == paths[1] || filepath.Base(paths[0]) != "2026-10-01-link-overrides-409.md" || filepath.Base(paths[1]) != "2026-10-01-link-overrides-409-2.md" {
		t.Errorf("no-overwrite names: %v", paths)
	}
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v %v", p, err, st)
		}
		if b, _ := os.ReadFile(p); string(b) != body {
			t.Errorf("content not kept in full: %q", b)
		}
	}
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %o", st.Mode().Perm())
	}
	// ref validation and usage
	for _, args := range [][]string{{"-"}, {"--ref", "ab", "-"}, {"--ref", "Has_Upper", "-"}, {"--ref", "../../etc/x", "-"}, {"--ref", strings.Repeat("a", 41), "-"}, {"--ref", "ok-slug"}} {
		if code := dogfoodNoteCmd(args, strings.NewReader("x"), io.Discard, io.Discard, dir, now); code != usage {
			t.Errorf("%v: %d", args, code)
		}
	}
	// the public draft carrying the slug still passes redact-check
	draft := "Skill: edit-link-overrides\nprivate reproduction details saved locally by the reporter, ref link-overrides-409\n"
	var out bytes.Buffer
	if code := redactCheckCmd([]string{"-"}, strings.NewReader(draft), &out, io.Discard, ""); code != 0 {
		t.Errorf("draft with ref slug: %d %s", code, out.String())
	}
}

func warnKinds(text string) (hard, soft []string) {
	f, w := newRedactor(nil).checkAll(text)
	for _, x := range f {
		hard = append(hard, x.Kind)
	}
	for _, x := range w {
		soft = append(soft, x.Kind)
	}
	return
}

func TestRedactSecretsWithoutSeparator(t *testing.T) {
	flagged := []string{"password hunter2 and token abc123def456", "pwd is Hunter22", "api key Zk9xLmM2Pq", "apikey: abcdefghij", "the credential s3cretvalue here", "Authorization: Bearer abcdef123456", "Authorization: Basic dXNlcjpwYXNz", "https://bob:pw12345@host.example.net/x", "AbCdEfGhIjKlMnOp12345", "a1b2c3d4e5f6a7b8c9d0e1f2"}
	for _, x := range flagged {
		if h, _ := warnKinds(x); len(h) == 0 {
			t.Errorf("%q should be a hard finding", x)
		}
	}
	clean := []string{"token: none and no password", "password requirements are shown", "the token expired", "secret sauce", "a bearer token", "the credential store", "TestRedactDetectorsNames"}
	for _, x := range clean {
		if h, s := warnKinds(x); len(h)+len(s) != 0 {
			t.Errorf("%q should be clean, got %v %v", x, h, s)
		}
	}
}

func TestRedactSoftWarnings(t *testing.T) {
	warn := []string{"Customer Initech DIR Springfield hit this.", "customer acme corp network", "a bug for Acme", "seen at Contoso", "data from Globex Corp", "with Initech Systems Inc"}
	for _, x := range warn {
		h, s := warnKinds(x)
		if len(h) != 0 || len(s) != 1 || s[0] != "possible-customer-name" {
			t.Errorf("%q: hard %v soft %v", x, h, s)
		}
	}
	clean := []string{"with Forward Networks and for Example", "from GitHub at Kubernetes", "for Cisco and Juniper, with Python", "the customer is unhappy", "customer network", "for <customer> at <device>", "with Claude Code", "for the router, at the edge"}
	for _, x := range clean {
		if h, s := warnKinds(x); len(h)+len(s) != 0 {
			t.Errorf("%q should be clean, got %v %v", x, h, s)
		}
	}
}

func TestRedactExitCodesAndMasking(t *testing.T) {
	run := func(text string, args ...string) (int, string) {
		var out bytes.Buffer
		code := redactCheckCmd(append(args, "-"), strings.NewReader(text), &out, io.Discard, "")
		return code, out.String()
	}
	if code, out := run("all good <device>"); code != 0 || !strings.Contains(out, `"ok": true`) {
		t.Errorf("clean: %d", code)
	}
	code, out := run("customer acme corp network")
	if code != 2 || !strings.Contains(out, `"ok": false`) || !strings.Contains(out, `"findings": []`) {
		t.Errorf("warning only: %d %s", code, out)
	}
	if strings.Contains(strings.ToLower(out), "acme") || strings.Contains(strings.ToLower(out), "corp") {
		t.Errorf("warning leaks: %s", out)
	}
	if code, out = run("customer Initech DIR at 10.1.2.3"); code != 1 || strings.Contains(out, "Initech") || strings.Contains(out, "10.1.2.3") {
		t.Errorf("finding beats warning: %d %s", code, out)
	}
	if code, out = run("password hunter2 and token abc123def456"); code != 1 || strings.Contains(out, "hunter2") || strings.Contains(out, "abc123def456") {
		t.Errorf("secrets: %d %s", code, out)
	}
}

func TestRedactDenyNormalisationAndSources(t *testing.T) {
	for _, entry := range []string{"acme-corp", "acme corp", "AcmeCorp", "Acme_Corp"} {
		for _, text := range []string{"the acme-corp lab", "Acme Corp", "ACMECORP", "acme.corp"} {
			if got := kindsOf(t, text, entry); len(got) != 1 || got[0] != "deny-list" {
				t.Errorf("entry %q text %q: %v", entry, text, got)
			}
		}
	}
	if got := kindsOf(t, "acmecorpish", "acme-corp"); len(got) != 0 {
		t.Errorf("boundary: %v", got)
	}
	// repeated --deny flags, --deny=, and comma lists
	var out bytes.Buffer
	if code := redactCheckCmd([]string{"--deny", "aaa", "--deny=bbb,ccc", "-"}, strings.NewReader("a CCC and an aaa and bbb"), &out, io.Discard, ""); code != 1 || strings.Count(out.String(), "deny-list") != 3 {
		t.Errorf("repeated --deny: %d %s", code, out.String())
	}
	// config redact_deny
	cfg := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(cfg, []byte(`{"redact_deny": ["Initech Systems", "umbrella"]}`), 0o600)
	t.Setenv("FORWARD_URL", "")
	t.Setenv("FORWARD_USERNAME", "")
	t.Setenv("FWDCTL_REDACT_DENY", "")
	for _, text := range []string{"the initech-systems lab", "UMBRELLA"} {
		out.Reset()
		if code := redactCheckCmd([]string{"-"}, strings.NewReader(text), &out, io.Discard, cfg); code != 1 || !strings.Contains(out.String(), "deny-list") {
			t.Errorf("redact_deny %q: %d", text, code)
		}
	}
	// the OS user, host and home name
	machineIdentity = func() []string { return []string{"jdoe", "devbox"} }
	defer func() { machineIdentity = func() []string { return nil } }()
	for _, text := range []string{"user jdoe ran it", "on devbox"} {
		out.Reset()
		if code := redactCheckCmd([]string{"-"}, strings.NewReader(text), &out, io.Discard, ""); code != 1 || strings.Contains(out.String(), "jdoe") || strings.Contains(out.String(), "devbox") {
			t.Errorf("machine deny %q: %d %s", text, code, out.String())
		}
	}
}

func TestMachineIdentityDefaultSkipsGenericWords(t *testing.T) {
	// the real function: nothing generic and nothing shorter than 3 characters
	for _, w := range func() []string { return realMachineIdentity() }() {
		if len(w) < 3 || genericLogin[strings.ToLower(w)] {
			t.Errorf("bad machine word %q", w)
		}
	}
}
