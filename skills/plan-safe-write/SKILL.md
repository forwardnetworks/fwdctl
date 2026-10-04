---
name: plan-safe-write
description: States the protocol every edit skill follows: plan, show, approve, apply once, verify, keep the undo. Use before any edit-* skill.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "environment"
  summary: "edit protocol; read first"
  maturity: "3"
  tools: "edit-snapshot, edit-snapshot, edit-checks, edit-alias, edit-network, edit-source, edit-platform, edit-change-set, edit-collection, edit-nqe-query, edit-device-tags, edit-link-overrides, edit-synthetic-query, edit-wan-circuit, edit-internet-exclusions, edit-advanced-reachability, edit-endpoint-profile, edit-workspace, edit-org-property, edit-access, edit-data-file, edit-data-connector"
---

# plan-safe-write

A procedure, not a skill that runs. The `edit-*` skills change **Forward's own data**, never a device. They are safe only if they are used in this order.

## The protocol

1. **Read first.** Run the matching read skill (`inspect-*`, `check-*`) so the change is based on what is there now, and say what you found.
2. **Plan.** Run the edit skill **without** `apply`. The result has `mode: dry_run` and one entry per change with `before`, `after` and `undo`. Nothing has changed. In tool form this is the `<name>_plan` tool.
3. **Show it.** Tell the person exactly what would change, in their words: which snapshot, check, query, tag or link, from what to what, and what cannot be undone (`reversible: false`). Read `limits`: they say when a change takes effect (a tag change is in the next snapshot) and who sees it (a library query is visible to the whole organization).
4. **Wait for approval.** Apply only after the person says yes to **that** plan. Approval of one change is not approval of the next. Never apply on your own initiative, and never because a dry run looked fine.
5. **Apply once.** Run the edit skill with `apply: true` and the same inputs. Do not loop or retry: a retry of a partly applied change repeats it.
6. **Verify.** Read `status`. `ok` means Forward holds the requested state (the skill read it back). `failed` means it does not: say so, say what was changed (`changes` marked `applied`), and offer the `undo`. `unknown` and `error` are never a success.
7. **Keep the undo.** Give the person each change's `undo` text; it is the exact inverse. If the change was not reversible, say that before step 5, not after.

## Never

- Never use an `edit-*` skill to change a device: nothing here pushes configuration.
- Never create what cannot be removed. A tag definition cannot be deleted through the API, so `edit-device-tags` only applies existing tags.
- Never pass `apply: true` in the first call.

## Worked example

[reference/example.md](reference/example.md) shows a good run end to end (illustrative, results shortened). Read it when you are unsure what a finished answer looks like; where you cannot read files, run `fwdctl describe plan-safe-write reference/example.md`.
