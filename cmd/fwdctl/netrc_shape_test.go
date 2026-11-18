package main

import (
	"strings"
	"testing"
)

// A token file of just a user and a password (two lines, no host) is refused with a message that says what is missing, from the file's shape alone: the message must never carry any of the
// file's content, since it holds a secret.
func TestParseNetrcSaysWhatIsMissingAndNeverEchoesTheFile(t *testing.T) {
	const user, secret = "example-user@example.test", "s3cr3t-Value-xyz"
	for name, tc := range map[string]struct{ text, want string }{
		"user and password only": {user + "\n" + secret + "\n", "2 non-empty line(s)"},
		"no machine":             {"login " + user + " password " + secret + "\n", "no machine line"},
		"two machines":           {"machine a.example.test login " + user + " password " + secret + "\nmachine b.example.test\n", "more than one machine"},
		"no password":            {"machine a.example.test login " + user + "\n", "no password"},
	} {
		_, _, _, err := parseNetrc("/x/f.netrc", tc.text)
		if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "machine HOST") {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), user) {
			t.Errorf("%s: the message echoes the file: %v", name, err)
		}
	}
	if u, l, p, err := parseNetrc("/x/f.netrc", "machine fwd.example.test\nlogin "+user+"\npassword "+secret+"\n"); err != nil || u != "https://fwd.example.test" || l != user || p != secret {
		t.Errorf("a valid netrc must still parse: %v %v %v %v", u, l, p, err)
	}
}
