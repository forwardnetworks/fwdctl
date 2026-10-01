# NQE: lint, look up, validate

NQE is Forward's query language. `fwdctl` has three tools for writing it, from fastest to most authoritative.

## 1. `fwdctl nqe lint` (offline, milliseconds)

    fwdctl nqe lint query.nqe
    cat query.nqe | fwdctl nqe lint
    fwdctl nqe lint -            # read stdin

No Forward connection, no login, no snapshot. It reads the query with a parser and type checker written for this tool and prints JSON:

    {
      "valid": false,
      "diagnostics": [
        {"severity": "error", "line": 2, "column": 14, "length": 4, "code": "type",
         "message": "Record does not have field: \"nmae\". Did you mean \"name\"?", "fix": "name"}
      ],
      "schema_hints": [],
      "note": "..."
    }

What it checks, and how it reports it (the wording follows Forward's own messages):

| Finding | Severity | Example |
|---|---|---|
| Syntax error, with 1-based line and column and what was expected | error | an unclosed `{`, a missing `select`, a bad regular expression |
| Unknown variable or function, wrong number of arguments, duplicate declarations, circular declarations | error | `Variable x not in scope.`, `length expects 1 argument, but was given 2 arguments.` |
| A field or enumeration value the data model does not have, with the closest real names (`fix`) | error | `Record does not have field: "nmae".`, `Unknown alternative CISC` |
| Type errors | error | comparing a String with an Integer, a bad argument type, `foreach` over something that is not a list, an `if` whose branches differ, the wrong top-level result type |
| Deprecated construct or field, with Forward's own advice | warning | the type `Number`; `Field 'uptimeSeconds' is deprecated. Use uptime instead.` |
| Bag/List migration warnings | warning | comparing a bag with a list; passing a bag where a function expects a list |
| Literals | error | regular expressions (nested quantifiers, bad ranges), config patterns, pattern forests, text blocks |

Exit status: 0 when there are no errors (warnings do not fail it), 1 on any error, 64 on bad usage.

**Imports.** `import "@fwd/L2/L2 Utilities";` names a query of Forward's built-in library; the checker knows what each library
module exports (names, parameter types, result types), so a misspelled or missing function, a wrong argument count or type, and a
wrong field on the result are reported, and the imported names are offered in completion and hover. Your own modules are read
from files next to the query (`import "helpers";` reads `helpers.nqe` in the same directory); give another directory with
`fwdctl nqe lint --modules DIR query.nqe`. The language server does the same for each open file. Also reported: a duplicate
import, an import cycle, and `Errors found in module 'm'` when an imported module has errors. A module found nowhere is not
guessed at: names that might come from it are not reported.

How far to trust it: the checker is gradual. Where it cannot tell a type (for example the capture types of a pattern),
it says nothing rather than guess, so a clean result is not proof that Forward will accept the query, but an
error it reports is one Forward reports too. Against Forward's own test suite it accepts all 909 queries Forward accepts, and
reproduces 467 of the 551 diagnostics Forward expects, word for word; on the 38 cases that import modules it accepts all and reproduces 31 of 38. `validate-nqe-query` asks Forward itself.

## 1b. In your editor: `fwdctl nqe lsp`

    fwdctl nqe lsp               # a language server on stdin and stdout

A standard language server (LSP) for files named `*.nqe`: diagnostics as you type, completion after a dot (the fields of the record
on its left, or the values of an enumeration) and in scope (variables, the standard library, keywords), hover (type, Forward's
documentation of a field, deprecation advice), quick fixes (the suggested name), go to definition, find references, rename (of a name the query defines), signature help
(the parameters of the function being called, with every overload of a library function), folding, the outline of declarations
(document symbols), workspace symbols, and whole-document formatting. Edits are synced incrementally, and the diagnostics of a
document are refreshed when a module it imports changes. It works on code that is unfinished at the cursor.
Point your editor's LSP client at that command for the file type. The Claude Code plugin in this repository does it for you:
it declares the server in `.lsp.json`, so Claude Code shows the diagnostics after it edits a `.nqe` file. `fwdctl` must be on `PATH`.

Other editors: Neovim `vim.lsp.config('fwd-nqe', { cmd = { 'fwdctl', 'nqe', 'lsp' }, filetypes = { 'nqe' } })` (and `vim.filetype.add({ extension = { nqe = 'nqe' } })`);
Helix `[language-server.fwd-nqe]` with `command = "fwdctl"`, `args = ["nqe", "lsp"]` and a language entry for `nqe`; VS Code through any generic LSP client
extension set to that command. Diagnostics, completion, hover, quick fixes, definition, references, symbols and formatting come from the same server.

Without an editor, completion and hover are commands (1-based line and column):

    fwdctl nqe complete query.nqe 2 14      # what can be written there
    fwdctl nqe hover query.nqe 2 12         # what is there

## 1c. Formatting: `fwdctl nqe fmt`

    fwdctl nqe fmt query.nqe             # print the formatted query
    fwdctl nqe fmt -w a.nqe b.nqe        # rewrite the files in place
    fwdctl nqe fmt --check *.nqe         # list the files that are not formatted, exit 1 if any (for CI)
    cat q.nqe | fwdctl nqe fmt           # stdin to stdout

The standard layout: lines of at most 80 columns, a two-space indent, one clause of a comprehension per line, records and calls broken one
item per line when they do not fit, redundant parentheses dropped, floats written `0.5` and `5.0`. Comments stay where they were written.
It reads the output back as the same query (checked on every query of Forward's library) and formatting twice changes nothing. A query with a
syntax error is not touched (exit 1).

## 2. `fwdctl context` (offline)

    fwdctl context schema "vendor"          # real field names and enum values matching a term
    fwdctl context nqe "devices missing ntp" -k 3   # the worked examples nearest a question

Never guess a field or enum value: look it up.

## 3. `validate-nqe-query` (online, authoritative)

    echo '{"network_id": "<id>", "query": "foreach d in network.devices select d.name"}' | fwdctl run validate-nqe-query

Runs the query on Forward against a snapshot: compile errors with positions, row count, a sample. Zero rows is `unknown`, not
"compliant". When the query fails to compile, the result also carries our own reading of it (`offline_lint`); when it runs, deprecation warnings appear in its `limits`.

## 3b. All the rows, as a table or CSV: `fwdctl nqe run`

    fwdctl nqe run --network <id> --file q.nqe --format table            # every row, paged for you (at most --max, default 50000)
    fwdctl nqe run --network <id> --file q.nqe --count-by vrf --format csv   # how many rows per value of a field
    fwdctl nqe run --network <id> --query-id Q_... --async --meta run.json   # execution key, outcome, Forward's timing and any diagnostics in run.json
    fwdctl nqe bundle --query-id Q_... --commit-id C --override /Lib/Mod=local.nqe > inline.nqe   # ONE query: the entry + every library module it imports at C, local files substituted
    fwdctl nqe run --network <id> --file inline.nqe --async --meta run.json
    fwdctl run inspect-edge --format table < input.json                  # any skill: the largest list of rows in its evidence as a table or CSV

`validate-nqe-query` returns a bounded sample (at most 200 rows) so a result stays small for an agent. `nqe run` is for a person or a script that wants all of them: it reads the query from `--file` or stdin, runs it on the latest processed
snapshot (or `--snapshot`), and prints JSON, JSON lines, a table or CSV. `--format table|csv` on `fwdctl run` prints the biggest list of rows in the result's evidence and puts the status, finding and limits on stderr.

## A good loop

1. `fwdctl context nqe "<the question>"` and `context schema "<a term>"` for real names and shape.
2. Write the query. `fwdctl nqe lint` after each edit until `valid`.
3. `validate-nqe-query` once, for the type check and the rows.
4. Read `fwdctl describe author-nqe-query` for the language rules and cheat sheet.

## Synthetic device queries

A synthetic device's connections can come from a saved NQE query (Forward's "add new query from template"):

    fwdctl nqe template l3vpn > connections.nqe          # internet | intranet | l3vpn | adjacent-network | l2vpn
    fwdctl nqe lint --synthetic l3vpn connections.nqe    # the query's rows must fit the kind's record: missing or mistyped columns, vlan 0

To derive an internet node's connections from the network model instead of writing them, run the `inspect-edge` analysis and print a query:

    fwdctl nqe synthesize internet --network ID [--vrf V] [--device D] [--discovery interfaceAddresses|bgpRoutes|ipRoutes|none] [--subnets CIDR,...] [--include-unlikely] [--snapshot ID] > connections.nqe

It writes one `InetConnection` row per (device, egress) of each likely internet edge (an unowned public next hop with an eBGP session to an unmodelled peer; `--include-unlikely` adds every unowned exit): `uplinkInterface` is the parent port
of a subinterface, `gatewayInterface` the subinterface, `vlan` the number after its dot (omitted, never 0, when there is none). `--discovery` defaults to `interfaceAddresses`; `ipRoutes` writes `advertisesDefaultRoute: false`; `bgpRoutes` takes the
`peerIps` from the unmodelled eBGP peers found and warns that the advertised-prefix counts are unreconciled; `none` is refused unless `--subnets` is given. The comment header names the snapshot and its time, each exit and why, what the discovery choice
means and every synthetic node that already claims the uplink (attaching the rows there too is a double claim, whose behaviour Forward does not document). The query is linted (`nqelint.Lint` and the internet row-type check) before it is printed;
output that is not clean goes to stderr with a non-zero exit. Check it with `fwdctl nqe lint --synthetic internet` and `validate-nqe-query` with `synthetic_kind: internet`.

`fwdctl run author-nqe-query` and `fwdctl describe author-nqe-query reference/synthetic-devices.md` have the row types and the mistakes Forward only reports after the query is attached.
