---
name: plan-snapshot-recovery
description: Sequences the skills that decide why a snapshot is bad and how to recover. Use when a snapshot failed or looks stale.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "snapshots"
  summary: "recover a bad snapshot"
  maturity: "2"
  tools: "inspect-snapshots, inspect-collection, investigate-collection-failure, edit-snapshot, edit-collection"
---

# plan-snapshot-recovery

A procedure, not a skill that runs. Find the cause before touching anything: reprocessing fixes a model, collecting again fixes data, and neither fixes a device that cannot be reached.

## Steps

1. **What state is the data in?** `inspect-snapshots`: the newest snapshot, its state (PROCESSED, FAILED, still processing) and age.
2. **Is collection working?** `inspect-collection` (view status): running, the last task, the collector's connection, devices failing.
3. **Why are devices missing or failing?** `investigate-collection-failure`: per device, the reason (credentials, reachability, a parse problem). A device-side cause (credentials, a firewall, a switched-off device) is fixed outside Forward; say so and stop.
4. **Choose the repair.**
   - The snapshot collected its data but its model is stale or failed to process: `edit-snapshot` (action reprocess) (follow `plan-safe-write`).
   - The data itself is old or incomplete and the cause is fixed: `edit-collection` to start a collection (follow `plan-safe-write`).
   - Neither: report the cause and what the person has to fix.
5. **Did it work?** Re-run `inspect-snapshots` (and `inspect-collection`) after the repair; a reprocess or collection is asynchronous, and "started" is not "fixed".

## The minimum

Before you answer, take at least: the snapshot state (step 1), collection (step 2) and the per-device cause (step 3) before any repair. Stop earlier only when the evidence already answers the question, and say so.

## Answering

State the cause with its evidence, the repair chosen and why, and what was not checked. Never reprocess or collect while one is already running; the skills refuse and say so.
