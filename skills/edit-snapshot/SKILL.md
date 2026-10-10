---
name: edit-snapshot
description: Changes a snapshot or retention: note, reprocess, invalidate, (un)favorite, delete, retention, export, import. Dry run unless apply. Use when labeling, moving or removing snapshots.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "snapshots"
  summary: "note, reprocess, delete"
  maturity: "2"
  class: write
  effect: "network"
  secrets: "true"
  reversible: "false"
  tools: "snapshots"
---

# edit-snapshot

## Intent

The one place to change a snapshot, or how a network keeps its snapshots. It replaces `edit-snapshot` (action note) and `edit-snapshot` (action reprocess) (both still work as names for two releases). It collects nothing and touches no device.

## Inputs

What each input means, and the fields per object and action: `fwdctl run edit-snapshot --help`, or `reference/inputs.md`, `reference/retention.md`, `reference/transfer.md`.

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
