package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
	"github.com/forwardnetworks/fwdctl/skills"
)

// app carries what every command needs: its input and output, how to open a Forward session, and which top-level command ran (for the update notice).
type app struct {
	in       io.Reader
	out, err io.Writer
	session  func() (*fwd.Session, error)
	conn     connOptions
	top      string
}

// connOptions are the connection flags every command accepts; applyConnectionOptions turns them (and the saved login) into the environment a session reads.
type connOptions struct {
	url, username, passwordFile, tokenFile, configFile string
	insecure                                           bool
}

func (a *app) emit(v any) {
	enc := json.NewEncoder(a.out)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// fail prints a usage-style error and returns the usage exit status.
func (a *app) fail(format string, args ...any) error {
	fmt.Fprintf(a.err, "error: "+format+"\n", args...)
	return exitCode(usage)
}

const rootLong = `fwdctl runs Forward Skills: questions about a Forward network, answered with evidence. A skill is run with a JSON object on stdin and returns one result
(status, evidence, limits, next actions). Skills that write (named edit-*) change nothing unless the input says "apply": true.

Start here: "echo '{}' | fwdctl run inspect-networks" lists the networks you can see, "fwdctl list" shows every skill, and "fwdctl describe plan-investigation" says which
skill answers which question. Every command answers --help; "fwdctl run <skill> --help" shows one skill's inputs and an example.

Credentials (environment): FORWARD_URL, FORWARD_USERNAME, FORWARD_PASSWORD (an API token's access key and secret work).
Or flags: --url URL --username NAME --password-file FILE [--insecure] [--config FILE]; or a saved login (fwdctl login), or a file
~/.config/fwdctl/config.json {"url", "username", "password_file"} (the password is read from that file, which must be mode 600).
Flags win over the environment, the environment over the file.
Set FORWARD_INSECURE=true only for a self-signed Forward: it turns TLS verification off and every result records it.

Long queries: FORWARD_TIMEOUT=600s raises the per-call HTTP limit (default 120s); a query cut off by it falls back to Forward's asynchronous API
(FORWARD_NQE_MODE=auto|sync|async, FORWARD_NQE_WAIT=20m). More: fwdctl docs troubleshooting.

Exit status: 0 ok, 1 failed (a finding), 2 unknown (could not decide: never a pass), 3 error, 64 bad usage or input.`

// execute runs one command line and returns the exit status.
func (a *app) execute(args []string) int {
	root := a.newRoot()
	root.SetArgs(args)
	root.SetIn(a.in)
	root.SetOut(a.out)
	root.SetErr(a.err)
	err := root.Execute()
	var ec exitCode
	if err != nil && !errors.As(err, &ec) {
		// cobra rejected the command line (an unknown command or flag, a missing flag, a wrong argument count): say so and where the usage is
		fmt.Fprintf(a.err, "error: %v\n", err)
		if c, _, ferr := root.Find(args); ferr == nil && c != nil {
			fmt.Fprintf(a.err, "Run '%s --help' for usage.\n", c.CommandPath())
		} else {
			fmt.Fprintln(a.err, "Run 'fwdctl --help' for usage.")
		}
		return usage
	}
	return a.code(err)
}

func (a *app) newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "fwdctl",
		Short:         "Forward Networks: skills, NQE and the Forward API from a shell or an agent",
		Long:          rootLong,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		Version:       fmt.Sprintf("%s (commit %s, built %s)", version, commit, date),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SetOut(a.err) // no command: the help goes to stderr and the status is "bad usage"
			_ = cmd.Help()
			return exitCode(usage)
		},
	}
	root.SetVersionTemplate("fwdctl {{.Version}}\n")
	root.AddGroup(
		&cobra.Group{ID: "skills", Title: "Skills:"},
		&cobra.Group{ID: "nqe", Title: "NQE:"},
		&cobra.Group{ID: "setup", Title: "Setup and help:"},
	)
	pf := root.PersistentFlags()
	pf.StringVar(&a.conn.url, "url", "", "Forward URL (overrides FORWARD_URL)")
	pf.StringVar(&a.conn.username, "username", "", "login name or API token access key (overrides FORWARD_USERNAME)")
	pf.StringVar(&a.conn.passwordFile, "password-file", "", "file holding the password or token secret, mode 600 (overrides FORWARD_PASSWORD)")
	pf.StringVar(&a.conn.tokenFile, "token-file", "", "a token file: URL, username and password on three lines, mode 600")
	pf.StringVar(&a.conn.configFile, "config", "", "connection config file (default ~/.config/fwdctl/config.json)")
	pf.BoolVar(&a.conn.insecure, "insecure", false, "do NOT verify the TLS certificate (a self-signed Forward; every result records it)")
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		a.top = topLevel(cmd)
		switch a.top {
		case "redact-check", "dogfood-note", "completion", "help", "__complete", "__completeNoDesc", "":
			// these read no connection (and the first two must never read the token file)
		default:
			if err := applyConnectionOptions(a.conn); err != nil {
				return a.fail("%v", err)
			}
		}
		autoUpdate(a.top, a.err)
		return nil
	}

	root.AddCommand(a.listCmd(), a.describeCmd(), a.runCmd(), a.whichCmd(), a.contextCmd())
	root.AddCommand(a.nqeCmd())
	root.AddCommand(a.installCmd(), a.loginCmd(), a.whoamiCmd(), a.updateCmd(), a.docsCmd(), a.versionCmd())
	root.AddCommand(a.redactCmd(), a.noteCmd()) // DOGFOOD-TEMP
	root.SetCompletionCommandGroupID("setup")
	root.SetHelpCommand(a.helpCmd(root))
	return root
}

// topLevel is the name of the command directly under the root that the line selected ("nqe" for "nqe run").
func topLevel(cmd *cobra.Command) string {
	for cmd.HasParent() && cmd.Parent().HasParent() {
		cmd = cmd.Parent()
	}
	if !cmd.HasParent() {
		return ""
	}
	return cmd.Name()
}

func skillNames(toComplete string) []string {
	var out []string
	for _, n := range skills.Names() {
		if strings.HasPrefix(n, toComplete) {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

func (a *app) listCmd() *cobra.Command {
	return &cobra.Command{
		Use: "list", GroupID: "skills", Short: "every skill: name, description, input schema (JSON)",
		Long: "Prints every skill as JSON: name, description, input_schema, class (read or write), runnable.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			all, err := skills.All()
			if err != nil {
				return a.fail("%v", err)
			}
			a.emit(all)
			return nil
		},
	}
}

func (a *app) describeCmd() *cobra.Command {
	return &cobra.Command{
		Use: "describe <skill> [reference-file]", GroupID: "skills", Short: "one skill's procedure (JSON); with a reference file name, that file's text",
		Long: "Prints a skill's procedure. A skill's reference files (see \"references\") print as plain text when named.",
		Args: cobra.RangeArgs(1, 2),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) == 0 {
				return skillNames(toComplete), cobra.ShellCompDirectiveNoFileComp
			}
			if len(args) == 1 {
				if m, err := skills.Describe(args[0]); err == nil {
					return m.References, cobra.ShellCompDirectiveNoFileComp
				}
			}
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := skills.Describe(args[0])
			if err != nil {
				return a.fail("%v; try `list`", err)
			}
			if len(args) == 2 { // a reference file is plain markdown, not JSON: it is meant to be read
				text, err := skills.Reference(args[0], args[1])
				if err != nil {
					return a.fail("%v; this skill's references: %s", err, strings.Join(m.References, ", "))
				}
				fmt.Fprint(a.out, text)
				return nil
			}
			a.emit(struct {
				skills.Meta
				Procedure string `json:"procedure"`
				AliasOf   string `json:"alias_of,omitempty"`
			}{m, m.Body, skills.AliasNote(args[0])})
			return nil
		},
	}
}

func (a *app) whichCmd() *cobra.Command {
	return &cobra.Command{
		Use: "which <question>", GroupID: "skills", Short: "which skill or playbook answers a question (an offline first guess)",
		Long: "Ranks the rows of the router table (plan-investigation) against the question and prints the best skills and playbooks, with the row that matched.\n" +
			"Offline, no model: a first guess. `fwdctl describe plan-investigation` is the full table.",
		Example: `  fwdctl which "why can't host A reach host B"`,
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sug, err := skills.Route(strings.Join(args, " "), 4)
			if err != nil {
				return a.fail("%v", err)
			}
			if sug == nil {
				sug = []skills.Suggestion{}
			}
			a.emit(map[string]any{"suggestions": sug, "note": "an offline first guess from plan-investigation's table; read it with `fwdctl describe plan-investigation`. A playbook (playbook: true) does not run: read it with `fwdctl describe <name>` and follow its steps."})
			return nil
		},
	}
}

func (a *app) contextCmd() *cobra.Command {
	c := parent(&cobra.Command{
		Use: "context", GroupID: "nqe", Short: "NQE authoring aids: worked examples, real field names",
		Long: "NQE authoring aids: worked examples closest to a question, or real field names matching a term.",
	})
	for _, kind := range []string{"nqe", "schema"} {
		kind := kind
		k := 3
		short := map[string]string{"nqe": "NQE worked examples closest to a question", "schema": "real NQE field names (and enum values) matching a term"}[kind]
		sub := &cobra.Command{
			Use: kind + " <" + map[string]string{"nqe": "question", "schema": "term"}[kind] + ">", Short: short, Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				out, err := skills.Context(kind, args[0], k)
				if err != nil {
					return a.fail("%v", err)
				}
				a.emit(out)
				return nil
			},
		}
		sub.Flags().IntVarP(&k, "top", "k", 3, "how many to return")
		c.AddCommand(sub)
	}
	return c
}

func (a *app) whoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use: "whoami", GroupID: "setup", Short: "who this connects as: Forward URL, login, organization, Forward version",
		Long: "Prints the Forward URL, the login, the organization and the Forward version this connects with (one request each); exit 3 if the login does not work.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return a.exit(whoami(a.session, a.out, a.err)) },
	}
}

func (a *app) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use: "version", GroupID: "setup", Short: "the release this binary was built from", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(a.out, "fwdctl %s (commit %s, built %s)\n", version, commit, date)
			return nil
		},
	}
}

func (a *app) docsCmd() *cobra.Command {
	entries, _ := guide.ReadDir("guide")
	var topics []string
	for _, e := range entries {
		topics = append(topics, strings.TrimSuffix(e.Name(), ".md"))
	}
	sort.Strings(topics)
	return &cobra.Command{
		Use: "docs [topic]", Aliases: []string{"guide"}, GroupID: "setup", Short: "the built-in guide, readable offline",
		Long:      "The built-in guide, readable offline. Topics: " + strings.Join(topics, ", ") + ".",
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: topics,
		RunE:      func(cmd *cobra.Command, args []string) error { return a.exit(docsCmd(args, a.out, a.err)) },
	}
}

func (a *app) loginCmd() *cobra.Command {
	var file string
	var forget bool
	c := &cobra.Command{
		Use: "login", GroupID: "setup", Short: "remember a login so every run and every agent session uses it",
		Long: "Remember where the login is, so every later fwdctl run (and every agent session that runs it) works with no environment.\n" +
			"TOKENFILE has three lines: the Forward URL, the username (or an API token's access key) and the password (or its secret), and must be mode 600.\n" +
			"The login is checked, then only the PATH of the file is saved in ~/.config/fwdctl/config.json; the password is never copied. Flags and FORWARD_* variables still win.",
		Example: "  fwdctl login --file ~/customer.token\n  fwdctl login --forget",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.exit(loginRun(file, forget, a.out, a.err, func() int { return whoami(a.session, a.out, a.err) }))
		},
	}
	c.Flags().StringVar(&file, "file", "", "a token file: URL, username and password on three lines (mode 600)")
	c.Flags().BoolVar(&forget, "forget", false, "remove the saved login")
	c.MarkFlagsMutuallyExclusive("file", "forget")
	c.MarkFlagsOneRequired("file", "forget")
	return c
}

func (a *app) updateCmd() *cobra.Command {
	var check, force bool
	var tag string
	c := &cobra.Command{
		Use: "update", GroupID: "setup", Short: "update fwdctl to the newest release (checksum verified)",
		Long: "Replaces this binary with the newest release after checking it against the release's SHA256SUMS. --check only reports (exit 2 when an update\n" +
			"exists). A private repository needs GITHUB_TOKEN. FWDCTL_AUTO_UPDATE=1 applies updates by itself; FWDCTL_NO_UPDATE_CHECK=1 silences the daily notice.\n" +
			"A Homebrew install is updated with: brew upgrade fwdctl.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.exit(updateRun(check, force, tag, a.out, a.err))
		},
	}
	c.Flags().BoolVar(&check, "check", false, "only report whether a newer release exists (exit 2 when one does)")
	c.Flags().StringVar(&tag, "version", "", "install this release (for example v0.5.48) instead of the newest")
	c.Flags().BoolVar(&force, "force", false, "install even when it is not newer, or replace a development build")
	return c
}

func (a *app) installCmd() *cobra.Command {
	c := parent(&cobra.Command{
		Use: "install", GroupID: "setup", Short: "put the skills where an agent loads them",
		Long: "Install the skills for an agent: into Claude Code's skills directory, or as a managed section of an AGENTS.md / CLAUDE.md file.",
	})
	var dir, file string
	claude := &cobra.Command{
		Use: "claude", Short: "write the skills where Claude Code loads them (default ~/.claude/skills)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.exit(installRun("claude", dir, "", a.out, a.err))
		},
	}
	claude.Flags().StringVar(&dir, "dir", "", "directory to write the skills into (default ~/.claude/skills)")
	agents := &cobra.Command{
		Use: "agents", Short: "add a managed skills section to an AGENTS.md / CLAUDE.md style file (stdout without --file)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.exit(installRun("agents", "", file, a.out, a.err))
		},
	}
	agents.Flags().StringVar(&file, "file", "", "the file to add the section to (default: print it)")
	c.AddCommand(claude, agents)
	return c
}

// runCmd is `fwdctl run <skill>`: the skill's inputs as one JSON object on stdin (or --input FILE), the result envelope on stdout.
func (a *app) runCmd() *cobra.Command {
	var file, format string
	var withOps bool
	c := &cobra.Command{
		Use: "run <skill>", GroupID: "skills", Short: "run a skill: inputs as JSON on stdin, the result on stdout",
		Long: "Run a skill. Reads the skill's inputs as a JSON object from --input FILE or stdin and prints the result envelope (status, evidence, limits, next actions).\n" +
			"Exit status: 0 ok, 1 failed, 2 unknown (never a pass), 3 error, 64 bad usage. `fwdctl run <skill> --help` shows that skill's inputs and an example.",
		Example: `  echo '{}' | fwdctl run inspect-networks
  echo '{"network_id": "123"}' | fwdctl run inspect-snapshots
  fwdctl run edit-org-property --input plan.json`,
		Args: cobra.ExactArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) == 0 {
				return skillNames(toComplete), cobra.ShellCompDirectiveNoFileComp
			}
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.exit(a.runSkill(args[0], file, format, withOps))
		},
	}
	c.Flags().StringVar(&file, "input", "", "JSON file with the skill inputs (default: stdin)")
	c.Flags().StringVar(&format, "format", "json", "json (the whole result), or table or csv (the largest list of rows in the evidence)")
	c.Flags().BoolVar(&withOps, "ops", false, "include the log of Forward calls the skill made (audit data; omitted by default to save tokens)")
	_ = c.RegisterFlagCompletionFunc("format", cobra.FixedCompletions([]string{"json", "table", "csv"}, cobra.ShellCompDirectiveNoFileComp))
	// `run <skill> --help` is the skill's own help: what it answers, its inputs, an example
	def := c.HelpFunc()
	c.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		// cobra hands the help function the whole command line: the skill is the first word after "run" that is not a flag
		for i := 0; i < len(args)-1; i++ {
			if args[i] == "run" && !strings.HasPrefix(args[i+1], "-") {
				if _, err := skills.Describe(args[i+1]); err == nil {
					skillHelp(a.out, args[i+1])
					return
				}
			}
		}
		def(cmd, args)
	})
	return c
}

func (a *app) helpCmd(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use: "help [command | skill]", GroupID: "setup", Short: "help for any command, or for one skill",
		Long: "Help for any command (fwdctl help nqe run), or for a skill (fwdctl help inspect-snapshots: what it answers, its inputs, an example).",
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			var out []string
			for _, c := range root.Commands() {
				if !c.Hidden && strings.HasPrefix(c.Name(), toComplete) {
					out = append(out, c.Name())
				}
			}
			return append(out, skillNames(toComplete)...), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return root.Help()
			}
			if c, _, err := root.Find(args); err == nil && c != nil && c != root {
				return c.Help()
			}
			if _, err := skills.Describe(args[0]); err == nil {
				skillHelp(a.out, args[0])
				return nil
			}
			return a.fail("no command or skill %q; `fwdctl help` lists the commands", strings.Join(args, " "))
		},
	}
}

// runSkill is the body of `fwdctl run`.
func (a *app) runSkill(name, file, format string, withOps bool) int {
	fail := func(f string, v ...any) int { fmt.Fprintf(a.err, "error: "+f+"\n", v...); return usage }
	var raw []byte
	var err error
	if file != "" {
		raw, err = os.ReadFile(file)
	} else {
		raw, err = io.ReadAll(a.in)
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
	if !validFormat(format) || format == "jsonl" {
		return fail("--format is json, table or csv")
	}
	sess, err := a.session()
	if err != nil {
		return fail("%v", err)
	}
	warnInsecure(a.err, sess)
	r, err := skills.Run(context.Background(), name, sess, raw)
	if errors.Is(err, skills.ErrInvalidInput) || errors.Is(err, skills.ErrUnknown) {
		return fail("%v", err)
	}
	if err != nil {
		// The skill could not run (transport, auth, an unexpected Forward answer). That is an `error` result, not a finding about the network.
		r = errorResult(name, err, obj, sess)
	}
	if !withOps {
		r.Operations = nil
	}
	if format == "table" || format == "csv" {
		if rows, where := largestRows(r); len(rows) > 0 {
			fmt.Fprintf(a.err, "status: %s; %s (%d rows from %s)\n", r.Status, r.Finding, len(rows), where)
			for _, l := range r.Limits {
				fmt.Fprintf(a.err, "limit: %s\n", l)
			}
			if err := renderRows(a.out, rows, format); err != nil {
				return fail("%v", err)
			}
			return exitFor[r.Status]
		}
		fmt.Fprintln(a.err, "note: this result has no list of rows to print as a "+format+"; showing the whole result as JSON")
	}
	a.emit(r)
	return exitFor[r.Status]
}

// warnInsecure tells the operator, once per process, that verification is off.
func warnInsecure(stderr io.Writer, s *fwd.Session) {
	if s != nil && s.Insecure() {
		fmt.Fprintln(stderr, "warning: FORWARD_INSECURE is set: TLS certificate verification is DISABLED for this run.")
	}
}

// errorResult is the `error` result for a skill that could not run (transport, auth, an unexpected Forward answer).
func errorResult(name string, err error, obj map[string]any, sess *fwd.Session) result.Result {
	r := result.NewError(name, err.Error(), result.Context{NetworkID: networkOf(obj)})
	r.Operations = sess.Operations()
	return r
}

func networkOf(obj map[string]any) string {
	if s, ok := obj["network_id"].(string); ok && s != "" {
		return s
	}
	return "unknown"
}

// parent makes a command that only groups subcommands: with no word it shows its help, with an unknown one it is a usage error (cobra would otherwise show the help for both).
func parent(c *cobra.Command) *cobra.Command {
	c.Args = cobra.ArbitraryArgs
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath())
		}
		return cmd.Help()
	}
	return c
}
