package main

// DOGFOOD-TEMP: fwdctl dogfood-note keeps the engineer's FULL, UNREDACTED reproduction details in a private local file, so a public
// (redacted) issue can point at it by a generic slug. It is offline, never uploads or sends anything, and does not run redact-check on
// the note (it is private by design). Remove it with the plan-report-skill-gap playbook (docs/internal.md).

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var reNoteSlug = regexp.MustCompile(`^[a-z0-9-]{3,40}$`)

const noteUsage = "usage: fwdctl dogfood-note --ref <slug> [--file FILE | -]   (slug: [a-z0-9-]{3,40}, generic, never a customer, device or network name)"

// dogfoodNoteCmd writes the details (FILE or stdin) under dir as <UTC-date>-<ref>.md (0600, directory 0700), never overwriting, and prints
// only the path and one instruction line.
func dogfoodNoteCmd(args []string, stdin io.Reader, stdout, stderr io.Writer, dir string, now time.Time) int {
	ref, file, fromStdin := "", "", false
	for i := 0; i < len(args); i++ {
		name, val, hasVal := strings.Cut(args[i], "=")
		take := func() (string, bool) {
			if hasVal {
				return val, true
			}
			if i+1 < len(args) {
				i++
				return args[i], true
			}
			return "", false
		}
		switch {
		case args[i] == "-":
			fromStdin = true
		case name == "--ref":
			v, ok := take()
			if !ok {
				fmt.Fprintln(stderr, noteUsage)
				return usage
			}
			ref = v
		case name == "--file":
			v, ok := take()
			if !ok {
				fmt.Fprintln(stderr, noteUsage)
				return usage
			}
			if v == "-" {
				fromStdin = true
			} else {
				file = v
			}
		default:
			fmt.Fprintln(stderr, noteUsage)
			return usage
		}
	}
	if !reNoteSlug.MatchString(ref) || (file == "") == !fromStdin || (file != "" && fromStdin) {
		fmt.Fprintln(stderr, noteUsage)
		return usage
	}
	var raw []byte
	var err error
	if fromStdin {
		raw, err = io.ReadAll(stdin)
	} else {
		raw, err = os.ReadFile(file)
	}
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return usage
	}
	if dir == "" {
		fmt.Fprintln(stderr, "error: no home directory on this machine")
		return 3
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 3
	}
	_ = os.Chmod(dir, 0o700)
	base := now.UTC().Format("2006-01-02") + "-" + ref
	var path string
	for n := 1; n < 1000; n++ {
		name := base + ".md"
		if n > 1 {
			name = fmt.Sprintf("%s-%d.md", base, n)
		}
		path = filepath.Join(dir, name)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 3
		}
		_, werr := f.Write(raw)
		cerr := f.Close()
		if werr != nil || cerr != nil {
			fmt.Fprintf(stderr, "error: cannot write %s\n", path)
			return 3
		}
		fmt.Fprintln(stdout, path)
		fmt.Fprintf(stdout, "private and local (not uploaded): hand this file to the skills owner in chat or email, never in a GitHub issue or comment; the issue carries only ref %s\n", ref)
		return 0
	}
	fmt.Fprintln(stderr, "error: too many notes with this ref today")
	return 3
}

func dogfoodNoteDir() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "fwdctl", "dogfood-private")
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".local", "share", "fwdctl", "dogfood-private")
	}
	return ""
}
