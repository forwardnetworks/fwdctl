# fwdctl command reference

Generated from the command tree (`fwdctl <command> --help` shows the same for one command). Every command answers `--help`.

Connection flags work on every command that talks to Forward:

```
      --ca-bundle string            PEM file of the certificate authorities to trust for Forward's certificate, for a private or self-signed CA (overrides FORWARD_CA_BUNDLE; verification stays on; not with --insecure)
      --config string               connection config file (default ~/.config/fwdctl/config.json)
      --insecure                    do NOT verify the TLS certificate (a self-signed Forward; every result records it)
      --password-file string        file holding the password or token secret, mode 600 (overrides FORWARD_PASSWORD)
      --profile string              use a named saved connection (fwdctl login --profile NAME --file TOKENFILE), so switching between tenants is a name on every command (and each run prints the Forward and login it used on stderr)
      --support-token-file string   a second token file (same format) for a Forward-support login: used only by changes that need support authority, such as edit-org-property with org_id; the main login is used for everything else
      --token-file string           a token file in netrc format (machine HOST, login USER, password SECRET), mode 600
      --url string                  Forward URL (overrides FORWARD_URL)
      --username string             login name or API token access key (overrides FORWARD_USERNAME)
```

Exit status: 0 ok, 1 failed (a finding), 2 unknown (never a pass), 3 error, 64 bad usage or input.

## fwdctl collector

Downloads Forward's collector software with the logged-in credential, so a new collector host needs no browser. Registering the collector (its username and key) and attaching it to a network are separate steps: Settings > Collectors in Forward, then `fwdctl run edit-network` with object collector action assign.

```
fwdctl collector
```

Examples:

```
  fwdctl collector download --out ~/collector
```

## fwdctl collector download

Streams the collector installer for --type (linux: fwd-unix-VERSION.sh, windows: .exe, osx: .dmg; default linux; headless-linux and headless-windows are the archives for a manual install) from the connected Forward into --out (default the current directory), named as Forward names it. It needs a user login or API token: a collector's own credential is refused (403). An existing file is not overwritten without --force. The last line on stdout is one JSON object: file, bytes, sha256, type, forward_release. Compare the sha256 with the one Forward publishes before you run the package.

```
fwdctl collector download [--type linux] [--out DIR] [flags]
```

Examples:

```
  fwdctl collector download --out ~/collector
  fwdctl collector download --type windows --out ./dl
```

Flags:

```
      --force              overwrite a file of the same name
      --out string         directory to write the package into (created if missing) (default ".")
      --timeout duration   give up after this long (default 30m0s)
      --type string        package: linux (.sh installer), windows (.exe), osx (.dmg), headless-linux (.tar.gz) or headless-windows (.zip) (default "linux")
```

## fwdctl context

NQE authoring aids: worked examples closest to a question, or real field names matching a term.

```
fwdctl context
```

Examples:

```
  fwdctl context nqe "interfaces that are down"
  fwdctl context schema vrf
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

real NQE field names (and enum values) matching a term; a record is expanded one level

```
fwdctl context schema <term> [flags]
```

Examples:

```
  fwdctl context schema vrf
```

Flags:

```
      --features fwdctl nqe features   turn on NQE feature flags for the offline schema, as an organization with those org properties has them (comma list or repeat; names or Forward's org property names; all, none). Forward's defaults apply otherwise (the security-rules features that give device.securityPolicy are EXPERIMENTAL and off by default). List them with fwdctl nqe features. Also FORWARD_NQE_FEATURES
  -k, --top int                        how many to return (default 3)
```

## fwdctl describe

Prints one name's full procedure: what it answers, its inputs, how to read the result and its limits. Its reference files (see "references") print as plain text when named.

```
fwdctl describe NAME [reference-file]
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

Install the skills for an agent: into Claude Code's skills directory, or as a managed section of an AGENTS.md / CLAUDE.md file. `install lsp` sets up the NQE language server for Claude Code without the skills.

```
fwdctl install
```

Examples:

```
  fwdctl install claude
  fwdctl install agents --file AGENTS.md
  fwdctl install lsp
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

## fwdctl install lsp

Write a small local Claude Code marketplace (default ~/.claude/plugins-local) holding one plugin that declares `fwdctl nqe lsp` as the language server for .nqe files. Claude Code takes a language server only from a plugin. This writes files only: it prints the two `claude plugin` commands to run, and does not run them. fwdctl must be on PATH; no login is needed. The forward-skills plugin already includes the server, so this is for using it without the skills.

```
fwdctl install lsp [flags]
```

Examples:

```
  fwdctl install lsp
  fwdctl install lsp --dir ~/plugins
```

Flags:

```
      --dir string   directory to write the marketplace into (default ~/.claude/plugins-local)
```

## fwdctl list

Prints every runnable name as JSON: name, description, input_schema, class (read or write), effect, secrets, runnable. `fwdctl run` with no name prints the same as a short readable menu.

```
fwdctl list
```

Examples:

```
  fwdctl list | jq -r '.[] | select(.class=="write") | .name'
```

## fwdctl login

Remember where the login is, so every later fwdctl run (and every agent session that runs it) works with no environment.
TOKENFILE is in netrc format (machine HOST, login USER, password SECRET; the username may be an API token's access key and the password its secret) and must be mode 600.
The login is checked, then only the PATH of the file is saved in ~/.config/fwdctl/config.json; the password is never copied. Flags and FORWARD_* variables still win.

```
fwdctl login [flags]
```

Examples:

```
  fwdctl login --file ~/customer.netrc
  fwdctl login --profile staging --file ~/staging.netrc   # then: fwdctl --profile staging whoami
  fwdctl login --forget
```

Flags:

```
      --file string   a token file in netrc format: machine HOST, login USER, password SECRET (mode 600)
      --forget        remove the saved login
```

## fwdctl nqe

NQE (Network Query Engine) tools. Write NQE with real field names: fwdctl context schema NAME, then fwdctl nqe lint FILE, in any language. Offline, no Forward connection: lint, fmt, lsp, complete, hover, template. Connected: run, bundle, synthesize.
Before a connected command: FORWARD_URL, FORWARD_USERNAME, FORWARD_PASSWORD (or fwdctl login). Long queries: FORWARD_TIMEOUT, FORWARD_NQE_MODE, FORWARD_NQE_WAIT (fwdctl docs troubleshooting).

```
fwdctl nqe
```

Examples:

```
  fwdctl nqe lint query.nqe
  fwdctl nqe run --network 123 --file query.nqe --format table
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

## fwdctl nqe compare

Runs version A and version B (each --a/--b FILE, or --a-query-id/--b-query-id with an optional commit) on the same snapshot with the same parameters, and reports: whether the rows are identical (row order is
ignored, array order inside a row counts), the rows added, removed and changed by --key (without --key, rows are compared as a set), a shape section (columns only in one side, columns whose JSON types differ),
the number of value differences that remain when array order is ignored, and the timing of every run (execution key, millis executing, queued, wall, rows, bytes, whether Forward served it from cache).
File queries are made cold: an always-true filter on a unique constant is added so Forward's result cache sees a new program each run (--warm turns that off; a saved query cannot be made cold). Runs alternate
A,B then B,A for --rounds 2, so a slow first run is not charged to one side. A and B being the same text is refused: nothing would be compared.
Exit status: 0 identical (with --ignore-array-order: identical apart from array order), 1 they differ, 64 bad usage or a key that is not unique, 3 error. Each run is a real execution and can be slow on a large network.

```
fwdctl nqe compare [flags]
```

Examples:

```
  fwdctl nqe compare --network 123 --snapshot 456 --a old.nqe --b new.nqe --key device,rule --params p.json --out diffs.jsonl
```

Flags:

```
      --a string             version A: a query file
      --a-commit-id string   the commit of saved query A (default HEAD)
      --a-query-id string    version A: a saved query id
      --b string             version B: a query file
      --b-commit-id string   the commit of saved query B (default HEAD)
      --b-query-id string    version B: a saved query id
      --ignore-array-order   treat arrays that differ only in element order as equal (and exit 0 then)
      --key string           the column(s) that identify a row, comma separated (without it rows are compared as a set)
      --max int              refuse a result of more rows than this (default 200000)
      --network string       network id
      --out string           write every difference to this file as JSON lines
      --param stringArray    a query parameter NAME=VALUE (repeatable)
      --params string        a JSON file of query parameters (used for both versions)
      --rounds int           runs of each version; with 2 or more the order alternates (A,B,B,A) so neither side always runs first (default 1)
      --show int             list at most this many of each kind in the summary (default 20)
      --snapshot string      snapshot id (default: the newest processed)
      --warm                 do not add the cache-busting filter to file queries (a cached result then makes a run look fast)
```

## fwdctl nqe complete

what could be written at a position (1-based line and column)

```
fwdctl nqe complete FILE|- LINE COL [flags]
```

Examples:

```
  fwdctl nqe complete query.nqe 3 12
```

Flags:

```
      --experimental fwdctl nqe features   turn on fwdctl's experimental lint rules (comma list or repeat; all). They only warn, and each rests on a measurement: see fwdctl nqe features. Also FORWARD_NQE_EXPERIMENTAL
      --features fwdctl nqe features       turn on NQE feature flags for the offline schema, as an organization with those org properties has them (comma list or repeat; names or Forward's org property names; all, none). Forward's defaults apply otherwise (the security-rules features that give device.securityPolicy are EXPERIMENTAL and off by default). List them with fwdctl nqe features. Also FORWARD_NQE_FEATURES
```

## fwdctl nqe diff

Runs the query (--file or stdin) on --before-snapshot and --after-snapshot and compares the rows by the --key columns: which keys were added, which removed, which changed (and which columns). The key columns must
identify a row uniquely in each snapshot (a repeated key is refused, since the diff would be ambiguous). Counts are exact; the stdout JSON lists up to --show of each kind; --out writes every difference as
JSON lines {change, key, before, after, changed_columns}. A list column (next hops, interfaces, tags) is compared in the order Forward returned it and NQE does not order such lists, so the same list in a different order shows as a changed row (one field session saw 15,000 false differences on forwarding entries): order the list inside the query before diffing (`nqe compare` has --ignore-array-order; this command does not). If a result is larger than --max rows the diff is refused rather than reported on a partial read.
Exit status: 0 no differences, 1 differences found (0 with --exit-zero), 64 bad usage or a key that is not unique, 3 error. Runs are asynchronous by default; for a large result run the query alone first (`nqe run --limit 1 --meta`) to see its size.

```
fwdctl nqe diff [flags]
```

Examples:

```
  fwdctl nqe diff --network 123 --before-snapshot 111 --after-snapshot 222 --file rules.nqe --key device,ruleId --out changes.jsonl
```

Flags:

```
      --after-snapshot string    the later snapshot id
      --before-snapshot string   the earlier snapshot id
      --exit-zero                exit 0 when differences are found (for scripts); errors and bad usage still fail
      --file string              the query (default: stdin)
      --key string               the column(s) that identify a row, comma separated
      --max int                  refuse a result of more rows than this in either snapshot (default 200000)
      --network string           network id
      --out string               write every difference to this file as JSON lines
      --show int                 list at most this many of each kind in the summary (default 20)
```

## fwdctl nqe explain

Prints the optimized plan Forward builds for a saved library query, as the Query Debug Panel in the NQE Library shows it: a device-parallel query starts `parallel_foreach`, a join that became a lookup shows
`_lookup(...)`, and `⸢ ⸣` marks expressions found total (cannot raise an error, so a `where` may float up), `⟪ ⟫` marks ONCE cardinality. Read it to see why a query is slow.
Give --query-id (optionally --commit-id; HEAD otherwise) or --path (a query in your own workspace, as the UI writes it, for example "/Operations/my query"). Raw query text is not explained by Forward: save it
as a draft first, or give --file: it saves the text as a throwaway draft (dry run unless --apply), explains it and discards the draft, naming the path it wrote. Internal Forward routes (preview); needs the organization's VIEW_NQE_LIBRARY permission. A query with errors is refused with its diagnostics. After the plan, stderr says how many `_lookup`s each collection got (or that there is none), so a rewrite that did not produce a lookup is seen at once.
For the execution time use `fwdctl nqe run --meta`; for slow runs Forward recorded, `fwdctl nqe metrics`.

```
fwdctl nqe explain [flags]
```

Examples:

```
  fwdctl nqe explain --query-id Q_0123456789abcdef0123456789abcdef01234567 --network 123
  fwdctl nqe explain --path "/Operations/my query" --param threshold=3
  fwdctl nqe explain --file bundle.nqe --apply
```

Flags:

```
      --apply               with --file: really write the draft (without it, only say what would be done)
      --commit-id string    the commit of the saved query (default HEAD)
      --draft-path string   with --file: the draft's path (default /fwdctl-explain-<hash of the text>); an existing draft there is never replaced
      --file string         a file of query text (a bundle works): saved as a throwaway workspace draft, explained, then discarded
      --network string      network id (its newest processed snapshot supplies the schema; optional)
      --param stringArray   a query parameter NAME=VALUE (repeatable)
      --params string       a JSON file of query parameters
      --path string         a query in your own NQE workspace, without the repository, for example "/Operations/my query"
      --query-id string     a saved library query's id
      --snapshot string     snapshot id to take the schema from (optional)
```

## fwdctl nqe export

Write the entry query and every organization library module it imports, transitively, to a folder tree: one <query name>.nqe per query under the library path
(/Team/Sub/Mod is DIR/Team/Sub/Mod.nqe), import statements left exactly as they are (they already name library paths, so the tree re-imports as it is), and manifest.json with each
file's library path, query id, commit id, sha256 and imports. Unlike bundle it inlines nothing. The tree and --zip are fwdctl's own format: the Forward UI's library import refuses them
("missing file queries-export.proto"); --ui-zip writes the package the UI's Export produces and Import reads (checked against a package Forward itself exported: the same queries with identical sources; Forward lists them in an arbitrary order, so two exports differ in order only). Imports of @fwd/... are listed, not fetched. The data files the queries read
(network.extensions.<name>) are reported in the manifest and on stderr: they are organization uploads, not part of the export. --override and --add-module behave as in bundle, and
one that matches nothing is an error. DIR must be empty (or pass --force); --zip also writes the same files as a zip.

```
fwdctl nqe export [flags]
```

Examples:

```
  fwdctl nqe export --path "/Team/Entry" --commit-id 9f3c --out export/
  fwdctl nqe export --query-id Q_abc --out export/ --zip export.zip
  fwdctl nqe export --query-id Q_abc --out export/ --ui-zip for-the-ui.zip   # what the Forward UI imports
```

Flags:

```
      --add-module stringArray   LIBRARY_PATH=FILE: a module that exists only locally; repeatable
      --commit-id string         the library commit to read at (default: the head, recorded in the manifest)
      --force                    write into a directory that is not empty
      --out string               the directory to write (required)
      --override stringArray     LIBRARY_PATH=FILE: use this local file instead of the module (or the entry) at that path; repeatable
      --path string              the entry query, by library path
      --query-id string          the entry query, by id (its library path is read from the head listing)
      --ui-zip string            also write the queries in the Forward UI's library import format (a zip holding queries-export.proto)
      --zip string               also write the tree and manifest as this zip (fwdctl's own format: the Forward UI cannot import it)
```

## fwdctl nqe features

A feature flag is an org property that switches a change in the model or the data on: setting it enables the fields, types or functions listed against it, and an organization without it does not have them. Forward serves each organization an NQE schema filtered by its feature flags. The offline checker uses Forward's defaults; --features turns flags on, as an organization with them would have, so a query using a field behind a flag checks clean. The Palo Alto and Fortinet security policy features (security-rules-panos and security-rules-fortios, org properties NQE_SECURITY_RULES_PANOS and NQE_SECURITY_RULES_FORTIOS) are EXPERIMENTAL in Forward, still so in 26.10, and off by default: device.securityPolicy exists only in an organization that has one of them turned on, so offline you need --features for it, or `nqe lint --org`. `nqe lint --org` reads the organization's own live schema and is the authority for one organization. Kinds: schema (fields and types), language (a function), runtime (storage and summaries: no effect on the checker).

```
fwdctl nqe features [flags]
```

Examples:

```
  fwdctl nqe features
  fwdctl nqe features --features security-rules-fortios,device-services
```

Flags:

```
      --experimental fwdctl nqe features   turn on fwdctl's experimental lint rules (comma list or repeat; all). They only warn, and each rests on a measurement: see fwdctl nqe features. Also FORWARD_NQE_EXPERIMENTAL
      --features fwdctl nqe features       turn on NQE feature flags for the offline schema, as an organization with those org properties has them (comma list or repeat; names or Forward's org property names; all, none). Forward's defaults apply otherwise (the security-rules features that give device.securityPolicy are EXPERIMENTAL and off by default). List them with fwdctl nqe features. Also FORWARD_NQE_FEATURES
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
fwdctl nqe hover FILE|- LINE COL [flags]
```

Examples:

```
  fwdctl nqe hover query.nqe 3 12
```

Flags:

```
      --experimental fwdctl nqe features   turn on fwdctl's experimental lint rules (comma list or repeat; all). They only warn, and each rests on a measurement: see fwdctl nqe features. Also FORWARD_NQE_EXPERIMENTAL
      --features fwdctl nqe features       turn on NQE feature flags for the offline schema, as an organization with those org properties has them (comma list or repeat; names or Forward's org property names; all, none). Forward's defaults apply otherwise (the security-rules features that give device.securityPolicy are EXPERIMENTAL and off by default). List them with fwdctl nqe features. Also FORWARD_NQE_FEATURES
```

## fwdctl nqe lint

Offline NQE check, no Forward connection: syntax errors with line and column, unknown names, wrong argument counts, fields and enum values the data model does not have,
type errors, and deprecations with Forward's own advice. Exit 1 on an error. The type check is gradual (it says nothing where it cannot tell a type), so
validate-nqe-query, which runs the query on Forward, is still the last word. An import of your own organization's saved query (not @fwd/...) warns rather than being
checked, since that library is per-organization and not sealed into this binary: `fwdctl nqe bundle` first for full coverage of it too.
Dead code is warned about, never an error (exit stays 0): an import none of whose names is used (unused-import), a parameter or let nothing reads (unused-param, unused-let) and a definition nothing reachable from the @query, the main
expression or an export refers to (unused-definition). Lint a `nqe bundle` to find what a whole module tree never uses; an exported definition is never called dead, since
another module may import it.
Performance advice is reported as `info` (never an error, exit stays 0): the query will not run device-parallel (perf-device-parallel), a device filter that is not an exact name equality (perf-device-filter), a very large inline query (perf-inline-size), a function called per list element that rebuilds shared group trees (perf-repeated-tree-per-row), a device-level value bound inside a row loop (perf-device-value-in-row-loop), a list held as a record field filtered by a field (perf-record-field-not-indexed), a large function called from three or more places (perf-call-site-expansion). The experimental rule perf-catalog-outer-join (two bare lists joined by a field: the smaller should be the outer) is off unless asked for with --experimental, because it cannot tell which list is larger and flags both forms. See `fwdctl describe author-nqe-query reference/performance.md`.
The embedded schema is a release's; an organization on a newer build has fields it lacks (and may have dropped fields it still has). --org reads the organization's live schema
(GET /api/nqe/schema; needs a login) and checks against that, so a field the organization no longer has is reported as an error, and the result says how many field paths the live schema
has that the embedded one lacks and the reverse (schema.source, fields_added, fields_removed).

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
      --errors-only                        report errors only, no warnings or deprecations (the exit status is the same)
      --experimental fwdctl nqe features   turn on fwdctl's experimental lint rules (comma list or repeat; all). They only warn, and each rests on a measurement: see fwdctl nqe features. Also FORWARD_NQE_EXPERIMENTAL
      --features fwdctl nqe features       turn on NQE feature flags for the offline schema, as an organization with those org properties has them (comma list or repeat; names or Forward's org property names; all, none). Forward's defaults apply otherwise (the security-rules features that give device.securityPolicy are EXPERIMENTAL and off by default). List them with fwdctl nqe features. Also FORWARD_NQE_FEATURES
      --modules string                     where "import" statements are read from (default: the directory of FILE)
      --org                                check against the organization's live schema instead of the embedded one (needs a login)
      --synthetic string                   check the file as a synthetic-device query of this kind (adjacent-network|internet|intranet|l2vpn|l3vpn)
```

## fwdctl nqe lsp

A language server for NQE over stdin/stdout (diagnostics, completion, hover, quick fixes), for an editor to launch.

```
fwdctl nqe lsp [flags]
```

Examples:

```
  fwdctl nqe lsp   # an editor launches this over stdin/stdout
```

Flags:

```
      --experimental fwdctl nqe features   turn on fwdctl's experimental lint rules (comma list or repeat; all). They only warn, and each rests on a measurement: see fwdctl nqe features. Also FORWARD_NQE_EXPERIMENTAL
      --features fwdctl nqe features       turn on NQE feature flags for the offline schema, as an organization with those org properties has them (comma list or repeat; names or Forward's org property names; all, none). Forward's defaults apply otherwise (the security-rules features that give device.securityPolicy are EXPERIMENTAL and off by default). List them with fwdctl nqe features. Also FORWARD_NQE_FEATURES
```

## fwdctl nqe metrics

Lists NQE runs Forward recorded with their run duration (execution only, not queue time), slowest first as Forward returns them: task, network, snapshot, saved query id and commit when it was a saved query,
when it ran. Defaults are Forward's: the last 7 days, runs of at least 10 seconds, at most 100. --min-seconds 0 lists every run. This is a Forward debug feature (organization permission DEBUG_NQE), which most
logins lack; the error names it. Use it to find which queries are expensive; `fwdctl nqe explain` shows why.

```
fwdctl nqe metrics [flags]
```

Examples:

```
  fwdctl nqe metrics --min-seconds 30 --limit 20
```

Flags:

```
      --from string       start of the window, RFC 3339 (default: 7 days ago)
      --limit int         at most this many runs (default 100)
      --min-seconds int   only runs of at least this many seconds (0: every run) (default 10)
      --to string         end of the window, RFC 3339 (default: now)
```

## fwdctl nqe pack

Read DIR (as nqe export writes it: <library path>.nqe files, with manifest.json when there is one) and print the edit-nqe-query input that commits the tree in ONE commit:
{"changes": [{"path", "source"}...]}. It only reads the directory and prints JSON; the write is edit-nqe-query's own dry run, so pipe the file to "fwdctl run edit-nqe-query", read the plan,
then add "apply": true. With a manifest, stderr says which files changed since the export, which are new and which are gone (pack never deletes), and --changed-only sends just the edited
ones. A zip in the Forward UI's export format (it holds queries-export.proto) is read too, so a package the UI's Export produced can be committed with fwdctl; it has no manifest, so there is no change report. --create-directory makes missing library folders in the same commit (needed to load a tree into a library that lacks them); --typecheck has Forward type every query and every
importer first; --basis-commit-id C (or "manifest" for the export's commit) refuses the commit if the library head is not C. A commit carries at most 25 queries.

```
fwdctl nqe pack DIR|UI.zip [flags]
```

Examples:

```
  fwdctl nqe pack tree/ --message "Load parser modules" --create-directory --typecheck > load.json
  fwdctl run edit-nqe-query < load.json                       # the dry run: the plan, the lint, Forward's typecheck
  fwdctl nqe pack tree/ --changed-only --basis-commit-id manifest > edits.json   # push only what was edited since the export
```

Flags:

```
      --basis-commit-id string   refuse to commit if the library head is not this commit ("manifest": the commit the tree was exported at)
      --changed-only             send only files that differ from manifest.json or are not in it
      --create-directory         create missing library directories in the same commit
      --message string           the commit title
      --typecheck                have Forward typecheck the changed queries and their importers before committing
```

## fwdctl nqe run

Run an NQE query on Forward and print the rows (stdout), with timing and scope on stderr. Every row is fetched, paged for you (up to --max), or with --limit/--offset ONE page.
A saved library query runs by id at a commit. Runs are ASYNCHRONOUS by default (Forward's execution API: a query's duration and size cannot be known beforehand, and a polled execution holds no connection open, so there is no gateway timeout; it costs a few hundred milliseconds on a small query). --sync, or FORWARD_NQE_MODE=sync, runs one synchronous request instead; FORWARD_NQE_MODE=syncfirst is the older sync-then-async-on-timeout; FORWARD_NQE_WAIT bounds the wait.
--meta records how the run went: the execution key, outcome, Forward's execution time (millis_executing), the time spent queued (queued_millis, measured here) and whether the result came from Forward's cache, response_bytes (the bytes of rows the run wrote) and a hint when an inline query waited long in the queue (Forward analyses an inline program afresh; a saved query run by id caches that). A failed run served from the cache says so: repeating the same request cannot succeed until the query text or parameters change (--fresh does that). Over ~100 KB sent inline, save the query and use --query-id.
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
      --allow-large           write a result of more than 100 MB (by default it is refused before anything is written, so a redirect cannot fill the disk)
      --async                 (the default) run through Forward's asynchronous execution API (the default; the execution key, outcome and times are in --meta)
      --commit-id string      with --query-id: the library commit to run it at (default: the head)
      --count-by string       print how many rows have each value of this field instead of the rows (counts every row: it reads past --max, up to 2,000,000, or refuses)
      --file string           file with the NQE query (default: stdin)
      --format string         json, jsonl, table or csv (default "json")
      --fresh                 EXPERIMENTAL: add an always-true filter after the first loop so Forward's result cache sees a new program (a cold timing, or a retry of a cached failure); rows are identical
      --limit int             read one page of at most this many rows instead of every row (0: every row, up to --max)
      --max int               stop after this many rows (stated on stderr when more exist) (default 50000)
      --meta string           write a JSON object about the run (mode, execution key, outcome, Forward's execution time, rows, HTTP status and diagnostics on failure) to this file, or - for stderr
      --network string        network id (required)
      --offset int            read one page: skip this many rows (with --limit; the page, the total and the offset are in --meta)
      --param stringArray     one parameter as NAME=JSON (repeatable; a value that is not JSON is a string), overrides --params
      --params string         JSON file with the query's parameters, an object of name to typed value
      --query-id string       run a saved query by id instead of a file (a library query: see fwdctl run find-nqe-query)
      --retry-transient int   retry up to N more times (waiting 5s, 10s, ... up to 30s) when Forward or its gateway answers 502, 503 or 504, for a long run that a Forward restart would kill; --meta records attempts and transport_clean
      --snapshot string       snapshot id (default: the latest processed)
      --sync                  run as one synchronous request instead (faster for a small query; a long one can be cut off by the gateway)
      --timeout duration      how long to wait: the whole synchronous request (response included; the default HTTP limit is 120s), or with --async the execution (default 10m0s)
```

## fwdctl nqe synthesize

Derive a synthetic-device query from evidence in the network model, lint it and print it; exit 1 if its own output is not clean.

```
fwdctl nqe synthesize
```

Examples:

```
  fwdctl nqe synthesize internet --network 123 --vrf default
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

Run one named analysis. Reads its inputs as a JSON object from --input FILE or stdin and prints the result envelope (status, evidence, limits, next actions).
With no name it lists every name and what it answers. Exit status: 0 ok, 1 failed, 2 unknown (never a pass), 3 error, 64 bad usage. `fwdctl run NAME --help` shows that name's inputs and an example.

```
fwdctl run [NAME] [flags]
```

Examples:

```
  echo '{}' | fwdctl run inspect-networks
  echo '{"network_id": "123"}' | fwdctl run inspect-snapshots
  fwdctl run edit-org-property --input plan.json
```

Flags:

```
      --format string   json (the whole result), or table or csv (the largest list of rows in the evidence, or the one --list names) (default "json")
      --input string    JSON file with the inputs (default: stdin)
      --list string     with --format table or csv: print this list (the key it sits under, such as by_vendor) instead of the largest; the others are named on stderr
      --ops             include the log of Forward calls the run made (audit data; omitted by default to save tokens)
      --quiet           with --format table or csv: print only the status line on stderr, not the limits and the other lists (the limits still matter: read them once)
```

## fwdctl source

Works on a whole source inventory at once instead of one edit-source call per item.

```
fwdctl source
```

Examples:

```
  fwdctl source apply lab.json            # dry run: what would be created or updated
  fwdctl source apply lab.json --apply
```

## fwdctl source apply

FILE is JSON: {"network_id", "endpoint_profiles":[{name, command_sets, custom_commands}], "credentials":[{name, type, kind, username, secret_env or secret_file}], "devices":[{name, host, type, port, credential, enable_credential, expert_credential, http_credential, snmp_credential, jump_server, collector_id, collect, note, enableSnmpCollection, collectBgpAdvertisements, bgpTableType, bgpPeerType, bgpAndBmpFailureHandling, disableIpv6Collection, fullCollectionLog}], "endpoints":[{name, type, host, port, protocol, credential, profile, jump_server, collect, collector_id}]}. Credentials and profiles are referred to by NAME and resolved to ids. A secret is never in the file: a credential names the environment variable (secret_env) or the mode-600 file (secret_file) that holds it.
Order: profiles, credentials, devices, endpoints. Each item is compared by name with what Forward holds: create, update, or unchanged. A dry run (the default) changes nothing. With --apply it stops at the first failure and marks the rest skipped, so a partial run is easy to read; run it again after fixing it, because unchanged items are left alone. Secrets are never compared, so a changed password is not detected (rotate with edit-source). A device's enable credential and collection options are compared with what Forward reports.
The last line on stdout is JSON: status, mode, network_id, counts, items (each {kind: profile|credential|device|endpoint, name, action: create|update|unchanged|failed|skipped, note}) and notes. Exit 0: done; 1: an item failed; 64: bad file; 3: Forward could not be read.

```
fwdctl source apply FILE [--network ID] [--apply] [flags]
```

Examples:

```
  fwdctl source apply lab.json --network 252606            # dry run: create / update / unchanged per item
  LAB_PW=... fwdctl source apply lab.json --network 252606 --apply
```

Flags:

```
      --apply            make the changes (default: a dry run)
      --network string   network id (overrides network_id in the file)
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

## fwdctl wait

Blocks, printing one progress line per poll on stderr, until Forward reaches the state asked for. For the work that takes an hour (a reprocess after a backdate, advanced
reachability) so it does not need a polling loop that dies with the session. Exit 0: reached. 1: Forward ended in a failed, canceled or timed-out state that will not change
by itself. 2: --timeout passed first (the work may still be running; run the wait again). 3: an error (bad input, Forward unreachable for several polls in a row).

```
fwdctl wait
```

Examples:

```
  fwdctl wait snapshot --network N --snapshot S --advanced-reachability --timeout 3h
```

## fwdctl wait collector

Polls the organization's collectors every --interval until the named one (its name, id or username) is CONNECTED. Use it after starting the collector software and before attaching the collector to a network (edit-network object collector action assign).
Exit 0: CONNECTED. 1: the collector no longer exists. 2: --timeout passed first. 3: an error (Forward unreachable for several polls in a row). The last line on stdout is a JSON object: status, collector, connection, version, waited_seconds. A collector that stays NEVER_CONNECTED has not authenticated: check the container's log and that its TOKEN is the registration key verbatim (it is already username:password).

```
fwdctl wait collector NAME_OR_USERNAME [--timeout 10m] [flags]
```

Examples:

```
  fwdctl wait collector lab --timeout 10m
```

Flags:

```
      --interval duration   time between polls (at least 1s) (default 10s)
      --timeout duration    give up after this long (exit 2) (default 10m0s)
```

## fwdctl wait snapshot

While the snapshot is PROCESSING, or its advanced reachability is (the ADVANCED_REACHABILITY stage), each poll line also says where it stands: the stage computing now, its object count, elapsed time against Forward's own estimate, and when elapsed is past the estimate (a stuck run looks like this). With --follow the progress goes to stdout like tail -f (a line when the state or stage changes, otherwise every --every), so a background run can be read from its log.
Polls the snapshot every --interval until it is PROCESSED and, with --advanced-reachability, its advanced reachability is PROCESSED too.
A FAILED, CANCELED or TIMED_OUT state ends the wait with exit 1: Forward does not retry those by itself (a reprocess clears them). The last line on stdout is a JSON object: status,
snapshot_id, state, advanced_reachability, waited_seconds. Typical durations on a 1,400-device network: processing about an hour, advanced reachability 15 to 30 minutes.
Nothing here starts the work: a reprocess or advanced reachability that was never started (edit-snapshot, edit-advanced-reachability) stays UNPROCESSED, and the wait says so after a few polls and ends at --timeout.

```
fwdctl wait snapshot --network ID --snapshot ID [--advanced-reachability] [--timeout 2h] [flags]
```

Examples:

```
  fwdctl wait snapshot --network N --snapshot S --advanced-reachability --timeout 3h
```

Flags:

```
      --advanced-reachability   also wait for the snapshot's advanced reachability to be PROCESSED
      --every duration          with --follow, the longest time between lines while nothing changes (default 1m0s)
      --follow                  stream progress on stdout like tail -f: a line when the state or stage changes and otherwise every --every, until the snapshot is PROCESSED, FAILED or the timeout; same exit codes and final JSON line; run it in the background and read the log
      --interval duration       time between polls (at least 1s) (default 30s)
      --network string          network id
      --snapshot string         snapshot id
      --timeout duration        give up after this long (exit 2) (default 2h0m0s)
```

## fwdctl which

Ranks the built-in question index against the question and prints the best names, with the row that matched.
Offline, no model: a first guess. `fwdctl run` lists every name with what it answers.

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
