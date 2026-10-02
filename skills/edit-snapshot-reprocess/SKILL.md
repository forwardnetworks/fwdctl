---
name: edit-snapshot-reprocess
description: Recomputes a snapshot's derived data from what it collected, showing what would start. Dry run unless apply is true. Use for a failed, stale or UNPROCESSED snapshot.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "snapshots"
  summary: "recompute a snapshot"
  maturity: "2"
  class: write
  effect: "snapshot"
  secrets: "false"
  reversible: "false"
  tools: "snapshots"
---

# edit-snapshot-reprocess

## Intent

Make Forward rebuild the model of a snapshot (paths, checks, NQE answers) from the configuration and state it already holds. It collects
nothing and touches no device. It is the fix for a snapshot stuck in FAILED, one whose answers predate a Forward upgrade, or one that is
UNPROCESSED: a snapshot a write skill's `backdate_snapshot_id` invalidated (`edit-synthetic-query`, `edit-link-overrides`,
`edit-wan-circuit`, and others), or one that was simply never processed. Forward does not start processing an invalidated snapshot by
itself; this skill does (confirmed live: Forward's reprocess call works the same on UNPROCESSED as on FAILED).

## Inputs

`network_id`, `snapshot_id`. Optional: `apply` (default false). See `schema.json`.

## Dry run

Without `apply: true` nothing is changed. The skill reads the snapshot and returns `mode: dry_run` with one change (`reprocess`, state
before and after). Show it to the person who asked before applying.

## Procedure

1. Find the snapshot in the network. If it is not there the answer is **unknown** and nothing is changed.
2. A snapshot that is actively being worked on (UNPACKING, PROCESSING or RESTORING) is refused: **failed**, nothing changed. Every other
   state, including UNPROCESSED, is reprocessed.
3. Dry run: return the plan. With `apply: true`: ask Forward to reprocess and report the state it returns.
4. The skill does not wait for the reprocess to finish; use `inspect-snapshots` to see the state change.

## Undo

There is no undo, and none is needed for the data: reprocessing recomputes the same derived data from the same collected data. The
change says `reversible: false` so a caller does not promise one. While a snapshot reprocesses its answers are unavailable.

## Evidence

One `state` item with `snapshot_id`, `state_before`, `mode` and, once applied, `state_after`.

## Output

The envelope in `schema/skill-result.schema.json`, with `mode` and `changes`.

## Next actions

`inspect-snapshots` to watch the state; `investigate-collection-failure` if it fails again.

## Running this skill

Give the inputs as one JSON object on stdin: `echo '{"network_id": "<id>", "snapshot_id": "<id>"}' | fwdctl run edit-snapshot-reprocess`.
It needs the `fwdctl` binary and `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD` in the environment. Read `status`
(`ok`, `failed`, `unknown`, `error`), `mode` and the `limits`; `unknown` is never a pass. Add `"apply": true` only after the dry run was accepted.
