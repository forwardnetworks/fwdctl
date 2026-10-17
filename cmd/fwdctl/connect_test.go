package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The CA bundle path comes from the flag, then FORWARD_CA_BUNDLE, then the config file's ca_bundle, like the other connection settings: a flag wins over the
// environment, and the environment over the file.
func TestTheCABundleComesFromTheFlagThenTheEnvironmentThenTheConfigFile(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfg, []byte(`{"url":"https://f","username":"u","ca_bundle":"/from/file.pem"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FORWARD_URL", "")
	t.Setenv("FORWARD_USERNAME", "")
	t.Setenv("FORWARD_CA_BUNDLE", "")
	if err := applyConnectionOptions(connOptions{configFile: cfg}); err != nil || os.Getenv("FORWARD_CA_BUNDLE") != "/from/file.pem" {
		t.Fatalf("file: %v %q", err, os.Getenv("FORWARD_CA_BUNDLE"))
	}
	t.Setenv("FORWARD_CA_BUNDLE", "/from/env.pem")
	if err := applyConnectionOptions(connOptions{configFile: cfg}); err != nil || os.Getenv("FORWARD_CA_BUNDLE") != "/from/env.pem" {
		t.Fatalf("env over file: %v %q", err, os.Getenv("FORWARD_CA_BUNDLE"))
	}
	if err := applyConnectionOptions(connOptions{configFile: cfg, caBundle: "/from/flag.pem"}); err != nil || os.Getenv("FORWARD_CA_BUNDLE") != "/from/flag.pem" {
		t.Fatalf("flag over env: %v %q", err, os.Getenv("FORWARD_CA_BUNDLE"))
	}
}

func TestTheCABundlePathExpandsALeadingTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("FORWARD_CA_BUNDLE", "")
	if err := applyConnectionOptions(connOptions{caBundle: "~/certs/ca.pem"}); err != nil || os.Getenv("FORWARD_CA_BUNDLE") != filepath.Join(home, "certs/ca.pem") {
		t.Fatalf("%v %q", err, os.Getenv("FORWARD_CA_BUNDLE"))
	}
}

// A second token file is the Forward-support login: --support-token-file (or FORWARD_SUPPORT_TOKEN_FILE, or support_token_file in the config) fills FORWARD_SUPPORT_*, and leaves the main login alone.
func TestTheSupportTokenFileFillsTheSupportLoginOnly(t *testing.T) {
	dir := t.TempDir()
	tok := filepath.Join(dir, "support.token")
	if err := os.WriteFile(tok, []byte("https://support.example\nsupport-user\nsupport-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"FORWARD_URL", "FORWARD_USERNAME", "FORWARD_PASSWORD", "FORWARD_SUPPORT_URL", "FORWARD_SUPPORT_USERNAME", "FORWARD_SUPPORT_PASSWORD", "FORWARD_SUPPORT_TOKEN_FILE"} {
		t.Setenv(k, "")
	}
	t.Setenv("FORWARD_URL", "https://main.example")
	cfg := filepath.Join(dir, "none.json")
	if err := os.WriteFile(cfg, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := applyConnectionOptions(connOptions{configFile: cfg, supportTokenFile: tok}); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("FORWARD_SUPPORT_USERNAME") != "support-user" || os.Getenv("FORWARD_SUPPORT_PASSWORD") != "support-secret" || os.Getenv("FORWARD_SUPPORT_URL") != "https://support.example" {
		t.Errorf("support login not set: %q", os.Getenv("FORWARD_SUPPORT_USERNAME"))
	}
	if os.Getenv("FORWARD_USERNAME") != "" || os.Getenv("FORWARD_URL") != "https://main.example" {
		t.Errorf("the main login must be untouched: %q %q", os.Getenv("FORWARD_USERNAME"), os.Getenv("FORWARD_URL"))
	}
}
