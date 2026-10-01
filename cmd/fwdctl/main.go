// Command fwdctl runs Forward Skills from a shell or any harness.
//
//	fwdctl version                     the release this binary was built from
//	fwdctl list                        every skill: name, description, maturity, input schema
//	fwdctl describe <skill> [ref]      one skill with its procedure; with a reference file name, that file's text
//	fwdctl run <skill> [--input FILE] [--ops]  read the inputs as JSON from FILE (or stdin), print the result;
//	                                           --ops adds the log of Forward calls made (omitted by default: it is audit data and costs tokens)
//	fwdctl install claude [--dir DIR]  write the skills where Claude Code loads them (default ~/.claude/skills)
//	fwdctl install agents [--file F]   add a managed skills section to an AGENTS.md / CLAUDE.md style file (stdout without --file)
//	fwdctl context nqe <question>      NQE worked examples closest to a question (for authoring)
//	fwdctl context schema <term>       real NQE field names (and enum values) matching a term
//
// Credentials come from FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD (an API token's access key and
// secret work). TLS is verified against the system trust store. For a self-signed Forward set FORWARD_INSECURE=true, which turns
// verification off (a warning is printed and every result records it).
//
// Exit status: 0 ok, 1 failed, 2 unknown, 3 error, 64 bad usage or input. A harness can branch on the code
// without parsing JSON, and a skill that could not decide never exits 0.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
	"github.com/forwardnetworks/fwdctl/skills"
)

// Set at build time by scripts/release.sh (-ldflags -X); "dev" for a plain go build.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

const usage = 64

var exitFor = map[result.Status]int{result.OK: 0, result.Failed: 1, result.Unknown: 2, result.Error: 3}

func main() {
	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	args, err := os.Args[1:], error(nil)
	if cmd != "redact-check" && cmd != "dogfood-note" { // DOGFOOD-TEMP: redact-check never reads the saved login's token file (its third line is the secret)
		args, err = applyConnection(os.Args[1:], os.Stderr)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(usage)
	}
	if len(args) > 0 {
		cmd = args[0]
	}
	autoUpdate(cmd, os.Stderr)
	code := run(args, os.Stdin, os.Stdout, os.Stderr, func() (*fwd.Session, error) {
		return fwd.NewSession(fwd.ConfigFromEnv())
	})
	updateNotice(cmd, os.Stderr)
	os.Exit(code)
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, session func() (*fwd.Session, error)) int {
	emit := func(v any) {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(v)
	}
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "error: "+format+"\n", a...)
		return usage
	}
	if len(args) == 0 {
		fmt.Fprint(stderr, helpText)
		return usage
	}
	if isHelp(args[0]) {
		if len(args) > 1 {
			if h, ok := commandHelp[args[1]]; ok {
				fmt.Fprintln(stdout, h)
				return 0
			}
			return skillHelp(stdout, args[1])
		}
		fmt.Fprint(stdout, helpText)
		return 0
	}
	if len(args) > 1 && isHelp(args[len(args)-1]) {
		if args[0] == "run" && len(args) >= 3 {
			return skillHelp(stdout, args[1])
		}
		if h, ok := commandHelp[args[0]]; ok {
			fmt.Fprintln(stdout, h)
			return 0
		}
	}
	switch args[0] {
	case "list":
		all, err := skills.All()
		if err != nil {
			return fail("%v", err)
		}
		emit(all)
		return 0
	case "describe":
		if len(args) < 2 || len(args) > 3 {
			return fail("usage: describe <skill> [reference-file]")
		}
		m, err := skills.Describe(args[1])
		if err != nil {
			return fail("%v; try `list`", err)
		}
		if len(args) == 3 { // a reference file is plain markdown, not JSON: it is meant to be read
			text, err := skills.Reference(args[1], args[2])
			if err != nil {
				return fail("%v; this skill's references: %s", err, strings.Join(m.References, ", "))
			}
			fmt.Fprint(stdout, text)
			return 0
		}
		emit(struct {
			skills.Meta
			Procedure string `json:"procedure"`
			AliasOf   string `json:"alias_of,omitempty"`
		}{m, m.Body, skills.AliasNote(args[1])})
		return 0
	case "update":
		return updateCmd(args[1:], stdout, stderr)
	case "login":
		return loginCmd(args[1:], stdout, stderr, func() int { return whoami(session, stdout, stderr) })
	case "which":
		if len(args) < 2 {
			return fail("usage: which <question>")
		}
		sug, err := skills.Route(strings.Join(args[1:], " "), 4)
		if err != nil {
			return fail("%v", err)
		}
		if sug == nil {
			sug = []skills.Suggestion{}
		}
		emit(map[string]any{"suggestions": sug, "note": "an offline first guess from plan-investigation's table; read it with `fwdctl describe plan-investigation`. A playbook (playbook: true) does not run: read it with `fwdctl describe <name>` and follow its steps."})
		return 0
	case "redact-check": // DOGFOOD-TEMP
		return redactCheckCmd(args[1:], stdin, stdout, stderr, defaultConfigPath())
	case "dogfood-note": // DOGFOOD-TEMP
		return dogfoodNoteCmd(args[1:], stdin, stdout, stderr, dogfoodNoteDir(), time.Now())
	case "completion":
		return completionCmd(args[1:], stdout, stderr)
	case "whoami":
		return whoami(session, stdout, stderr)
	case "docs", "guide":
		return docsCmd(args[1:], stdout, stderr)
	case "nqe":
		if len(args) > 1 && args[1] == "run" {
			return nqeRunCmd(args[2:], stdin, stdout, stderr, session)
		}
		if len(args) > 1 && args[1] == "bundle" {
			return nqeBundleCmd(args[2:], stdout, stderr, session)
		}
		if len(args) > 1 && args[1] == "synthesize" {
			return nqeSynthesizeCmd(args[2:], stdout, stderr, session)
		}
		return nqeCmd(args[1:], stdin, stdout, stderr)
	case "version", "--version":
		fmt.Fprintf(stdout, "fwdctl %s (commit %s, built %s)\n", version, commit, date)
		return 0
	case "install":
		return installCmd(args[1:], stdout, stderr)
	case "context":
		if len(args) < 3 || (args[1] != "nqe" && args[1] != "schema") {
			return fail("usage: context nqe <question> [-k N] | context schema <term> [-k N]")
		}
		fs := flag.NewFlagSet("context", flag.ContinueOnError)
		fs.SetOutput(stderr)
		k := fs.Int("k", 3, "worked examples to return")
		question := args[2]
		if err := fs.Parse(args[3:]); err != nil {
			return usage
		}
		out, err := skills.Context(args[1], question, *k)
		if err != nil {
			return fail("%v", err)
		}
		emit(out)
		return 0
	case "run":
		fs := flag.NewFlagSet("run", flag.ContinueOnError)
		fs.SetOutput(stderr)
		file := fs.String("input", "", "JSON file with the skill inputs (default: stdin)")
		format := fs.String("format", "json", "json (the whole result), or table or csv (the largest list of rows in the evidence)")
		withOps := fs.Bool("ops", false, "include the log of Forward calls the skill made (audit data; omitted by default to save tokens)")
		if len(args) < 2 {
			return fail("run takes a skill name; `fwdctl list` shows them, `fwdctl run <skill> --help` shows its inputs")
		}
		name := args[1]
		if err := fs.Parse(args[2:]); err != nil {
			return usage
		}
		var raw []byte
		var err error
		if *file != "" {
			raw, err = os.ReadFile(*file)
		} else {
			raw, err = io.ReadAll(stdin)
		}
		if err != nil {
			return fail("%v", err)
		}
		var obj map[string]any
		if json.Unmarshal(raw, &obj) != nil || obj == nil {
			return fail("inputs must be a JSON object")
		}
		if _, err := skills.Describe(name); err != nil {
			return fail("%v; try `list`", err)
		}
		sess, err := session()
		if err != nil {
			return fail("%v", err)
		}
		warnInsecure(stderr, sess)
		r, err := skills.Run(context.Background(), name, sess, raw)
		if errors.Is(err, skills.ErrInvalidInput) || errors.Is(err, skills.ErrUnknown) {
			return fail("%v", err)
		}
		if err != nil {
			// The skill could not run (transport, auth, an unexpected Forward answer). That is an `error`
			// result, not a finding about the network.
			r = result.NewError(name, err.Error(), result.Context{NetworkID: networkOf(obj)})
			r.Operations = sess.Operations()
		}
		if !*withOps {
			r.Operations = nil
		}
		if *format == "table" || *format == "csv" {
			if rows, where := largestRows(r); len(rows) > 0 {
				fmt.Fprintf(stderr, "status: %s; %s (%d rows from %s)\n", r.Status, r.Finding, len(rows), where)
				for _, l := range r.Limits {
					fmt.Fprintf(stderr, "limit: %s\n", l)
				}
				if err := renderRows(stdout, rows, *format); err != nil {
					return fail("%v", err)
				}
				return exitFor[r.Status]
			}
			fmt.Fprintln(stderr, "note: this result has no list of rows to print as a "+*format+"; showing the whole result as JSON")
		} else if !validFormat(*format) {
			return fail("--format is json, table or csv")
		}
		emit(r)
		return exitFor[r.Status]
	}
	return fail("unknown command %q; `fwdctl help` lists the commands", args[0])
}

// warnInsecure tells the operator, once per process, that verification is off.
func warnInsecure(stderr io.Writer, s *fwd.Session) {
	if s != nil && s.Insecure() {
		fmt.Fprintln(stderr, "warning: FORWARD_INSECURE is set: TLS certificate verification is DISABLED for this run.")
	}
}

func networkOf(obj map[string]any) string {
	if s, ok := obj["network_id"].(string); ok && s != "" {
		return s
	}
	return "unknown"
}

func installCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "claude" && args[0] != "agents") {
		fmt.Fprintln(stderr, "error: usage: install claude [--dir DIR] | install agents [--file FILE]")
		return usage
	}
	all, err := skills.All()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return usage
	}
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "claude: directory to write skills into (default ~/.claude/skills)")
	file := fs.String("file", "", "agents: file to add the section to (default: print it)")
	if err := fs.Parse(args[1:]); err != nil {
		return usage
	}
	switch args[0] {
	case "claude":
		target := *dir
		if target == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				fmt.Fprintf(stderr, "error: %v\n", err)
				return usage
			}
			target = filepath.Join(home, ".claude", "skills")
		}
		written, err := installClaude(target, all)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "wrote %d skills under %s\n", len(written), target)
	case "agents":
		if *file == "" {
			fmt.Fprint(stdout, agentsBlock(all))
			return 0
		}
		if err := installAgents(*file, all); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "updated %s\n", *file)
	}
	return 0
}
