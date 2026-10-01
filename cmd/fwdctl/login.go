package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// loginCmd remembers where the login is, so every later fwdctl run (and every agent session that runs it) works with no environment:
//
//	fwdctl login --file ~/customer.token     remember a token file (URL, username, password: three lines)
//	fwdctl login --forget                    remove what was remembered
//
// It stores only the path of the file, never the password, and checks the login before it saves anything.
func loginCmd(args []string, stdout, stderr io.Writer, whoamiRun func() int) int {
	path := defaultConfigPath()
	if path == "" {
		fmt.Fprintln(stderr, "error: no user config directory on this machine")
		return 3
	}
	if len(args) == 1 && args[0] == "--forget" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 3
		}
		fmt.Fprintln(stdout, "forgot the saved login")
		return 0
	}
	if len(args) != 2 || args[0] != "--file" {
		fmt.Fprintln(stderr, "usage: fwdctl login --file TOKENFILE | fwdctl login --forget")
		return usage
	}
	abs, err := filepath.Abs(args[1])
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return usage
	}
	url, user, pass, err := readTokenFile(abs)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return usage
	}
	for k, v := range map[string]string{"FORWARD_URL": url, "FORWARD_USERNAME": user, "FORWARD_PASSWORD": pass} {
		os.Setenv(k, v)
	}
	if code := whoamiRun(); code != 0 {
		fmt.Fprintln(stderr, "the login does not work, so it was not saved")
		return code
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 3
	}
	b, _ := json.MarshalIndent(connFile{TokenFile: abs}, "", "  ")
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 3
	}
	fmt.Fprintf(stdout, "saved: fwdctl will use %s from now on (%s). Run fwdctl login --forget to remove it.\n", abs, path)
	return 0
}
