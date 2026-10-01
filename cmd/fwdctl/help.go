package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/forwardnetworks/fwdctl/skills"
)

const helpText = `fwdctl runs Forward Skills: questions about a Forward network, answered with evidence.

Usage:
  fwdctl list                          every skill: name, description, input schema (JSON)
  fwdctl describe <skill> [file]       one skill's procedure (JSON); with a reference file name, that file's text
  fwdctl run <skill> [--input FILE]    run a skill: inputs as JSON on stdin or from FILE, result as JSON on stdout
  fwdctl run <skill> --help            what that skill answers, its inputs and an example
  fwdctl context nqe <question>        NQE worked examples closest to a question
  fwdctl context schema <term>         real NQE field names matching a term
  fwdctl nqe lint [FILE|-]             check an NQE query offline: syntax, names, types, deprecations
  fwdctl nqe run --network ID [--file F | --query-id ID [--commit-id C]] [--param N=JSON] [--format table|csv] [--count-by FIELD]   run a query, or a saved one at a commit, and print every row (paged for you); timing on stderr
  fwdctl nqe bundle (--query-id Q|--path P) [--commit-id C] [--override PATH=FILE]... [--add-module PATH=FILE]... [--out FILE] prints ONE self-contained query: the entry and every library module it imports at the commit, names prefixed per module, local files substituted (inline text always imports at the head, and a commit cannot be combined with inline text); run it with nqe run --file.\nfwdctl nqe synthesize internet --network ID [--vrf V] [--device D] [--discovery M]   derive an internet node's connection query from the model (lint-clean or exit 1)
  fwdctl nqe fmt [-w] [--check] [FILE] format NQE in the standard style (stdin to stdout without a file)
  fwdctl nqe lsp                       a language server for NQE (editors: diagnostics, completion, hover, quick fixes)
  fwdctl nqe complete|hover FILE L C   what to write or what is there at a position (1-based)
  fwdctl install claude [--dir DIR]    put the skills where Claude Code loads them
  fwdctl install agents [--file F]     add a skills section to an AGENTS.md or CLAUDE.md file
  fwdctl docs [topic]                  the built-in guide: overview, skills, nqe, install, troubleshooting
  fwdctl update [--check]              update fwdctl to the newest release (checksum verified); FWDCTL_AUTO_UPDATE=1 does it by itself
  fwdctl which <question>              which skill or playbook answers a question (an offline first guess)
  fwdctl login --file TOKENFILE        remember a login (a file of URL, username, password) so every run and every agent session uses it
  fwdctl whoami                        who this connects as: Forward URL, login, organization, Forward version
  fwdctl redact-check [--file F | -] [--deny w]...  DOGFOOD-TEMP: scan an issue draft for customer data (IPs, hosts, ids, secrets) before it is shown or filed
  fwdctl dogfood-note --ref SLUG [--file F | -]  DOGFOOD-TEMP: save full private reproduction details to a local 0600 file (never uploaded) and print its path
  fwdctl completion bash|zsh|fish|powershell  a shell completion script
  fwdctl version                       the release this binary was built from
  fwdctl help [command]                this text, or one command's usage

Credentials (environment): FORWARD_URL, FORWARD_USERNAME, FORWARD_PASSWORD (an API token's access key and secret work).
Or flags before the command: --url URL --username NAME --password-file FILE [--insecure] [--config FILE]; or a file
~/.config/fwdctl/config.json {"url", "username", "password_file"} (the password is read from that file, which must be mode 600).
Flags win over the environment, the environment over the file.
Set FORWARD_INSECURE=true only for a self-signed Forward: it turns TLS verification off and every result records it.

Start here: "echo '{}' | fwdctl run inspect-networks" lists the networks you can see, "fwdctl list" shows every skill, and
"fwdctl describe plan-investigation" says which skill answers which question.

Exit status: 0 ok, 1 failed (a finding), 2 unknown (could not decide: never a pass), 3 error, 64 bad usage or input.
Skills that write (named edit-*) change nothing unless the input says "apply": true.
`

var commandHelp = map[string]string{
	"list":     "usage: fwdctl list\nPrints every skill as JSON: name, description, input_schema, class (read or write), runnable.",
	"describe": "usage: fwdctl describe <skill> [reference-file]\nPrints a skill's procedure. A skill's reference files (see \"references\") print as plain text when named.",
	"run": "usage: fwdctl run <skill> [--input FILE] [--ops]\nReads the skill's inputs as a JSON object from FILE or stdin and prints the result envelope.\n" +
		"  --input FILE   read inputs from FILE instead of stdin\n  --ops          include the log of Forward calls made (audit data)\n" +
		"  fwdctl run <skill> --help shows that skill's inputs and an example.",
	"nqe":          "usage: fwdctl nqe lint [FILE|-]\nOffline NQE check, no Forward connection: syntax errors with line and column, unknown names, wrong argument counts, fields and enum values the\ndata model does not have, type errors, and deprecations with Forward's own advice. Exit 1 on an error. The type check is gradual (it says nothing\nwhere it cannot tell a type), so validate-nqe-query, which runs the query on Forward, is still the last word.\nAlso: fwdctl nqe lsp (language server), fwdctl nqe complete|hover FILE LINE COL, and fwdctl nqe run --network ID [--file F | --query-id ID [--commit-id C]] [--params FILE] [--param NAME=JSON]... [--snapshot ID] [--format json|jsonl|table|csv] [--count-by FIELD] [--max N] [--async] [--meta FILE|-] [--timeout D] (runs the query on Forward and prints every row, not the 200-row sample of validate-nqe-query; --query-id runs a saved library query, at --commit-id or the head; parameters are typed JSON; elapsed time, rows and snapshot go to stderr, so the rows can be parsed from stdout; --meta writes a JSON object with the mode, execution key and outcome (--async uses the asynchronous execution API), Forward's own execution time, rows, and on failure the HTTP status and diagnostics, so a 503 can be told from a query outcome; a run whose wall time is far below Forward's recorded execution time is marked likely_cached: a comment, an unused let and useLatestDataFiles were measured NOT to change Forward's cache key, so a cold timing needs a change that alters the result, such as one extra constant column).\nfwdctl nqe synthesize internet --network ID [--vrf V] [--device D] [--discovery interfaceAddresses|bgpRoutes|ipRoutes|none] [--subnets CIDR,...] [--include-unlikely] [--snapshot ID] prints a ready internet-connection query derived from the model (the inspect-edge analysis), linted; exit 1 if its own output is not clean.",
	"context":      "usage: fwdctl context nqe <question> [-k N]\n       fwdctl context schema <term> [-k N]\nNQE authoring aids: worked examples closest to a question, or real field names matching a term.",
	"install":      "usage: fwdctl install claude [--dir DIR]   (default ~/.claude/skills)\n       fwdctl install agents [--file FILE]   (stdout without --file)",
	"docs":         "usage: fwdctl docs [topic]\nThe built-in guide, readable offline. Topics: overview (default), skills, nqe, install, troubleshooting.",
	"update":       "usage: fwdctl update [--check] [--version vX.Y.Z] [--force]\nReplaces this binary with the newest release after checking it against the release's SHA256SUMS. --check only reports (exit 2 when an update\nexists). A private repository needs GITHUB_TOKEN. FWDCTL_AUTO_UPDATE=1 applies updates by itself; FWDCTL_NO_UPDATE_CHECK=1 silences the daily notice.",
	"which":        "usage: fwdctl which <question>\nRanks the rows of the router table (plan-investigation) against the question and prints the best skills and playbooks, with the row that matched.\nOffline, no model: a first guess. `fwdctl describe plan-investigation` is the full table.",
	"login":        "usage: fwdctl login --file TOKENFILE | fwdctl login --forget\nTOKENFILE has three lines: the Forward URL, the username (or an API token's access key) and the password (or its secret), and must be mode 600.\nThe login is checked, then only the PATH of the file is saved in ~/.config/fwdctl/config.json; the password is never copied. Flags and FORWARD_* variables still win.",
	"whoami":       "usage: fwdctl whoami\nPrints the Forward URL, the login, the organization and the Forward version this connects with (one request each); exit 3 if the login does not work.",
	"redact-check": "usage: fwdctl redact-check [--file FILE | -] [--deny word]...   (DOGFOOD-TEMP)\nOffline scan of a draft GitHub issue for customer data: IP addresses (except documentation ranges), hostnames, device-style names, URLs, secrets, emails, network/snapshot/org ids, home paths, and the words from your saved login (URL host, username) plus --deny and FWDCTL_REDACT_DENY. Prints JSON {ok, findings, note} with every excerpt masked; exit 0 only when clean, 1 on findings, 2 on warnings only (a possible customer or place name), 64 on bad usage. --deny repeats and takes comma lists; acme-corp, \"acme corp\" and AcmeCorp match one another. Also denied automatically: the saved login, OS user, machine name, redact_deny in the config file, FWDCTL_REDACT_DENY. Pass every customer, organization and network name you know: the tool cannot. Clean is necessary, not sufficient.",
	"completion":   "usage: fwdctl completion bash|zsh|fish|powershell\nPrints a completion script: source <(fwdctl completion bash), or for zsh/fish/PowerShell the line in its first comment.",
	"version":      "usage: fwdctl version\nPrints the release, commit and build date.",
}

func isHelp(a string) bool { return a == "help" || a == "-h" || a == "--help" }

// skillHelp is what a person or agent needs before running one skill: what it answers, its inputs, and how to call it.
func skillHelp(w io.Writer, name string) int {
	m, err := skills.Describe(name)
	if err != nil {
		fmt.Fprintf(w, "error: %v; run `fwdctl list` for the skills\n", err)
		return usage
	}
	fmt.Fprintf(w, "%s\n\n%s\n\n", m.Name, m.Description)
	if note := skills.AliasNote(name); note != "" {
		fmt.Fprintf(w, "`%s` is a retired name: it runs %s. The inputs below are the surviving skill's; the old inputs still work.\n\n", name, note)
	}
	if !m.Runnable {
		fmt.Fprintf(w, "This is a procedure, not something to run. Read it with: fwdctl describe %s\n", m.Name)
		return 0
	}
	fmt.Fprintln(w, strings.TrimPrefix(runSection(m), "\n"))
	fmt.Fprintf(w, "Procedure and evidence: fwdctl describe %s\n", m.Name)
	return 0
}
