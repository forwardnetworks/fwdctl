---
name: plan-link-overrides
description: Sequences the skills that diagnose and fix missing or drifted link overrides. Use when a manual or suppressed link is missing.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "edge-synthetic"
  summary: "missing or drifted overrides"
  maturity: "1"
  tools: "inspect-snapshots, inspect-topology, inspect-inventory, edit-link-overrides, edit-snapshot"
---

# plan-link-overrides

A procedure, not a skill that runs. A **link override** forces a link between two ports to be **present** (a link Forward did not discover) or **absent** (a discovered link that is wrong). Forward's API says it builds the links from discovery (LLDP, CDP),
inference (MAC addresses) and the overrides, and that overrides **have the highest precedence**. Overrides are stored per snapshot, so "missing" usually means one snapshot has them and the one people look at does not.
Read the reference file a step names only when you reach that step (where you cannot read files: `fwdctl describe plan-link-overrides reference/<file>`).

## Steps

1. **Compare.** `inspect-snapshots` to pick the snapshot the customer uses and an earlier or later one, then `inspect-topology` with `compare_to_snapshot_id` between them: what was added, removed or changed (present versus absent), the counts and the devices involved. Name both snapshot ids. `kind: link_overrides` reads one snapshot's overrides without dumping them; a snapshot still processing cannot be read. Detail: [reference/steps.md](reference/steps.md).
2. **Verify each missing or suspect override.** The `link_overrides` rows say `ports_exist` and `link_in_topology`; `inspect-topology` `kind: links` with `device` and `inspect-inventory` give the detail. A port that does not exist means the override cannot have applied there; a present override that is not in the topology (or an absent one that still is) means the snapshot was not reprocessed with it or Forward did not apply it, and which side won is **unknown**: say so, never assume. How to read each case: `reference/steps.md`.
3. **Fix, minimally.** Derive the edit from the diff (each override the target lacks becomes `add_present` or `add_absent`, each it should not have becomes `remove_present` or `remove_absent`) and run `edit-link-overrides` on the **snapshot people use**: dry run, show the change, approval, apply once, read back (`plan-safe-write`). **Writing a snapshot's overrides invalidates it** (and later snapshots in its override range): list them after the write and **ASK before any `edit-snapshot` reprocess, one at a time; never chain a reprocess onto the apply**. A 409 on the read-back is unknown, not a failure: do not apply again. The 409 case, the customer's own calls and the network-level alternative: `reference/steps.md` and [reference/customer-curl.md](reference/customer-curl.md).
4. **Prevent a repeat.** Since Forward 26.8 network-level overrides are **staged** (they reach the network's next snapshot, and a confirmed backdate pushes them onto existing ones); the snapshot-scoped operations `edit-link-overrides` uses by default are scheduled for removal in 26.11. Use `edit-link-overrides` with `staged: true` (fixture-tested, not run live). Whether a later snapshot inherits a snapshot-scoped write, and who set an override, are **unknown**; no synthetic device or NQE query replaces an override. Detail: `reference/steps.md`.
5. **Hand over.** Give the customer a short summary in this shape (fill every blank from evidence, none from memory):
   - *What was missing:* `<n>` override(s) on `<devices>`, present in snapshot `<id>` and absent in `<id>` (ports, state).
   - *What we did:* the edit applied to snapshot `<id>` (or the staged change), the approval, and that the snapshot reprocessed; the proof (`inspect-topology` with `compare_to_snapshot_id` now shows no difference, the pair is in `inspect-topology` links).
   - *What to expect:* the next collected snapshot may start without these overrides until staged at network level or applied again; how to check (`inspect-topology` with `compare_to_snapshot_id` against this snapshot); the undo.

## Freedom

How much room each step gives (levels are defined in `plan-investigation`):

- **Fixed** (steps 3): the edit is a write that invalidates the snapshot: `plan-safe-write`, dry run, approval, apply once, and say plainly that it reprocesses the snapshot.
- **Guided** (steps 1, 2, 4, 5): compare, then verify each override, and do not edit before both; the hand-over keeps the shape given.

## The minimum

Before you answer, take at least: the comparison (step 1) and the per-override verification (step 2). Do not edit before both. Stop earlier only when the evidence already answers the question, and say so.

## Answering

State which snapshots you compared and what differs, what you verified for each override and what stayed unknown (which side wins, who set it, whether later snapshots inherit it), the fix and whether it was applied, and what was not checked. Never apply while a snapshot is still processing, and never describe a write as harmless: it invalidates the snapshot.

## Worked example

[reference/example.md](reference/example.md) shows a good run end to end (illustrative, results shortened). Read it when you are unsure what a finished answer looks like; where you cannot read files, run `fwdctl describe plan-link-overrides reference/example.md`.
