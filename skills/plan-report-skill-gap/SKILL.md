---
name: plan-report-skill-gap
description: Reports a forward-skills gap as a redacted issue. Use when a skill result was wrong or missing.
compatibility: Needs the fwdctl binary on PATH and the gh CLI logged in to GitHub.
metadata:
  cluster: "environment"
  summary: "report a skill gap"
  maturity: "1"
  tools: "plan-investigation"
---

# plan-report-skill-gap

DOGFOOD-TEMP: this playbook exists only while engineers are dogfooding forward-skills. It is removed at release (see docs/internal.md).

A procedure, not a skill that runs. It turns a gap the engineer just hit into a GitHub issue on `forwardnetworks/forward-skills` so the maintainers can see and fix it. **The repository is PUBLIC.**

## When it applies

- A skill result was wrong, misleading, or `unknown`/empty with no reason given.
- A skill crashed or returned an `error` that looks like a bug.
- A skill, field, filter, format or doc you needed was missing and you had to hand-write NQE, curl or jq, or guess.
- Routing (`fwdctl which`, `plan-investigation`) picked the wrong skill.
- The docs contradicted the behaviour.

Not for: a genuine empty network, a mistake in the person's request, or a Forward product bug unrelated to the skills.

## Steps

1. **Finish the person's task first.** Never derail it. Keep a private note of the gap as you go.
2. **Offer once, at a natural point.** One short line, for example: "A skill had a gap here; want me to file a redacted issue on forward-skills?" Proceed only on a yes. If they decline, drop it.
3. **Look for an existing issue.** `gh issue list -R forwardnetworks/forward-skills --state open --search "<keywords>"`. If one matches, offer to add a short comment with the new occurrence instead of a duplicate (same redaction, same approval): `gh issue comment <number> -R forwardnetworks/forward-skills --body-file <file>`.
4. **Draft** into a temp file (not inline). Title: `[dogfood] <skill-or-area>: <one line>`. Body sections:
   - **Skill/area**
   - **Versions**: `fwdctl version` output, and the plugin version if known
   - **What the person was trying to do** (generic)
   - **What was run**: skill name and inputs, identifiers replaced by placeholders
   - **What came back**: `status`, the finding and `limits` quoted; never raw evidence rows
   - **What was expected**
   - **Workaround used**
   - **Suggested fix**
5. **Redact, then run the mechanical check. This is the heart of the playbook.** Apply the rules below. The repository is public and the tool cannot know customer, organization or network names, so **pass every one you know from the session** (from `inspect-networks`, `whoami`, the person's own words, device-name prefixes) with `--deny`: `fwdctl redact-check --file <draft> --deny "<name>" --deny "<other name>"` (DOGFOOD-TEMP, offline; the flag repeats, and `acme-corp`, `acme corp` and `AcmeCorp` all match one another). It also adds your saved login, your OS user and this machine's name by itself. It prints JSON with masked excerpts. Exit 1 = findings: replace each with a placeholder or delete the sentence. Exit 2 = warnings only (a possible customer or place name): fix it, or ask the engineer whether it is a product word. Re-run until it exits 0. **Only then show the draft to the engineer.** A clean check is necessary, not sufficient: the engineer still reads the draft and says yes. Never paste raw skill output into an issue, even if the check passes.
6. **Show the final draft and ask for approval.** File only after a yes to that exact text. If you edit the draft after the yes, re-run the check and ask again.
7. **File it.** `gh issue create -R forwardnetworks/forward-skills --title "<title>" --body-file <file> --label bug` (a defect), or `--label enhancement` (a missing capability). Only the labels `bug` and `enhancement` exist; do not invent others.
8. **If `gh` is missing or not logged in**, save the draft to a file, say where it is, and tell the engineer to file it with the command above or to paste it into a new issue.
9. **Report the issue URL** (or the saved file path) back to the engineer.

## Redaction rules (the repository is PUBLIC)

Never put any of these in an issue or a comment:

- customer or organization names
- network ids, snapshot ids
- device names and hostnames
- IP addresses or prefixes, unless they are documentation ranges (192.0.2.0/24, 198.51.100.0/24, 203.0.113.0/24, 2001:db8::/32)
- usernames, tokens, passwords, API keys
- URLs of a Forward instance
- file paths that contain a user name
- any raw evidence (rows, config lines, command output)

Replace each with a placeholder: `<device>`, `<ip>`, `<network-id>`, `<snapshot-id>`, `<customer>`, `<user>`, `<forward-url>`. Describe a shape ("an ACI fabric of about 1,400 devices") only when it is needed to reproduce the gap, and keep it generic. **When in doubt, leave it out**: the engineer can add detail in a private channel.

## When only real specifics reproduce the gap

If the gap can be reproduced or understood only with real customer or network specifics, still file the REDACTED public issue (it must pass `fwdctl redact-check`). Pick a short generic slug (`[a-z0-9-]`, for example `link-overrides-409`; never a customer, device or network name) and add exactly one line to the issue: `private reproduction details saved locally by the reporter, ref <slug>`. Save the full, unredacted details (exact inputs, outputs, ids, device names) with `fwdctl dogfood-note --ref <slug> --file <details>` (DOGFOOD-TEMP, offline; it writes a 0600 file under `~/.local/share/fwdctl/dogfood-private/` and prints only its path). Tell the engineer plainly to hand that file to the skills owner privately (chat or email), NOT in a GitHub issue or comment and not pasted into any public place. The agent never attaches, uploads or pastes the private file anywhere.

## Answering

Say whether you filed, commented, or saved a draft; give the URL or path; say that the draft passed `fwdctl redact-check` and was approved; and, if a private note was saved, give its path and say to hand it over privately. Never file without the person's yes.
