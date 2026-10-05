# fwdctl

> **Experimental.** A dogfooding project, **not an official Forward Networks release or product**. It is unsupported and may change without notice.

The Forward Networks command line: log in to Forward, run its **skills** (read-only questions and dry-run-first changes that return evidence), and run NQE
queries. It is an API client built on the public Go SDK, [forward-go-sdk](https://github.com/forwardnetworks/forward-go-sdk). The skills themselves, for
Claude Code, Codex and Gemini, live in [forward-skills](https://github.com/forwardnetworks/fwdctl); this repository is the CLI those skills run.

## Install

```bash
brew install forwardnetworks/tap/fwdctl                  # macOS and Linux
curl -fsSL https://raw.githubusercontent.com/forwardnetworks/fwdctl/main/install.sh | sh   # macOS and Linux, no brew
irm https://raw.githubusercontent.com/forwardnetworks/fwdctl/main/install.ps1 | iex       # Windows (PowerShell)
```

Each release carries `fwdctl_<version>_<os>_<arch>` archives (linux/amd64, darwin/arm64, darwin/amd64, windows/amd64) with a `SHA256SUMS` file; the scripts verify it.
`fwdctl update` installs the newest release in place (a brew-installed copy tells you to run `brew upgrade fwdctl`).

## Use

```bash
fwdctl login --file ~/forward.token        # once: URL, username (or API token access key) and password (or secret), one per line, chmod 600
fwdctl whoami                              # who this connects as, and the Forward version
fwdctl run inspect-networks <<< '{}'       # run any skill with a JSON object on stdin (every skill has a schema)
fwdctl nqe run --network ID --file q.nqe --format table      # run an NQE query: every row, paged for you
fwdctl nqe run --network ID --query-id Q_... --commit-id C   # a saved library query at a commit, with --param NAME=JSON
fwdctl help
```

Every skill returns one envelope (`status` ok, failed, unknown or error; `evidence`; `limits`; `next_actions`). **`unknown` is never a pass**, and a change in Forward is a dry run until
you pass `"apply": true`. Connection flags, the `FORWARD_URL`, `FORWARD_USERNAME` and `FORWARD_PASSWORD` variables, and `--token-file` are all accepted.


## Building from source

`fwdctl` is a Go program (Go 1.24 or newer). It reaches Forward only through the public Go SDK, [github.com/forwardnetworks/forward-go-sdk](https://github.com/forwardnetworks/forward-go-sdk),
which `go.mod` pins; read the SDK's README there for how that client is built and versioned.

```bash
git clone https://github.com/forwardnetworks/fwdctl && cd fwdctl
go build -tags fwdctl_cli -o fwdctl ./cmd/fwdctl      # a working fwdctl (the tag enables deleting a workspace network)
go vet ./... && go test ./... && go test -tags fwdctl_cli ./fwd   # the tests of this tree
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
