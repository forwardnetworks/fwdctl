package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	// TokenFile is a netrc file: machine HOST, login USER (or an API token's access key), password SECRET (or its secret).
	TokenFile string `json:"token_file"`
	// SupportTokenFile is a second token file for a Forward-support login (FORWARD_SUPPORT_TOKEN_FILE, --support-token-file): only changes that need support authority use it.
	SupportTokenFile string `json:"support_token_file"`
	Insecure         bool   `json:"insecure"`
	// CABundle is the path of a PEM file of certificate authorities to trust for Forward's certificate.
	CABundle string `json:"ca_bundle"`
}

// readTokenFile reads the netrc login file: machine HOST (or a full URL), login USER, password SECRET. A host without a scheme gets https://. This is the only format. The file must not be readable
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
	return parseNetrc(path, string(b))
}

// parseNetrc reads a netrc token file: one machine entry with login and password, tokens separated by whitespace or newlines. The machine is the Forward host (or a full URL); a bare host gets https://.
func parseNetrc(path, text string) (string, string, string, error) {
	toks := strings.Fields(text)
	var machines []string
	var login, password string
	for i := 0; i+1 < len(toks); i += 2 {
		switch toks[i] {
		case "machine":
			machines = append(machines, toks[i+1])
		case "login":
			login = toks[i+1]
		case "password":
			password = toks[i+1]
		}
	}
	if len(machines) != 1 || login == "" || password == "" {
		return "", "", "", fmt.Errorf("the token file %s is not in a format fwdctl reads: expected netrc: machine HOST, login USER, password SECRET (one machine, on one line or several)", path)
	}
	url := machines[0]
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "https://" + url
	}
	return strings.TrimRight(url, "/"), login, password, nil
}

var profileName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// profilePath is where a named profile lives: the same file format as the default config, in a profiles folder next to it.
func profilePath(name string) (string, error) {
	if !profileName.MatchString(name) {
		return "", fmt.Errorf("a profile name is lower-case letters, digits, - and _ (at most 32), not %q", name)
	}
	base := defaultConfigPath()
	if base == "" {
		return "", errors.New("no user config directory on this machine")
	}
	return filepath.Join(filepath.Dir(base), "profiles", name+".json"), nil
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
	if o.profile != "" {
		if cfg != "" {
			return errors.New("give --profile or --config, not both")
		}
		pp, err := profilePath(o.profile)
		if err != nil {
			return err
		}
		if _, serr := os.Stat(pp); serr != nil && !o.profileMayBeNew {
			return fmt.Errorf("no profile %q: save one with `fwdctl login --profile %s --file TOKENFILE`", o.profile, o.profile)
		}
		cfg = pp
	}
	file := cfg
	explicit := cfg != "" && !o.profileMayBeNew
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
	// A token_file in the config is used with whatever else the config sets: the config's url or username (and flags and the
	// environment) win over the matching line of the token file; a config password_file wins over the token file's password.
	if tokenFile == "" && c.TokenFile != "" {
		tokenFile = c.TokenFile
	}
	if tokenFile != "" { // a token file fills what flags and the environment leave empty
		tu, tn, tp, err := readTokenFile(tokenFile)
		if err != nil {
			return err
		}
		if url == "" && os.Getenv("FORWARD_URL") == "" && c.URL == "" {
			url = tu
		}
		if user == "" && os.Getenv("FORWARD_USERNAME") == "" && c.Username == "" {
			user = tn
		}
		if passFile == "" && c.PasswordFile == "" && os.Getenv("FORWARD_PASSWORD") == "" {
			os.Setenv("FORWARD_PASSWORD", tp)
		}
	}
	sup := o.supportTokenFile
	if sup == "" {
		sup = os.Getenv("FORWARD_SUPPORT_TOKEN_FILE")
	}
	if sup == "" {
		sup = c.SupportTokenFile
	}
	if sup != "" {
		su, sn, sp, err := readTokenFile(sup)
		if err != nil {
			return fmt.Errorf("support login: %v", err)
		}
		os.Setenv("FORWARD_SUPPORT_URL", su)
		os.Setenv("FORWARD_SUPPORT_USERNAME", sn)
		os.Setenv("FORWARD_SUPPORT_PASSWORD", sp)
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
	set("FORWARD_CA_BUNDLE", expandHome(o.caBundle), expandHome(c.CABundle))
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

// expandHome turns a leading ~/ into the user's home directory, as the password and token file paths are.
func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, path[2:])
		}
	}
	return path
}
