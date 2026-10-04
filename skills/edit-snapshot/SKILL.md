---
name: edit-snapshot
description: Changes a snapshot or retention: note, reprocess, invalidate, favorite, delete, retention policy. Dry run unless apply is true. Use when labeling, recomputing or removing snapshots.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "snapshots"
  summary: "note, reprocess, delete"
  maturity: "2"
  class: write
  effect: "network"
  secrets: "false"
  reversible: "false"
  tools: "snapshots"
---

# edit-snapshot

## Intent

The one place to change a snapshot, or how a network keeps its snapshots. It replaces `edit-snapshot` (action note) and `edit-snapshot` (action reprocess) (both still work as names for two releases). It collects nothing and touches no device.

## Inputs

`network_id`, `action`, and `snapshot_id` (not for `retention_policy`). Optional: `apply` (default false), and by action:

| `action` | Also takes | What it does | Undo |
|---|---|---|---|
| `note` | `note` (empty clears; at most 1000 bytes) | sets the snapshot's note, showing what it replaces | run it again with the earlier note |
| `reprocess` | | recomputes the model (paths, checks, NQE answers) from what was collected; the fix for a FAILED, stale or UNPROCESSED snapshot; does not wait | none needed: same data, same result |
| `invalidate` | | empties the derived model until reprocessed | `reprocess` |
| `favorite` | `confirm` = `snapshot_id` | marks it a favorite, which retention never thins | **none**: the API cannot clear a favorite |
| `delete` | `confirm` = `snapshot_id` | removes the snapshot and its model | **none** |
| `retention_policy` | `definition`, `confirm` = `network_id` | sets how Forward thins the network's snapshots (on-premises only) | set the old values back; snapshots already cleaned are gone |

`definition` for `retention_policy`: any of `enabled`, `lastWeek`, `lastMonth`, `lastQuarter`, `lastYear`, `older` (granularity `ALL`, `ONE_PER_DAY`, `ONE_PER_TWO_DAYS`, `ONE_PER_WEEK`, `ONE_PER_TWO_WEEKS`, `ONE_PER_MONTH`, `ONE_PER_QUARTER`, `NONE`); fields left out keep their current value. Forward's allowed combinations are in `reference/retention.md`. See `schema.json`.

## Dry run

Without `apply: true` nothing changes. The result returns `mode: dry_run`, the change with before and after, the undo (or that there is none), and for `delete`, `favorite` and `retention_policy` the exact `confirm` value needed.

## Procedure

1. Find the snapshot (or read the network's policy). Not found: **unknown**, nothing changed.
2. A snapshot being worked on (UNPACKING, PROCESSING, RESTORING) is refused for every action but `note`.
3. Dry run returns the plan. With `apply: true`: one call, then read back and say if the read-back differs (**failed**).

## Limits

`delete` and `retention_policy` are destructive and cannot be undone through the API; use `inspect-snapshots` (with `retention: true` for the saved policy and what the next cleanup would delete) before either. Forward keeps the 10 newest processed snapshots, favorites, and predictions with the snapshots they derive from, whatever the policy.

## Evidence

One `state` item: `action`, `mode`, `before`, and `after` once applied.

## Next actions

`inspect-snapshots` to see the state; `investigate-collection-failure` if a reprocess fails again.

## Running this skill

`echo '{"network_id":"N","snapshot_id":"S","action":"note","note":"before change X"}' | fwdctl run edit-snapshot` is the dry run; add `"apply": true` once accepted.
