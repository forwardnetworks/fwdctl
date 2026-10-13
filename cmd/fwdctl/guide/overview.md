# fwdctl

`fwdctl` runs Forward Skills: questions about a Forward network, answered with evidence. One binary, no runtime, no model. It
reads Forward's digital twin through Forward's API and prints one JSON result per question.

## Set up

Give it a Forward login in the environment:

    export FORWARD_URL=https://fwd.app
    export FORWARD_USERNAME=<an API token's access key, or a login name>
    export FORWARD_PASSWORD=<its secret, or a password>

TLS is verified against the system trust store. For a self-signed Forward set `FORWARD_INSECURE=true`: it turns verification off,
prints a warning and every result records it.

## Getting started: five examples

Each one is a command you can run now. `--help` on any command or name shows its inputs and an example, so you rarely need more than this page.

    fwdctl whoami                                           # who you are connected as: URL, login, organization, Forward version
    echo '{}' | fwdctl run inspect-networks                 # 1. which networks can I see, and their ids
    fwdctl which "is CVE-2024-3400 relevant to us"          # 2. which name answers a question in words (offline)
    echo '{"network_id": "<id>"}' | fwdctl run inspect-vulnerabilities
    echo '{"network_id": "<id>", "kind": "summary"}' | fwdctl run inspect-inventory
    fwdctl nqe lint query.nqe                               # 3. check an NQE query offline, then run it:
    fwdctl nqe run --network <id> --file query.nqe --limit 5 --format table
    fwdctl run edit-alias --help                            # 4. a name that starts edit- changes Forward; read its help first
    fwdctl describe plan-investigation                      # 5. which skill answers which question

What to expect from any of them:

- One JSON result: `status` (`ok`, `failed`, `unknown`, `error`), `finding`, `evidence`, `limits` and `next_actions`. `limits` says what was **not** measured. Read it before you rely on an answer.
- `failed` is a finding (for example, CVEs expose devices), not a crash. `unknown` means it could not decide and is never a pass. The exit status tells a script which (below).
- A name that needs more input says so and what to add: `invalid input: $: missing required "kind"`. `fwdctl run NAME --help` lists the allowed values.
- The first query on a large snapshot can take minutes while Forward loads it. Later ones take seconds. The result says when it did.
- A name that starts `edit-` is a dry run until the input has `"apply": true`. The dry run returns `before`, `after` and `mode: dry_run`, and changes nothing.

With an agent (Claude Code, Codex or Gemini) you ask in plain words and it picks the names: "Which CVEs expose devices here, worst first?", "Why can't 10.1.1.5 reach 10.2.2.9 on TCP 443?", "Run our compliance checks and tell me what's worst." `fwdctl docs install` covers the plugin.

## Commands

| Command | What it does |
|---|---|
| `fwdctl run <skill>` | Run a skill. Inputs are one JSON object on stdin (or `--input FILE`). Prints the result JSON. |
| `fwdctl list` | Every skill: name, description, input schema. |
| `fwdctl describe <skill>` | A skill's procedure. `describe <skill> <file>` prints one of its reference files. |
| `fwdctl nqe lint [FILE]` | Check an NQE query offline: no Forward connection. See `fwdctl docs nqe`. |
| `fwdctl context nqe <question>` | NQE worked examples nearest a question. |
| `fwdctl context schema <term>` | Real NQE field names and enum values matching a term. |
| `fwdctl install claude` / `agents` | Put the skills where an agent loads them. See `fwdctl docs install`. |
| `fwdctl docs [topic]` | This guide. |
| `fwdctl version` | Release, commit and build date. |

## Exit status

| Code | Meaning |
|---|---|
| 0 | `ok`: the question was answered and nothing is wrong |
| 1 | `failed`: a finding (something is wrong), or for `nqe lint` a syntax error |
| 2 | `unknown`: it could not decide. **Never read this as a pass.** Read the `limits`. |
| 3 | `error`: the skill itself could not run |
| 64 | bad usage or input |

A script can branch on the code without parsing JSON.
