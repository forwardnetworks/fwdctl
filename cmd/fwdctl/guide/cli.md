# fwdctl command reference

Generated from the command tree (`fwdctl <command> --help` shows the same for one command). Every command answers `--help`.

Connection flags work on every command that talks to Forward:

```
      --config string          connection config file (default ~/.config/fwdctl/config.json)
      --insecure               do NOT verify the TLS certificate (a self-signed Forward; every result records it)
      --password-file string   file holding the password or token secret, mode 600 (overrides FORWARD_PASSWORD)
      --token-file string      a token file: URL, username and password on three lines, mode 600
      --url string             Forward URL (overrides FORWARD_URL)
      --username string        login name or API token access key (overrides FORWARD_USERNAME)
```

Exit status: 0 ok, 1 failed (a finding), 2 unknown (never a pass), 3 error, 64 bad usage or input.

## fwdctl context

NQE authoring aids: worked examples closest to a question, or real field names matching a term.

```
fwdctl context
```

## fwdctl context nqe

NQE worked examples closest to a question

```
fwdctl context nqe <question> [flags]
```

Examples:

```
  fwdctl context nqe "bgp neighbors that are down" -k 2
```

Flags:

```
  -k, --top int   how many to return (default 3)
```

## fwdctl context schema

real NQE field names (and enum values) matching a term

```
fwdctl context schema <term> [flags]
```

Examples:

```
  fwdctl context schema vrf
```

Flags:

```
  -k, --top int   how many to return (default 3)
```

## fwdctl describe

Prints a skill's procedure. A skill's reference files (see "references") print as plain text when named.

```
fwdctl describe <skill> [reference-file]
```

Examples:

```
  fwdctl describe plan-investigation
  fwdctl describe inspect-environment properties.md
```

## fwdctl docs

The built-in guide, readable offline. Topics: agents, architecture, cli, install, nqe, overview, skills, troubleshooting.

```
fwdctl docs [topic]
```

Examples:

```
  fwdctl docs
  fwdctl docs nqe
  fwdctl docs cli
```

## fwdctl install

Install the skills for an agent: into Claude Code's skills directory, or as a managed section of an AGENTS.md / CLAUDE.md file.

```
fwdctl install
```

## fwdctl install agents

add a managed skills section to an AGENTS.md / CLAUDE.md style file (stdout without --file)

```
fwdctl install agents [flags]
```

Examples:

```
  fwdctl install agents --file AGENTS.md
```

Flags:

```
      --file string   the file to add the section to (default: print it)
```

## fwdctl install claude

write the skills where Claude Code loads them (default ~/.claude/skills)

```
fwdctl install claude [flags]
```

Examples:

```
  fwdctl install claude
```

Flags:

```
      --dir string   directory to write the skills into (default ~/.claude/skills)
```

## fwdctl list

Prints every skill as JSON: name, description, input_schema, class (read or write), runnable.

```
fwdctl list
```

Examples:

```
  fwdctl list | jq -r '.[] | select(.class=="write") | .name'
```

## fwdctl login

Remember where the login is, so every later fwdctl run (and every agent session that runs it) works with no environment.
TOKENFILE has three lines: the Forward URL, the username (or an API token's access key) and the password (or its secret), and must be mode 600.
The login is checked, then only the PATH of the file is saved in ~/.config/fwdctl/config.json; the password is never copied. Flags and FORWARD_* variables still win.

```
fwdctl login [flags]
```

Examples:

```
  fwdctl login --file ~/customer.token
  fwdctl login --forget
```

Flags:

```
      --file string   a token file: URL, username and password on three lines (mode 600)
      --forget        remove the saved login
```

## fwdctl nqe

NQE (Network Query Engine) tools. Offline, no Forward connection: lint, fmt, lsp, complete, hover, template. Connected: run, bundle, synthesize.
Before a connected command: FORWARD_URL, FORWARD_USERNAME, FORWARD_PASSWORD (or fwdctl login). Long queries: FORWARD_TIMEOUT, FORWARD_NQE_MODE, FORWARD_NQE_WAIT (fwdctl docs troubleshooting).

```
fwdctl nqe
```

## fwdctl nqe bundle

Print ONE self-contained query: the entry and every library module it imports at a commit, names prefixed per module, local files substituted. Inline query text always imports
against the library head and a commit cannot be combined with inline text, so testing an older or an uncommitted version of a module needs this. An --override of the entry's own
path replaces the entry; an --override or --add-module the bundle never reads is an error. Run the result with: fwdctl nqe run --network ID --file FILE

```
fwdctl nqe bundle [flags]
```

Examples:

```
  fwdctl nqe bundle --path "/Team/Entry" --commit-id 9f3c --out pre.nqe
  fwdctl nqe bundle --query-id Q_abc --override "/Team/Helpers=helpers.nqe" --out post.nqe
```

Flags:

```
      --add-module stringArray   LIBRARY_PATH=FILE: a module that exists only locally, importable by the entry or an override; repeatable
      --commit-id string         the library commit to read modules at (default: the head)
      --out string               write the bundle to this file (default: stdout)
      --override stringArray     LIBRARY_PATH=FILE: use this local file instead of the module (or the entry) at that path; repeatable
      --path string              the entry query, by library path
      --query-id string          the entry query, by id
```

## fwdctl nqe complete

what could be written at a position (1-based line and column)

```
fwdctl nqe complete FILE|- LINE COL
```

Examples:

```
  fwdctl nqe complete query.nqe 3 12
```

## fwdctl nqe fmt

Lay NQE out in the standard style. With no file it filters stdin to stdout. -w rewrites the files; --check prints the files that would change and exits 1 if any would.

```
fwdctl nqe fmt [FILE...] [flags]
```

Examples:

```
  fwdctl nqe fmt -w queries/*.nqe
  fwdctl nqe fmt --check queries/*.nqe
```

Flags:

```
      --check   list the files that would change and exit 1 if any would
  -w, --write   rewrite the files in place
```

## fwdctl nqe hover

what is at a position: its type and documentation (1-based line and column)

```
fwdctl nqe hover FILE|- LINE COL
```

Examples:

```
  fwdctl nqe hover query.nqe 3 12
```

## fwdctl nqe lint

Offline NQE check, no Forward connection: syntax errors with line and column, unknown names, wrong argument counts, fields and enum values the data model does not have,
type errors, and deprecations with Forward's own advice. Exit 1 on an error. The type check is gradual (it says nothing where it cannot tell a type), so
validate-nqe-query, which runs the query on Forward, is still the last word. An import of your own organization's saved query (not @fwd/...) warns rather than being
checked, since that library is per-organization and not sealed into this binary: `fwdctl nqe bundle` first for full coverage of it too.

```
fwdctl nqe lint [FILE|-] [flags]
```

Examples:

```
  fwdctl nqe lint query.nqe
  cat query.nqe | fwdctl nqe lint -
```

Flags:

```
      --modules string     where "import" statements are read from (default: the directory of FILE)
      --synthetic string   check the file as a synthetic-device query of this kind (adjacent-network|internet|intranet|l2vpn|l3vpn)
```

## fwdctl nqe lsp

A language server for NQE over stdin/stdout (diagnostics, completion, hover, quick fixes), for an editor to launch.

```
fwdctl nqe lsp
```

Examples:

```
  fwdctl nqe lsp   # an editor launches this over stdin/stdout
```

## fwdctl nqe run

Run an NQE query on Forward and print the rows (stdout), with timing and scope on stderr. Every row is fetched, paged for you (up to --max), or with --limit/--offset ONE page.
A saved library query runs by id at a commit. --async uses Forward's asynchronous execution API, --meta records how the run went. A synchronous call that is cut off by the HTTP
timeout falls back to the asynchronous API by itself (FORWARD_NQE_MODE=sync turns that off, =async always uses it; FORWARD_NQE_WAIT bounds the wait).
Exit status: 0 ok, 1 the query does not compile, 2 no processed snapshot, 3 error, 64 bad usage.

```
fwdctl nqe run [flags]
```

Examples:

```
  fwdctl nqe run --network 123 --file q.nqe --format table
  fwdctl nqe run --network 123 --query-id Q_abc --commit-id 9f3c --param threshold=10 --meta run.json
  fwdctl nqe run --network 123 --file q.nqe --offset 200 --limit 100 --meta page.json
```

Flags:

```
      --async               run through Forward's asynchronous execution API (the execution key and outcome are in --meta)
      --commit-id string    with --query-id: the library commit to run it at (default: the head)
      --count-by string     print how many rows have each value of this field instead of the rows
      --file string         file with the NQE query (default: stdin)
      --format string       json, jsonl, table or csv (default "json")
      --limit int           read one page of at most this many rows instead of every row (0: every row, up to --max)
      --max int             stop after this many rows (stated on stderr when more exist) (default 50000)
      --meta string         write a JSON object about the run (mode, execution key, outcome, Forward's execution time, rows, HTTP status and diagnostics on failure) to this file, or - for stderr
      --network string      network id (required)
      --offset int          read one page: skip this many rows (with --limit; the page, the total and the offset are in --meta)
      --param stringArray   one parameter as NAME=JSON (repeatable; a value that is not JSON is a string), overrides --params
      --params string       JSON file with the query's parameters, an object of name to typed value
      --query-id string     run a saved query by id instead of a file (a library query: see fwdctl run find-nqe-query)
      --snapshot string     snapshot id (default: the latest processed)
      --timeout duration    how long to wait: the whole synchronous request (response included; the default HTTP limit is 120s), or with --async the execution (default 10m0s)
```

## fwdctl nqe synthesize

Derive a synthetic-device query from evidence in the network model, lint it and print it; exit 1 if its own output is not clean.

```
fwdctl nqe synthesize
```

## fwdctl nqe synthesize internet

Derive an internet node's connection query from the model (the inspect-edge analysis), lint it and print it; exit 1 if its own output is not clean.

```
fwdctl nqe synthesize internet [flags]
```

Examples:

```
  fwdctl nqe synthesize internet --network 123 --vrf default
```

Flags:

```
      --device string      only default routes on this device
      --discovery string   interfaceAddresses, bgpRoutes, ipRoutes or none (default "interfaceAddresses")
      --include-unlikely   also write rows for unowned exits that are not likely internet edges
      --network string     network id (required)
      --snapshot string    snapshot id (default: the latest processed)
      --subnets string     comma-separated prefixes written on every row (required for --discovery none)
      --vrf string         only default routes in this VRF
```

## fwdctl nqe template

Print a starter query for a synthetic device (the CLI's "add new query from template").

```
fwdctl nqe template KIND
```

Examples:

```
  fwdctl nqe template internet > internet.nqe
```

## fwdctl run

Run a skill. Reads the skill's inputs as a JSON object from --input FILE or stdin and prints the result envelope (status, evidence, limits, next actions).
Exit status: 0 ok, 1 failed, 2 unknown (never a pass), 3 error, 64 bad usage. `fwdctl run <skill> --help` shows that skill's inputs and an example.

```
fwdctl run <skill> [flags]
```

Examples:

```
  echo '{}' | fwdctl run inspect-networks
  echo '{"network_id": "123"}' | fwdctl run inspect-snapshots
  fwdctl run edit-org-property --input plan.json
```

Flags:

```
      --format string   json (the whole result), or table or csv (the largest list of rows in the evidence) (default "json")
      --input string    JSON file with the skill inputs (default: stdin)
      --ops             include the log of Forward calls the skill made (audit data; omitted by default to save tokens)
```

## fwdctl update

Replaces this binary with the newest release after checking it against the release's SHA256SUMS. --check only reports (exit 2 when an update
exists). A private repository needs GITHUB_TOKEN. FWDCTL_AUTO_UPDATE=1 applies updates by itself; FWDCTL_NO_UPDATE_CHECK=1 silences the daily notice.
A Homebrew install is updated with: brew upgrade fwdctl.

```
fwdctl update [flags]
```

Examples:

```
  fwdctl update --check
  fwdctl update
```

Flags:

```
      --check            only report whether a newer release exists (exit 2 when one does)
      --force            install even when it is not newer, or replace a development build
      --version string   install this release (for example v0.5.48) instead of the newest
```

## fwdctl version

the release this binary was built from

```
fwdctl version
```

Examples:

```
  fwdctl version
```

## fwdctl which

Ranks the rows of the router table (plan-investigation) against the question and prints the best skills and playbooks, with the row that matched.
Offline, no model: a first guess. `fwdctl describe plan-investigation` is the full table.

```
fwdctl which <question>
```

Examples:

```
  fwdctl which "why can't host A reach host B"
```

## fwdctl whoami

Prints the Forward URL, the login, the organization and the Forward version this connects with (one request each); exit 3 if the login does not work.

```
fwdctl whoami
```

Examples:

```
  fwdctl whoami
```

## fwdctl completion

Prints a shell completion script: `source <(fwdctl completion bash)`, or for zsh, fish and PowerShell the line in the script's first comment (`fwdctl completion zsh --help`). It completes commands, skill names, flag values, and with a working login `--network` and `--snapshot`.
