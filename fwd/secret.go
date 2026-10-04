package fwd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Secret is a value read from a file or the environment for a write that needs one (a password, a key, a token). It prints, logs and marshals as "<secret>" everywhere; only
// Reveal returns the text, and only the call that sends it to Forward should use that.
type Secret struct{ v string }

const secretMask = "<secret>"

func (Secret) String() string               { return secretMask }
func (Secret) GoString() string             { return secretMask }
func (Secret) MarshalJSON() ([]byte, error) { return json.Marshal(secretMask) }
func (Secret) MarshalText() ([]byte, error) { return []byte(secretMask), nil }

// SecretFromString wraps a value already in memory (one field of a JSON secret file).
func SecretFromString(v string) Secret { return Secret{v: v} }

func (s Secret) Reveal() string            { return s.v }
func (s Secret) Empty() bool               { return s.v == "" }
func (s Secret) Format(f fmtState, _ rune) { _, _ = f.Write([]byte(secretMask)) }

// fmtState is the part of fmt.State that Format needs.
type fmtState interface{ Write([]byte) (int, error) }

// ReadSecret returns the secret named by a file path (which must not be readable by group or others) or an environment variable. Exactly one of file and env is given. The secret
// is never part of the skill's input JSON, so it does not land in shell history, agent transcripts or the operations log.
func ReadSecret(file, env string) (Secret, error) {
	switch {
	case file != "" && env != "":
		return Secret{}, errors.New("give secret_file or secret_env, not both")
	case file == "" && env == "":
		return Secret{}, errors.New("this needs a secret: give secret_file (a path, mode 600) or secret_env (an environment variable name)")
	case env != "":
		v, ok := os.LookupEnv(env)
		if !ok || v == "" {
			return Secret{}, fmt.Errorf("the environment variable %s is not set", env)
		}
		return Secret{v: v}, nil
	}
	st, err := os.Stat(file)
	if err != nil {
		return Secret{}, fmt.Errorf("secret_file: %w", err)
	}
	if st.IsDir() {
		return Secret{}, fmt.Errorf("secret_file %s is a directory", file)
	}
	if st.Mode().Perm()&0o077 != 0 {
		return Secret{}, fmt.Errorf("secret_file %s is readable by other users (mode %o): chmod 600 it", file, st.Mode().Perm())
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return Secret{}, fmt.Errorf("secret_file: %w", err)
	}
	v := strings.TrimRight(string(b), "\r\n")
	if v == "" {
		return Secret{}, fmt.Errorf("secret_file %s is empty", file)
	}
	return Secret{v: v}, nil
}

var secretKeyRe = regexp.MustCompile(`(?i)(pass(word|wd|phrase)?|secret|privatekey|sshkey|apikey|accesskey|community|authenticationkey|privacykey|signedlicensekey|licensekey|^token$|bearer|authorization|credentialvalue|signingkey|clientsecret)`)

// SecretKey says whether a JSON field name holds a secret, by name. It errs on the side of hiding.
func SecretKey(name string) bool { return secretKeyRe.MatchString(name) }

// RedactSecrets replaces, in place, the value of every field whose name says it is a secret with "<redacted>", and shortens very long strings (a certificate body, a key block);
// it returns how many values it replaced. Reads use it on anything an SDK type marshals, so a secret an SDK struct carries is never returned by a skill.
func RedactSecrets(v any) int {
	n := 0
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if SecretKey(k) {
				if s, ok := val.(string); ok && s == "" {
					continue // nothing is held there
				}
				if val != nil {
					x[k] = "<redacted>"
					n++
				}
				continue
			}
			if s, ok := val.(string); ok && len(s) > 300 {
				x[k] = s[:120] + "... (" + fmt.Sprint(len(s)) + " characters)"
				continue
			}
			n += RedactSecrets(val)
		}
	case []any:
		for _, e := range x {
			n += RedactSecrets(e)
		}
	case []map[string]any:
		for _, e := range x {
			n += RedactSecrets(e)
		}
	}
	return n
}

// Generic marshals any SDK value to generic JSON (maps, slices, scalars) so it can be redacted and returned.
func Generic(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	return out, json.Unmarshal(b, &out)
}
