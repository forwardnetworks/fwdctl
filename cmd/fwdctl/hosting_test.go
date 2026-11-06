package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/fwdtest"
)

// hostedCall runs fwdctl with the session built the way main builds it (the FWDCTL_ guards read from the environment), against the fake.
func hostedCall(t *testing.T, args []string, stdin string, r map[string]fwdtest.Handler) (int, string, string, *fwdtest.Server) {
	t.Helper()
	var out, errb bytes.Buffer
	var srv *fwdtest.Server
	code := run(args, strings.NewReader(stdin), &out, &errb, func() (*fwd.Session, error) {
		s, sv := fwdtest.NewWith(t, r, guardFromEnv)
		srv = sv
		return s, nil
	})
	return code, out.String(), errb.String(), srv
}

func errorKind(t *testing.T, out string) (string, string) {
	t.Helper()
	var r struct {
		Status    string `json:"status"`
		ErrorKind string `json:"error_kind"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("stdout is not a result: %v\n%s", err, out)
	}
	return r.Status, r.ErrorKind
}

// FWDCTL_ALLOW_NETWORKS: a run for another network is refused before any request, with error_kind refused, and the network list holds only the allowed ones.
func TestAllowNetworksRefusesARunForAnotherNetwork(t *testing.T) {
	t.Setenv("FWDCTL_ALLOW_NETWORKS", "n1")
	code, out, _, srv := hostedCall(t, []string{"run", "investigate-reachability"}, `{"network_id":"n2","src_ip":"192.0.2.1","dst_ip":"192.0.2.2"}`, routes("DELIVERED"))
	if code != usage {
		t.Fatalf("exit %d, want %d", code, usage)
	}
	if st, kind := errorKind(t, out); st != "error" || kind != "refused" {
		t.Fatalf("status %s kind %q, want error/refused: %s", st, kind, out)
	}
	if srv != nil && len(srv.Calls()) != 0 {
		t.Fatalf("requests were sent: %+v", srv.Calls())
	}
	ok := routes("DELIVERED")
	ok["GET /api/snapshots/s1/progress"] = fwdtest.Const(200, map[string]any{"networkId": "n1", "snapshotId": "s1"})
	code, out, _, _ = hostedCall(t, []string{"run", "investigate-reachability"}, in, ok)
	if code != 0 {
		t.Fatalf("the allowed network runs: exit %d %s", code, out)
	}
	r := map[string]fwdtest.Handler{"GET /api/networks": fwdtest.Const(200, []map[string]any{{"id": "n1", "name": "a"}, {"id": "n2", "name": "b"}})}
	code, out, _, _ = hostedCall(t, []string{"run", "inspect-networks"}, `{}`, r)
	if code != 0 || strings.Contains(out, `"n2"`) || !strings.Contains(out, `"n1"`) {
		t.Fatalf("the network list must hold only n1: exit %d %s", code, out)
	}
}

// FWDCTL_READONLY: an edit- name with apply is refused before anything is sent; its dry run still answers.
func TestReadOnlyRefusesApplyButAllowsADryRun(t *testing.T) {
	t.Setenv("FWDCTL_READONLY", "1")
	r := map[string]fwdtest.Handler{
		"GET /api/networks/n1/snapshots": fwdtest.Snapshots(fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")),
	}
	code, out, _, srv := hostedCall(t, []string{"run", "edit-snapshot"}, `{"network_id":"n1","action":"note","snapshot_id":"s1","note":"x","apply":true}`, r)
	if code != usage {
		t.Fatalf("exit %d, want %d: %s", code, usage, out)
	}
	if st, kind := errorKind(t, out); st != "error" || kind != "refused" {
		t.Fatalf("status %s kind %q: %s", st, kind, out)
	}
	if srv != nil && len(srv.Calls()) != 0 {
		t.Fatalf("requests were sent: %+v", srv.Calls())
	}
	code, out, _, _ = hostedCall(t, []string{"run", "edit-snapshot"}, `{"network_id":"n1","action":"note","snapshot_id":"s1","note":"x"}`, r)
	if code != 0 || !strings.Contains(out, `"dry_run"`) {
		t.Fatalf("a dry run still answers: exit %d %s", code, out)
	}
}

// A run that could not complete says why in error_kind: a wrong login is auth.
func TestErrorResultCarriesTheKind(t *testing.T) {
	r := map[string]fwdtest.Handler{"GET /api/networks/n1/snapshots": fwdtest.Const(401, map[string]any{"message": "Bad credentials"})}
	code, out, _ := call(t, []string{"run", "investigate-reachability"}, in, r)
	if code != 3 {
		t.Fatalf("exit %d, want 3: %s", code, out)
	}
	if st, kind := errorKind(t, out); st != "error" || kind != "auth" {
		t.Fatalf("status %s kind %q, want error/auth: %s", st, kind, out)
	}
	code, out, _ = call(t, []string{"run", "investigate-reachability"}, `{"network_id":"n1"}`, r)
	if st, kind := errorKind(t, out); code != usage || st != "error" || kind != "invalid_input" {
		t.Fatalf("missing input: exit %d status %s kind %q", code, st, kind)
	}
}

// FWDCTL_QUIET: a run prints nothing on stderr (no "against URL as USER" line, no dry-run note); stdout is the result as before.
func TestQuietRunPrintsNothingOnStderr(t *testing.T) {
	t.Setenv("FORWARD_URL", "https://forward.example")
	t.Setenv("FORWARD_USERNAME", "tester")
	t.Setenv("FWDCTL_QUIET", "1")
	r := map[string]fwdtest.Handler{
		"GET /api/networks/n1/snapshots": fwdtest.Snapshots(fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")),
	}
	code, out, errs := call(t, []string{"run", "edit-snapshot"}, `{"network_id":"n1","action":"note","snapshot_id":"s1","note":"x"}`, r)
	if code != 0 || errs != "" || !strings.Contains(out, `"dry_run"`) {
		t.Fatalf("exit %d stderr %q", code, errs)
	}
	code, _, errs = call(t, []string{"run", "investigate-reachability"}, `{"network_id":"n1"}`, r)
	if code != usage || !strings.Contains(errs, "error:") {
		t.Fatalf("errors still go to stderr: exit %d %q", code, errs)
	}
}

// FWDCTL_NO_STATE: the saved login is not read, so credentials come only from the environment and the flags.
func TestNoStateIgnoresTheSavedLogin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	p := defaultConfigPath()
	if p == "" {
		t.Skip("no user config directory")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"url":"https://saved.example.invalid","username":"saved"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FORWARD_URL", "")
	t.Setenv("FORWARD_USERNAME", "")
	t.Setenv("FWDCTL_NO_STATE", "1")
	if err := applyConnectionOptions(connOptions{}); err != nil {
		t.Fatal(err)
	}
	if u := strings.TrimSpace(os.Getenv("FORWARD_URL")); u != "" {
		t.Fatalf("FORWARD_URL = %q from the saved login; FWDCTL_NO_STATE must not read it", u)
	}
	t.Setenv("FWDCTL_NO_STATE", "")
	if err := applyConnectionOptions(connOptions{}); err != nil {
		t.Fatal(err)
	}
	if u := os.Getenv("FORWARD_URL"); u != "https://saved.example.invalid" {
		t.Fatalf("without FWDCTL_NO_STATE the saved login is read: FORWARD_URL = %q", u)
	}
}

// FWDCTL_FORCE_REDACT: no_redact:true (device files) and include_content:true (a data file) are refused before anything is read; the same
// read without them answers, redacted.
func TestForceRedactRefusesTurningRedactionOff(t *testing.T) {
	t.Setenv("FWDCTL_FORCE_REDACT", "1")
	r := map[string]fwdtest.Handler{
		"GET /api/networks/n1/snapshots":                   fwdtest.Snapshots(fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")),
		"GET /api/networks/n1/devices/r1/files/config.txt": fwdtest.Const(200, []byte("tacacs-server key 7 SEC1\n")),
	}
	for _, in := range []string{
		`{"network_id":"n1","device":"r1","mode":"read","file":"config.txt","no_redact":true}`,
		`{"network_id":"n1","view":"config","data_file":"f.csv","include_content":true}`,
		`{"network_id":"n1","device":"r1","no_redact":true}`,
	} {
		name := "inspect-device-files"
		if strings.Contains(in, "include_content") {
			name = "inspect-collection"
		} else if !strings.Contains(in, "mode") {
			name = "compare-device-config"
		}
		code, out, _, srv := hostedCall(t, []string{"run", name}, in, r)
		if code != usage {
			t.Fatalf("%s: exit %d, want %d: %s", name, code, usage, out)
		}
		if st, kind := errorKind(t, out); st != "error" || kind != "refused" || strings.Contains(out, "SEC1") {
			t.Fatalf("%s: status %s kind %q: %s", name, st, kind, out)
		}
		if srv != nil && len(srv.Calls()) != 0 {
			t.Fatalf("%s: requests were sent: %+v", name, srv.Calls())
		}
	}
	code, out, _, _ := hostedCall(t, []string{"run", "inspect-device-files"}, `{"network_id":"n1","device":"r1","mode":"read","file":"config.txt"}`, r)
	if code != 0 || strings.Contains(out, "SEC1") || !strings.Contains(out, `\u003credacted\u003e`) || !strings.Contains(out, "this session keeps redaction on") {
		t.Fatalf("the redacted read still answers: exit %d %s", code, out)
	}
}
