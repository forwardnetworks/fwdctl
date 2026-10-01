---
name: edit-snapshot-note
description: Sets a snapshot's note, such as before change X, after showing what it replaces. Dry run unless apply is true. Use when asked to label or annotate a snapshot, or record why it was taken.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "snapshots"
  summary: "note on a snapshot"
  maturity: "3"
  class: write
  reversible: "true"
  tools: "snapshots"
---

# edit-snapshot-note

## Intent

Record a short human note on one snapshot. This is metadata only: it changes no device, no collected data and no verdict.

## Inputs

`network_id`, `snapshot_id`, `note` (an empty string clears the note; at most 1000 bytes). Optional: `apply` (default false). See `schema.json`.

## Dry run

Without `apply: true` the skill changes nothing. It reads the snapshot's current note and returns `mode: dry_run` with one change
showing `before` and `after`. Show that to the person who asked before applying.

## Procedure

1. Find the snapshot in the network. If it is not there the answer is **unknown** and nothing is changed.
2. Read its current note.
3. If the note already matches, say so and change nothing.
4. Dry run: return the plan. With `apply: true`: set the note, then check that the note Forward returns is the one sent. A mismatch is
   **failed**, never ok.

## Undo

The applied change carries `before` (the prior note) and `undo`: run this skill again on the same snapshot with the prior note and
`apply: true`. A snapshot's favourite flag is deliberately not offered: the SDK has no call to remove one, so it could not be undone.

## Evidence

One `state` item with `note_before`, `note_requested`, `mode` and, once applied, `note_after`.

## Output

The envelope in `schema/skill-result.schema.json`, with `mode` and `changes`.

## Next actions

`inspect-snapshots` to see the snapshot.

## Running this skill

Give the inputs as one JSON object on stdin: `echo '{"network_id": "<id>", "snapshot_id": "<id>", "note": "before change X"}' | fwdctl run edit-snapshot-note`.
It needs the `fwdctl` binary and `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD` in the environment. Read `status`
(`ok`, `failed`, `unknown`, `error`), `mode` and the `limits`; `unknown` is never a pass. Add `"apply": true` only after the dry run was accepted.
