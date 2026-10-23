package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The token file is netrc (machine, login, password; one line or several). Any other shape, including the old three-line form, is refused with a message naming the expected format.
func TestTokenFileReadsNetrcAndStillTheOldThreeLines(t *testing.T) {
	write := func(text string) string {
		p := filepath.Join(t.TempDir(), "tok")
		os.WriteFile(p, []byte(text), 0o600)
		return p
	}
	for name, text := range map[string]string{
		"several lines": "machine fwd.example.com\nlogin me@example.com\npassword s3cret\n",
		"one line":      "machine fwd.example.com login me@example.com password s3cret\n",
		"full url":      "machine https://fwd.example.com/ login me@example.com password s3cret\n",
	} {
		u, user, pass, err := readTokenFile(write(text))
		if err != nil || u != "https://fwd.example.com" || user != "me@example.com" || pass != "s3cret" {
			t.Errorf("%s: %q %q %q %v", name, u, user, pass, err)
		}
	}
	for _, bad := range []string{
		"machine a login x password y machine b login x2 password y2\n",
		"machine fwd.example.com login only\n",
		"just one line that is not enough\n",
		"https://fwd.example.com\nme@example.com\ns3cret\n",
	} {
		_, _, _, err := readTokenFile(write(bad))
		if err == nil || !strings.Contains(err.Error(), "netrc") {
			t.Errorf("%q must be refused naming netrc: %v", bad, err)
		}
	}
}
