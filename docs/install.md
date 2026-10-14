# Installing the skills for an agent

*Experimental: a dogfooding project, not an official Forward Networks release or product.*

    fwdctl install claude [--dir DIR]       # writes <dir>/<skill>/SKILL.md; default ~/.claude/skills
    fwdctl install agents [--file FILE]     # adds a managed block (rules and a compact skill list) to AGENTS.md or CLAUDE.md (stdout without --file)

The agent reads each skill's description to decide when to use it, then runs it through `fwdctl run`. The skills themselves (the
playbooks, the safe-write protocol, the NQE authoring notes) are plain text and need no binary, and any channel below can deliver
them. To **run** an analysis against Forward, `fwdctl` must be on the agent's `PATH` and the three `FORWARD_*` variables must be in
its environment.

Re-running is safe: `install claude` rewrites the skill files and `install agents` replaces only its own marked block.

## Claude Code plugin

The repository is also a Claude Code plugin: `/plugin marketplace add forwardnetworks/forward-skills`, then
`/plugin install forward-skills@forward-skills`. The plugin carries the skills and needs no binary to install; the `fwdctl` binary has to be on `PATH` only to run them.

## Codex plugin

    codex plugin marketplace add forwardnetworks/forward-skills
    codex plugin add forward-skills@forward-skills

The same skills as a Codex plugin (`.codex-plugin/plugin.json`, marketplace `.agents/plugins/marketplace.json`). Or run
`fwdctl install agents --file ~/.codex/AGENTS.md` for the rules and a compact skill list in Codex's instructions file.

## Gemini CLI

    gemini skills install https://github.com/forwardnetworks/fwdctl --path skills --scope user
    gemini skills list
    fwdctl install agents --file ~/.gemini/GEMINI.md        # optional rules and skill list in Gemini's context file

Each skill is installed with its `reference/` files under `~/.gemini/skills/<name>/`. Re-run the install to update; `--scope workspace` limits it to one project.
`fwdctl` must be on `PATH` and logged in (`fwdctl login`), because Gemini runs it through its shell tool.

## The NQE language server in Claude Code

`fwdctl nqe lsp` is a language server for `*.nqe` files. It is part of the binary, not a skill, and the skills do not need it (they use `fwdctl nqe lint` and `validate-nqe-query`);
it gives Claude Code diagnostics, hover, definitions and completion on the NQE files it edits. It needs only `fwdctl` on `PATH`: no login, no Forward connection.

**With the skills:** the `forward-skills` plugin registers it already (its `.lsp.json`). Install the plugin, restart Claude Code, done.

**Without the skills:** Claude Code only takes a language server declared by a plugin. `fwdctl install lsp` writes a small local one (a marketplace with a single plugin, no skills) and prints the two commands to run. It writes files only and runs nothing:

    fwdctl install lsp                         # default ~/.claude/plugins-local; --dir DIR to change it
    claude plugin marketplace add ~/.claude/plugins-local && claude plugin install fwd-nqe-lsp@fwd-local

For one session only, with no install, load the plugin directory with a flag instead:

    claude --plugin-dir ~/.claude/plugins-local/fwd-nqe-lsp

The files are three small JSON files (`.claude-plugin/marketplace.json`, `fwd-nqe-lsp/.claude-plugin/plugin.json`, `fwd-nqe-lsp/.lsp.json`); `.lsp.json` is `{"nqe":{"command":"fwdctl","args":["nqe","lsp"],"extensionToLanguage":{".nqe":"nqe"}}}`, so you can write them by hand on a machine with an older fwdctl.

`claude plugin details fwd-nqe-lsp@fwd-local` should list "LSP servers (1) nqe". Restart Claude Code after installing or updating a plugin.

Check: ask Claude to use the LSP tool (`hover`) on a `.nqe` file with a misspelt field. The tool has no diagnostics operation; opening the file makes the server publish them, and they come back as `Record does not have field: "nmae"`. Other editors: run `fwdctl nqe lsp` as the language server over stdio for `*.nqe`.

## Installing the binary

    macOS, Linux:  curl -fsSL https://raw.githubusercontent.com/forwardnetworks/fwdctl/main/install.sh | sh
    Windows:       irm https://raw.githubusercontent.com/forwardnetworks/fwdctl/main/install.ps1 | iex

Builds: linux/amd64, darwin/arm64 (Apple silicon), darwin/amd64 (Intel Mac), windows/amd64. The scripts verify the SHA-256 and print where they installed; they use
the `gh` CLI when it is logged in. Add `~/.local/bin` to `PATH` if told to.

## Updating

    fwdctl update            # replace this binary with the newest release (checksum verified)
    fwdctl update --check    # only report; exit 2 when a newer release exists

A notice appears at most once a day on an interactive terminal (`FWDCTL_NO_UPDATE_CHECK=1` turns it
off); `FWDCTL_AUTO_UPDATE=1` applies updates before a command runs. A development build is never updated automatically.

## Credentials, whoami and completion

    fwdctl whoami                                   # URL, login, organization, Forward version: proves the login works
    fwdctl --url https://fwd.app --username KEY --password-file ~/secret run inspect-networks
    source <(fwdctl completion bash)                # also: zsh, fish, powershell

Completion completes the commands, skill names (`fwdctl run <TAB>`), flag values (`--format`), and with a working login the networks and snapshots (`--network <TAB>`). A Homebrew install
sets it up for bash, zsh and fish by itself; otherwise add the line above to your shell's startup file (`fwdctl completion zsh --help` shows the zsh and fish forms). Every command answers `--help`,
`fwdctl docs cli` is the whole command reference, and the release archives carry man pages (`man fwdctl`, `man fwdctl-nqe-run`; a Homebrew install puts them in place).

The login comes from flags (anywhere on the command line), else `FORWARD_URL` / `FORWARD_USERNAME` / `FORWARD_PASSWORD`, else the file
`~/.config/fwdctl/config.json` (`{"url", "username", "password_file"}`; `--config FILE` names another). The file never holds the password,
only the path of a file that does, and that file must be mode 600.

## Remember a login: `fwdctl login`

    fwdctl login --file ~/forward.token     # once; checks the login, then remembers the file's path
    fwdctl login --forget

The token file has three lines (the Forward URL, the username or API access key, the password or secret; the same three values on one line separated by spaces also work) and must be mode 600. In a `--config` file, `token_file` can sit beside `url` and `username`: a `url` or `username` in the config (and a flag or `FORWARD_*` variable) overrides the matching value in the token file, and a config `password_file` overrides its password. Only its path is saved
(`~/.config/fwdctl/config.json`); the password is never copied. Flags and `FORWARD_*` variables still win.

## Acting as another user (administrator impersonation)

When a user has no credential you can use (for example an automation user whose password cannot be read), an administrator that Forward allows to impersonate can read as that user: set `FORWARD_IMPERSONATE=<user id>` with the administrator's own login (`FORWARD_USERNAME` and `FORWARD_PASSWORD`, a login file or a token). fwdctl signs in as the administrator, asks Forward to impersonate the user (the id gets `.full` appended if it lacks it) and then reads with that user's session. It is for reading: PUT, PATCH and DELETE are refused before they leave the process, every edit skill refuses `apply: true` (a dry run still works), and every result says "made by an administrator impersonating Forward user ...", so what it saw is what that user can see. If Forward refuses the impersonation the run fails; it never falls back to the administrator. It is off unless the variable is set. Forward's own permission rules decide who may impersonate; this does not add one.
