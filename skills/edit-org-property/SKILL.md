---
name: edit-org-property
description: Lists Forward's org properties with value, who may change them and risk, and sets or clears one. Dry run unless apply is true. Use when changing an org-wide Forward setting.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
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

Every input, what it means and what to read before applying: `fwdctl run edit-org-property --help` (from `reference/inputs.md`). Read before applying: Guarded window, Refusals.

**A guarded window changes an organization-wide property: it refuses to start while any snapshot in any network is processing or a collection may fire, and it always restores the original value.**

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

## Dry run

Without `apply: true` nothing is changed. A change names the property, its current value, whether it was an override (so the undo is to set it back) or the default (so the undo is to clear the override), the
organization-wide effect, the risk with its reason and **what it can break**, and for a computation property that it applies to snapshots processed after the change.

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

Run `fwdctl run edit-org-property --help` for the inputs and an example, then give them as one JSON object on stdin: `fwdctl run edit-org-property < input.json`. It is a dry run unless the input has `"apply": true`; add that only after the dry run was accepted. Read `status` (`ok`, `failed`, `unknown`, `error`), `mode` and the `limits`; `unknown` is never a pass.

## Examples

```bash
echo '{"match": "reachability"}' | fwdctl run edit-org-property
echo '{"property": "ADVANCED_REACHABILITY_ANALYSIS", "value": "ASYNC"}' | fwdctl run edit-org-property
```
