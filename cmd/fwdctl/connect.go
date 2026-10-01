package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Where the Forward login comes from, in order of precedence: command-line flags in front of the command
// (--url, --username, --password-file, --insecure), then the environment (FORWARD_URL, FORWARD_USERNAME, FORWARD_PASSWORD,
// FORWARD_INSECURE), then an optional file (~/.config/fwdctl/config.json or --config FILE). The file holds the URL, the username and
// the PATH of a file with the password, never the password itself; a password file that others can read is refused.

type connFile struct {
	URL          string `json:"url"`
	Username     string `json:"username"`
	PasswordFile string `json:"password_file"`
	// TokenFile is a file of three lines: the Forward URL, the username (or an API token's access key) and the password (or its secret).
	TokenFile string `json:"token_file"`
	Insecure  bool   `json:"insecure"`
	// RedactDeny (DOGFOOD-TEMP) is extra words fwdctl redact-check must never let through; no other command reads it.
	RedactDeny []string `json:"redact_deny"`
}

// readTokenFile reads the three-line login file: URL, username, password. A URL without a scheme gets https://. The file must not be readable
// by other users.
func readTokenFile(path string) (url, user, pass string, err error) {
	if strings.HasPrefix(path, "~/") {
		if h, e := os.UserHomeDir(); e == nil {
			path = filepath.Join(h, path[2:])
		}
	}
	st, err := os.Stat(path)
	if err != nil {
		return "", "", "", fmt.Errorf("cannot read the token file: %v", err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		return "", "", "", fmt.Errorf("the token file %s can be read by other users (mode %o); run: chmod 600 %s", path, st.Mode().Perm(), path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", "", "", err
	}
	var lines []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimRight(l, "\r"); strings.TrimSpace(l) != "" {
			lines = append(lines, strings.TrimSpace(l))
		}
	}
	if len(lines) != 3 {
		return "", "", "", fmt.Errorf("the token file must have three lines (URL, username, password); %s has %d", path, len(lines))
	}
	url = lines[0]
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "https://" + url
	}
	return strings.TrimRight(url, "/"), lines[1], lines[2], nil
}

func defaultConfigPath() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "fwdctl", "config.json")
	}
	return ""
}

// applyConnection takes the connection flags off the front of args and puts the connection into the environment the session reads.
// It returns the remaining arguments.
func applyConnection(args []string, stderr io.Writer) ([]string, error) {
	var url, user, passFile, cfg, tokenFile string
	insecure := false
	for len(args) > 0 && strings.HasPrefix(args[0], "--") && args[0] != "--help" && args[0] != "--version" {
		name, val, hasVal := strings.Cut(args[0], "=")
		take := func() (string, error) {
			if hasVal {
				return val, nil
			}
			if len(args) < 2 {
				return "", fmt.Errorf("%s needs a value", name)
			}
			args = args[1:]
			return args[0], nil
		}
		var err error
		switch name {
		case "--url":
			url, err = take()
		case "--username":
			user, err = take()
		case "--password-file":
			passFile, err = take()
		case "--config":
			cfg, err = take()
		case "--token-file":
			tokenFile, err = take()
		case "--insecure":
			insecure = true
		default:
			return args, nil // not a connection flag: the command's own
		}
		if err != nil {
			return nil, err
		}
		args = args[1:]
	}
	file := cfg
	explicit := cfg != ""
	if file == "" {
		file = defaultConfigPath()
	}
	var c connFile
	if file != "" {
		if b, err := os.ReadFile(file); err == nil {
			if err := json.Unmarshal(b, &c); err != nil {
				return nil, fmt.Errorf("%s is not valid JSON: %v", file, err)
			}
		} else if explicit {
			return nil, fmt.Errorf("cannot read %s: %v", file, err)
		}
	}
	if tokenFile == "" && c.TokenFile != "" && c.URL == "" && c.Username == "" && c.PasswordFile == "" {
		tokenFile = c.TokenFile
	}
	if tokenFile != "" { // a token file fills what flags and the environment leave empty
		tu, tn, tp, err := readTokenFile(tokenFile)
		if err != nil {
			return nil, err
		}
		if url == "" && os.Getenv("FORWARD_URL") == "" {
			url = tu
		}
		if user == "" && os.Getenv("FORWARD_USERNAME") == "" {
			user = tn
		}
		if passFile == "" && os.Getenv("FORWARD_PASSWORD") == "" {
			os.Setenv("FORWARD_PASSWORD", tp)
		}
	}
	set := func(env, flagVal, fileVal string) {
		switch {
		case flagVal != "":
			os.Setenv(env, flagVal)
		case os.Getenv(env) == "" && fileVal != "":
			os.Setenv(env, fileVal)
		}
	}
	set("FORWARD_URL", url, c.URL)
	set("FORWARD_USERNAME", user, c.Username)
	if insecure || (c.Insecure && os.Getenv("FORWARD_INSECURE") == "") {
		os.Setenv("FORWARD_INSECURE", "true")
	}
	pf := passFile
	if pf == "" && os.Getenv("FORWARD_PASSWORD") == "" {
		pf = c.PasswordFile
	}
	if pf != "" && (passFile != "" || os.Getenv("FORWARD_PASSWORD") == "") {
		pw, err := readPasswordFile(pf)
		if err != nil {
			return nil, err
		}
		os.Setenv("FORWARD_PASSWORD", pw)
	}
	return args, nil
}

func readPasswordFile(path string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(h, path[2:])
		}
	}
	st, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("cannot read the password file: %v", err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("the password file %s can be read by other users (mode %o); run: chmod 600 %s", path, st.Mode().Perm(), path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	pw := strings.TrimRight(string(b), "\r\n")
	if pw == "" {
		return "", errors.New("the password file is empty")
	}
	return pw, nil
}
