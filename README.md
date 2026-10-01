# Forward Skills

A portable capability layer that lets agents and automation accomplish network-engineering tasks with
Forward. A skill encodes **how** to reach an objective and returns **evidence**, not just an answer.
Forward is the data source here, read almost entirely; the few skills that write change only Forward's own data, and only when told to apply. This is not an agent framework and it calls no model.

```text
Agent / harness   -> orchestration, memory, governance, approval, the model
Forward Skills    -> investigate-reachability, verify-change, ...
fwd               -> thin layer over forward-go-sdk (operation log, snapshot guards, bounded results)
Forward platform  -> the digital twin
```

## Quick start (Claude Code, Codex or Gemini)

```bash
curl -fsSL https://raw.githubusercontent.com/forwardnetworks/forward-skills/main/scripts/install.sh | sh   # the fwdctl binary
fwdctl login --file ~/forward.token      # once: remember the login (file format below)
```

Then give your agent the skills (pick one):

```bash
# Claude Code (the plugin also registers the NQE language server for *.nqe files)
claude plugin marketplace add forwardnetworks/forward-skills && claude plugin install forward-skills@forward-skills
# Codex
codex plugin marketplace add forwardnetworks/forward-skills && codex plugin add forward-skills@forward-skills
# Gemini CLI
gemini skills install https://github.com/forwardnetworks/fwdctl --path skills --scope user
```

Restart the agent after installing, then open a session anywhere and ask. **NQE language server** (diagnostics, hover, completion on `.nqe` files in Claude Code): the Claude Code plugin has it already;
for the server alone, other editors and how to check it, see [docs/install.md](docs/install.md). It is optional: the skills lint and run NQE without it.

The login file has three lines: the Forward URL, the username (or an API token's access key) and the password (or its secret). Keep it private (`chmod 600`):

```text
https://fwd.app
<access key or username>
<secret or password>
```

`fwdctl login` checks the login and remembers only the path of the file (the password is never copied); `fwdctl login --forget` removes it. Flags and the `FORWARD_*` variables still win. More in [docs/install.md](docs/install.md).

Then ask in plain words: *"Why can't 10.1.1.5 reach the database on 5432?"*, *"Give me a security review"*, *"Is CVE-2024-3400 relevant to us?"*. The agent reads the router (`plan-investigation`), follows the playbook it names, and says what it measured and what it did not.
Changes in Forward are dry runs until you approve a plan; devices are never reconfigured.

## Skills

A **playbook** (`plan-*`) says which skills to use for a task and in what order; a **read skill** answers one question and returns evidence; an **edit skill** (`edit-*`) changes only Forward's own data, and only as a dry run until you pass `"apply": true`.
Every skill returns the same envelope (`status` ok / failed / unknown / error, `evidence`, `limits`, `next_actions`); **`unknown` is never a pass**. Start with `plan-investigation`, the router. Several skills have views (`view` or `kind`), and the older names of the skills they absorbed still work. The full list with what each does is in [docs/skills.md](docs/skills.md).

- **Path analysis and troubleshooting:** `plan-troubleshoot-connectivity`, `plan-incident-triage`, `plan-what-changed`, `plan-synthetic-device`, `plan-snapshot-recovery`, `plan-link-overrides`, `investigate-reachability`, `inspect-topology`, `inspect-history`, `compare-device-config`, `inspect-device-files`, `investigate-collection-failure`
- **Security:** `plan-security-posture`, `plan-vulnerability-response`, `plan-segmentation-check`, `inspect-vulnerabilities`, `check-network-compliance`, `investigate-reachability`
- **Change:** `plan-change-review`, `plan-maintenance-window`, `verify-change`, `edit-change-set`
- **Audit and compliance:** `plan-compliance-audit`, `plan-device-audit`, `check-network-compliance`, `inspect-inventory`, `edit-checks`
- **Health and collection:** `plan-health-check`, `inspect-snapshots`, `inspect-collection`, `inspect-performance`, `inspect-environment`, `edit-collection`, `edit-endpoint-profile`, `edit-workspace`, `edit-snapshot-reprocess`, `edit-advanced-reachability`, `edit-snapshot-note`
- **Inventory and topology:** `inspect-networks`, `inspect-inventory`, `inspect-topology`, `edit-device-tags`, `inspect-edge`, `inspect-bgp-neighbors`, `edit-internet-exclusions`, `edit-wan-circuit`, `edit-synthetic-query`, `edit-link-overrides`
- **NQE (custom questions):** `find-nqe-query`, `author-nqe-query`, `validate-nqe-query`, `compare-nqe-results`, `edit-nqe-query`
- **Router and protocol:** `plan-investigation`, `plan-safe-write`, `plan-report-skill-gap` <!-- DOGFOOD-TEMP -->

## More

- [docs/install.md](docs/install.md): every install path (Claude Code, Codex, Gemini CLI), the login, updating, credentials and the NQE language server (other editors, how to check it)
- [docs/skills.md](docs/skills.md): each skill and playbook, and how an AI gets the right context
- [docs/nqe.md](docs/nqe.md): writing, linting, formatting and running NQE with `fwdctl nqe`
- [docs/architecture.md](docs/architecture.md): the reference architecture and a worked flow

## Building from source

`fwdctl` is a Go program (Go 1.24 or newer). It reaches Forward only through the public Go SDK, [github.com/forwardnetworks/forward-go-sdk](https://github.com/forwardnetworks/forward-go-sdk),
which `go.mod` pins; read the SDK's README there for how that client is built and versioned.

```bash
git clone https://github.com/forwardnetworks/fwdctl && cd fwdctl
go build -o fwdctl ./cmd/fwdctl      # a working fwdctl
go vet ./... && go test ./...        # the tests of this tree
```

**The source build is not the release build.** This repository is the API client: `fwdctl`, the skill runners and the Go SDK calls they make. Everything about
**authoring NQE queries** is not here: the linter and formatter, the editor support (language server), Forward's NQE data model, its library signatures, its
builtin table, the worked-example corpus, the starter-query templates and the authoring references. They are Forward's own data and tooling, and they are
included only in the official release binaries. The packages are present as stubs with the same shape and nothing inside, so a binary built from this
source compiles and runs every skill and command, but:

- `fwdctl nqe lint`, `nqe fmt`, `nqe lsp`, `nqe complete` and `nqe hover` report that the authoring tools are not in this build (a lint answer carries a warning, never a silent pass);
- `fwdctl context schema` and `context nqe` return nothing, `nqe template` prints nothing, and schema hints are absent;
- the synthetic-device row check reports that it was not done.

Use a release binary (the install script, or `brew install` once the tap is published) when you want those. `fwdctl nqe run`, `validate-nqe-query` and every
skill that asks Forward are the same in both builds: Forward itself is always the last word on a query.
