package main

import (
	"encoding/json"
	"errors"
	"fmt"
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

// applyConnectionOptions puts the connection (flags, then the saved config and token file for whatever they leave empty) into the environment the session reads.
func applyConnectionOptions(o connOptions) error {
	url, user, passFile, cfg, tokenFile, insecure := o.url, o.username, o.passwordFile, o.configFile, o.tokenFile, o.insecure
	file := cfg
	explicit := cfg != ""
	if file == "" {
		file = defaultConfigPath()
	}
	var c connFile
	if file != "" {
		if b, err := os.ReadFile(file); err == nil {
			if err := json.Unmarshal(b, &c); err != nil {
				return fmt.Errorf("%s is not valid JSON: %v", file, err)
			}
		} else if explicit {
			return fmt.Errorf("cannot read %s: %v", file, err)
		}
	}
	if tokenFile == "" && c.TokenFile != "" && c.URL == "" && c.Username == "" && c.PasswordFile == "" {
		tokenFile = c.TokenFile
	}
	if tokenFile != "" { // a token file fills what flags and the environment leave empty
		tu, tn, tp, err := readTokenFile(tokenFile)
		if err != nil {
			return err
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
			return err
		}
		os.Setenv("FORWARD_PASSWORD", pw)
	}
	return nil
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
