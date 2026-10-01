---
name: edit-collection
description: Starts a collection, or stops a running one, after checking none runs and the collector is up. Dry run unless apply is true. Use when asked to collect now or cancel a collection.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "snapshots"
  summary: "start or stop a collection"
  maturity: "3"
  class: write
  reversible: "false"
  tools: "collector tasks, collectors"
---

# edit-collection

## Intent

Get a fresh snapshot, or end a collection that should not continue. A collection opens management sessions to every device in scope
(load on the devices, and lockout if credentials are wrong), so nothing starts until `apply` is true, and it never starts on top of a
running one.

## Inputs

`network_id`. Optional: `action` (`start`, the default, or `stop`), `task_id` (required for `stop`), `stop_mode` (`CANCEL`, the default,
or `SKIP`), `apply` (default false). See `schema.json`.

## Dry run

Without `apply: true` the skill changes nothing. It reads whether a collection is running, the attached collector and the last
finished task, and returns `mode: dry_run` with the change it would make. Show that before applying.

## Procedure

1. **start**: if a collection is already running, or the attached collector is not connected, the answer is **unknown** with the
   reason and nothing is started. Otherwise, with `apply`, start it and return the task id at once. It does not wait: use
   `inspect-collection-status` for progress and `inspect-snapshots` for the snapshot.
2. **stop**: read the task. A missing task, another network's task, or one that already finished is **unknown** and untouched.
   `CANCEL` ends it with no snapshot; `SKIP` ends it and makes a snapshot from what was already collected.

## Undo

- A started collection is undone by stopping the task (`action: stop`, `CANCEL`); a snapshot it already made stays. The applied change
  names the exact call.
- A stopped collection cannot be resumed; start a new one. So the skill is not marked reversible overall.

## Evidence

One `collection` item with `state` (running, collector, last finished task) and, once applied, the `task_id`.

## Output

The envelope in `schema/skill-result.schema.json`, with `mode` and `changes`.

## Next actions

`inspect-collection-status`, `inspect-snapshots`.

## Running this skill

Give the inputs as one JSON object on stdin: `echo '{"network_id": "<id>"}' | fwdctl run edit-collection`. It needs the `fwdctl` binary
and `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD` in the environment. Read `status` (`ok`, `failed`, `unknown`, `error`), `mode`
and the `limits`; `unknown` is never a pass. Add `"apply": true` only after the dry run was accepted.
