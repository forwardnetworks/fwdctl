# Installing the skills for an agent

    fwdctl install claude [--dir DIR]       # writes <dir>/<skill>/SKILL.md; default ~/.claude/skills
    fwdctl install agents [--file FILE]     # adds a managed block (rules and a compact skill list) to AGENTS.md or CLAUDE.md (stdout without --file)

The agent reads each skill's description to decide when to use it, then runs it through `fwdctl run`. So `fwdctl` must be on the
agent's `PATH` and the three `FORWARD_*` variables must be in its environment.

Re-running is safe: `install claude` rewrites the skill files and `install agents` replaces only its own marked block.

## Claude Code plugin

The repository is also a Claude Code plugin: `/plugin marketplace add forwardnetworks/forward-skills`, then
`/plugin install forward-skills@forward-skills`. The plugin carries the skills; the `fwdctl` binary still has to be on `PATH`.

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
it gives Claude Code diagnostics, hover, definitions and completion on the NQE files it edits. The `forward-skills` plugin registers it already (its `.lsp.json`). To get only the server,
Claude Code needs it declared by a plugin; a two-file local plugin does it:

    mkdir -p ~/.claude/plugins-local/fwd-nqe-lsp/.claude-plugin && cd ~/.claude/plugins-local/fwd-nqe-lsp
    printf '{"name":"fwd-nqe-lsp","description":"NQE language server","version":"1.0.0","lspServers":"./.lsp.json"}\n' > .claude-plugin/plugin.json
    printf '{"nqe":{"command":"fwdctl","args":["nqe","lsp"],"extensionToLanguage":{".nqe":"nqe"}}}\n' > .lsp.json
    claude --plugin-dir ~/.claude/plugins-local/fwd-nqe-lsp

Check: ask Claude to use the LSP tool on a `.nqe` file with a misspelt field; it returns `Record does not have field: "nmae"`. Restart Claude Code after installing or updating a plugin.

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

The login comes from flags in front of the command, else `FORWARD_URL` / `FORWARD_USERNAME` / `FORWARD_PASSWORD`, else the file
`~/.config/fwdctl/config.json` (`{"url", "username", "password_file"}`; `--config FILE` names another). The file never holds the password,
only the path of a file that does, and that file must be mode 600.

## Remember a login: `fwdctl login`

    fwdctl login --file ~/forward.token     # once; checks the login, then remembers the file's path
    fwdctl login --forget

The token file has three lines (the Forward URL, the username or API access key, the password or secret) and must be mode 600. Only its path is saved
(`~/.config/fwdctl/config.json`); the password is never copied. Flags and `FORWARD_*` variables still win.
