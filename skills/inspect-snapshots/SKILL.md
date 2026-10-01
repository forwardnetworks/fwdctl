---
name: inspect-snapshots
description: Lists a network's snapshots, says which is the newest worth reading, which are predictions or drafts, and how complete one is. Use when choosing a before and after snapshot.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "snapshots"
  summary: "list snapshots, pick the newest"
  maturity: "3"
  tools: "snapshots"
---

# inspect-snapshots

## Intent

Every comparison and every change check needs the right snapshots. This lists them with their state and kind, marks the
newest **processed, collected** one (what the other skills read by default), and can describe one snapshot in detail.

## Inputs

`network_id`. Optional: `snapshot_id` (describe that one), `limit` (default 10, at most 100) and `offset` to page the list.
See `schema.json`.

## Procedure

1. List the network's snapshots, newest first.
2. Without `snapshot_id`: return a page of rows (id, state, kind, time, device count, note, favourite, and for a prediction its
   parent snapshot and change set, and `age_seconds` since `createdAt`) and mark the latest readable one.
3. With `snapshot_id`: return that snapshot's row plus its collection metrics and its exception count and the kinds of exception (type, occurrences, up to 10 devices, the first line of the stack trace; needs DEBUG_SNAPSHOTS).
4. Decide:
   - Rows: **ok**. A prediction is labelled `predicted`: it is a model of a proposed change, not a state the network was in.
   - A network with no snapshots, or an id that is not in it: **unknown**.
   - Exceptions that could not be read (a permission gap) are said in the limits; their absence is not health.

## Limits worth stating

`age_seconds` is measured from `createdAt`, and a reprocess keeps the original `createdAt`, so for a recomputed snapshot it is the age of the snapshot, not of the run. For a snapshot that is not in a final state (for example PROCESSING) the skill reads Forward's processing progress and estimate (`GET /api/snapshots/{id}/progress` and `/processEstimate`, the latter a preview endpoint) and adds `processing: {started_at, elapsed_seconds, stage, stage_state, stages: [{stage, state, started_at, objects}], done, estimate_total_seconds}`. `started_at` and `elapsed_seconds` count from the earliest stage that started, not from `createdAt`; `stage` is the stage computing now (null when none is); a stage that has not started has no `started_at`, and `objects` appears once a stage has reported; `done` is Forward's own verdict (every stage but ADVANCED_REACHABILITY finished). `estimate_total_seconds` is the sum of Forward's per-stage durations, an estimate from earlier runs of this network. Progress is read for the first five non-final snapshots of a listing (read one with `snapshot_id` for more). If either call fails or is not served the result says so in `limits`, never silently.

## Evidence

One `collection` item: the total, the page of snapshots and the id of the latest readable one, or one snapshot's detail.

## Output

The envelope in `schema/skill-result.schema.json`.

## Next actions

`verify-change` and `analyze-blast-radius` to compare two snapshots, `investigate-collection-failure` for one that looks
incomplete.

## Running this skill

Give the inputs as one JSON object on stdin: `echo '{"network_id": "<id>"}' | fwdctl run inspect-snapshots`. It needs the
`fwdctl` binary and `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD` in the environment. Read `status`
(`ok`, `failed`, `unknown`, `error`) and the `limits`; `unknown` is never a pass. Inputs are listed above and in `schema.json`.
