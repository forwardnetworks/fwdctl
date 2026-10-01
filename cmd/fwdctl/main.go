// Command fwdctl runs Forward Skills from a shell or any harness, and carries the NQE tools and the Forward API client's commands.
//
// The command line is a cobra tree (root.go, nqe_cmds.go): every command answers --help with its own usage and flags, `fwdctl completion bash|zsh|fish|powershell` prints a
// completion script that completes skill names, command words and flag values, and `fwdctl run <skill> --help` is the skill's own help (what it answers, its inputs, an example).
// A command that decides its own exit status returns it as an exitCode; cobra's own usage errors (an unknown command or flag, a missing flag) are status 64.
//
// Credentials come from FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD (an API token's access key and secret work), the connection flags (--url, --username,
// --password-file, --insecure), or the saved login (fwdctl login). TLS is verified against the system trust store. For a self-signed Forward set FORWARD_INSECURE=true, which
// turns verification off (a warning is printed and every result records it).
//
// Exit status: 0 ok, 1 failed, 2 unknown, 3 error, 64 bad usage or input. A harness can branch on the code without parsing JSON, and a skill that could not decide never
// exits 0.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// Set at build time by scripts/release.sh (-ldflags -X); "dev" for a plain go build.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

const usage = 64

var exitFor = map[result.Status]int{result.OK: 0, result.Failed: 1, result.Unknown: 2, result.Error: 3}

// requestTimeout, when set (by nqe run --timeout), is how long one HTTP call may take; it replaces the default 120s that otherwise ends a long synchronous query.
var requestTimeout time.Duration

func main() {
	a := &app{in: os.Stdin, out: os.Stdout, err: os.Stderr, session: func() (*fwd.Session, error) {
		cfg := fwd.ConfigFromEnv()
		if requestTimeout > 0 {
			cfg.Timeout = requestTimeout
		}
		return fwd.NewSession(cfg)
	}}
	code := a.execute(os.Args[1:])
	updateNotice(a.top, os.Stderr)
	os.Exit(code)
}

// run is the entry the tests use: the same command tree with its input, output and session injected.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, session func() (*fwd.Session, error)) int {
	return (&app{in: stdin, out: stdout, err: stderr, session: session}).execute(args)
}

// exitCode is a command's exit status, returned as an error so cobra unwinds to execute.
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// code turns a command's result into the process exit status. A command that decided its own status returns an exitCode; anything else is cobra rejecting the command line
// (an unknown command or flag, a wrong argument count), which is a usage error.
func (a *app) code(err error) int {
	if err == nil {
		return 0
	}
	var ec exitCode
	if errors.As(err, &ec) {
		return int(ec)
	}
	fmt.Fprintf(a.err, "error: %v\n", err)
	return usage
}

// exit is how a command returns the status an older int-returning helper computed.
func (a *app) exit(code int) error {
	if code == 0 {
		return nil
	}
	return exitCode(code)
}
