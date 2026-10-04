---
name: edit-org-property
description: Lists Forward's org properties with value, who may change them and risk, and sets or clears one. Dry run unless apply is true. Use when changing an org-wide Forward setting.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "access"
  summary: "change an org-wide setting"
  maturity: "1"
  class: write
  effect: "org"
  secrets: "false"
  reversible: "true"
  tools: "organization properties"
---

# edit-org-property

## Intent

An organization property is a setting that applies to **every network and every user** of the organization: how snapshots are computed, who may sign in, how long data is kept.
This skill shows what is configurable, what each setting does, who may change it and **how dangerous it is to change**, and sets or clears one with the exact undo.
It never touches Forward's global (deployment-wide) configuration.

## Inputs

- No `property`: **list** (read-only). Optional `match` (a substring of the name or its description), `limit` and `offset`. Each row: the property, its current value, whether the organization overrides it, the default,
  who may change it, and its risk.
- `property` and `value` (text: `true`, `42`, `ASYNC`), or `property` and `clear: true` (remove the organization's override so the default applies again).
- `confirm`: the property name again, **required to apply** a property classified `dangerous` or not classified at all.
- `apply` (default false).

## Where the facts come from

The property names and current values are read from Forward itself (`GET /api/config`: effective values, the organization's overrides, the defaults), so a property added by an upgrade appears with no change here.
What a property is, the values it accepts (a boolean, an integer range, the values of an enumeration), whether an organization administrator may change it (`ANYWHERE`, `ON_PREMISES` only, or Forward support
only) and what it is used for come from Forward's source (`OrgProperty`), embedded in the release build. **Risk** is a reviewed classification of what changing it can break:

| Risk | Meaning | To apply |
|---|---|---|
| `dangerous` | can lock users out, delete data or switch off a computation | `confirm` = the property name |
| `caution` | an organization-wide behaviour change that needs a reason; a property that changes snapshot computation is `caution` by rule | dry run, then apply |
| `safe` | a display preference or a notification threshold | dry run, then apply |
| `unclassified` | no reviewed classification (a property newer than the table): treated as caution | `confirm` = the property name |

The classification is a draft that an owner reviews (`knowledge/orgprops-risk.json`). A property this build does not know is never `safe`.

## Guarded window (an experiment on how snapshots are processed)

For a property that changes how snapshots are **computed** (a parsing mode, for example), give `reprocess_snapshot_id`, `network_id` (that snapshot's network), `value` and optionally `window_minutes` (default 30, at most 240).
The window sets the value, reprocesses that ONE snapshot, waits for it to finish, and **always puts the original value back** (an override is set back to its value; no override is cleared), also when the reprocess fails,
the wait runs out or the call is cancelled. A restore that itself fails is reported loudly with the value to set by hand.

The property is **organization-wide**, so the window **refuses to start** while any snapshot in **any** network is UNPACKING, PROCESSING or RESTORING, or while an enabled collection schedule may fire before the window ends
(a time-of-day schedule is computed from its times, days and zone, the organization's zone when it names none; a periodic schedule is due at the network's last collection plus its period, and with no known last collection it
blocks). Anything that cannot be read blocks: unknown is never a pass. The dry run lists every blocker and writes nothing. After the window it reports any snapshot elsewhere that began processing during it. Use
`inspect-snapshots` with no `network_id` to see what is processing now.

## Dry run

Without `apply: true` nothing is changed. A change names the property, its current value, whether it was an override (so the undo is to set it back) or the default (so the undo is to clear the override), the
organization-wide effect, the risk with its reason and **what it can break**, and for a computation property that it applies to snapshots processed after the change.

## Refusals

A value of the wrong kind or outside Forward's range or enumeration; a property Forward does not define; a property only Forward support may change (**failed**, with who can); a dangerous or unclassified property
without `confirm`; a property whose value format is not modelled. An on-premises-only property is refused by Forward (403) on SaaS: the skill says so and does not retry another route.

## Procedure

1. Read the organization's effective values, overrides and defaults. If that fails the answer is **unknown**.
2. List, or find the property (an unknown name is refused, never guessed) and its definition.
3. Validate the value; compute before, after and undo; add the risk and its consequence to the limits.
4. Dry run: return the plan. `apply: true`: send `PUT /api/config/{property}` (or `DELETE` to clear), read the effective value back, and report **failed** if it is not what was asked.

## Undo

Set the property back to the value in `before` (when it was an override), or run again with `clear: true` (when it had none). Reverting does not recompute snapshots that were processed under the new value: reprocess
them (`edit-snapshot` (action reprocess)).

## Evidence

One `state` item with the property, the risk, `before`, `needs_confirm` and the mode.

## Next actions

`inspect-environment` to see the features table and non-default properties; `inspect-snapshots` and `edit-snapshot` (action reprocess) when a computation property changed.

## Running this skill

`echo '{"match": "reachability"}' | fwdctl run edit-org-property` lists; `echo '{"property": "ADVANCED_REACHABILITY_ANALYSIS", "value": "ASYNC"}' | fwdctl run edit-org-property` is the dry run.
Read `status`, `mode`, the `changes` and the `limits`; `unknown` is never a pass. Add `"apply": true` (and `confirm` when it asks) only after the dry run was accepted.
