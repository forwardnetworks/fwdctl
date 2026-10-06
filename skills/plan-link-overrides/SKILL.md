---
name: plan-link-overrides
description: Sequences the skills that diagnose and fix missing or drifted link overrides. Use when a manual or suppressed link is missing.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "edge-synthetic"
  summary: "missing or drifted overrides"
  maturity: "1"
  tools: "inspect-snapshots, inspect-topology, inspect-inventory, edit-link-overrides"
---

# plan-link-overrides

A procedure, not a skill that runs. A **link override** forces a link between two ports to be **present** (a link Forward did not discover) or **absent** (a discovered link that is wrong). Forward's API says it builds the links from discovery (LLDP, CDP),
inference (MAC addresses) and the overrides, and that overrides **have the highest precedence**. Overrides are stored per snapshot, so "missing" usually means one snapshot has them and the one people look at does not.
Read the reference file a step names only when you reach that step (where you cannot read files: `fwdctl describe plan-link-overrides reference/<file>`).

## Steps

1. **Compare.** `inspect-snapshots` to pick the snapshot the customer uses and an earlier or later one, then `inspect-topology` with `compare_to_snapshot_id` between them: what was added, removed or changed (present versus absent), the counts and the devices involved. Name both snapshot ids in what you report.
   Example: a newer snapshot has two overrides that an older one lacks, `fw-ext-01 po47 <-> core-01 po147` and `fw-ext-01 po47 <-> core-02 po147`. To read one snapshot's overrides without dumping hundreds, use
   `inspect-topology` with `kind: link_overrides` (a summary and a page; `device` narrows to either end). A snapshot that is still processing cannot be read; wait or pick another.
2. **Verify each missing or suspect override.** The `link_overrides` rows already say `ports_exist` (both devices and both interfaces are in that snapshot's model) and `link_in_topology` (the pair is among the snapshot's links). For the detail: `inspect-topology` `kind: links` with `device` to see what the
   snapshot connects that device to, and `inspect-inventory` for the interfaces. Reading the result:
   - a port that does not exist in the snapshot: Forward rejects an override whose port is not a physical interface or port channel of the snapshot it is written against (its source returns "Illegal ports in link overrides"), so the override cannot have been applied there; fix the name or the snapshot;
   - the ports exist but a **present** override is not in the topology, or an **absent** one still is: the snapshot was probably not reprocessed with it (see step 3) or Forward did not apply it. Forward's documentation says overrides take precedence over discovered and inferred links and says nothing more about a conflict, and the API does not expose which side won: **unknown**, say so; test by applying and re-reading, never assume.
3. **Fix, minimally.** Derive the edit from the diff: each override the target snapshot lacks becomes `add_present` or `add_absent` (by its `state`), each one it should not have becomes `remove_present` or `remove_absent`. Run `edit-link-overrides` on the **snapshot people use** as a dry run, show the exact change and the overrides before it, get the person's approval, then apply once and read it back (follow `plan-safe-write`).
   The undo is the exact inverse edit in the dry run. State plainly: **writing a snapshot's overrides invalidates that snapshot** (and the later snapshots in its override range), so it reprocesses and is unavailable meanwhile (verified in Forward's source: a snapshot-scoped write calls the snapshot invalidator; `PUT` replaces *all* of the snapshot's overrides, `POST` edits). Check `inspect-snapshots` afterwards until it is processed, then re-run the comparison and `inspect-topology` `kind: links` for the pair.
   The customer's own calls and the network-level alternative are in [reference/customer-curl.md](reference/customer-curl.md).
4. **Prevent a repeat.** What is documented: since Forward 26.8 there are **network-level** operations (`GET`, `PUT`, `PATCH /api/networks/{id}/link-overrides`) whose writes are **staged**: they leave existing snapshots alone, apply automatically to the network's **next** snapshot, and can be pushed onto a snapshot (and the newer ones) with a **backdate**, which invalidates and reprocesses them. Forward has deprecated the snapshot-scoped
   operations (the `/api/snapshots/{id}/topology/overrides` family, which `edit-link-overrides` uses) for removal in release 26.11. `fwdctl` does not read or write the staged, network-level overrides yet, so a staged override is invisible to every skill here; say that when the answer depends on it.
   What is not documented, so **unknown**: whether a snapshot collected after a snapshot-scoped write inherits it (Forward stores overrides with a start and end time, which suggests a set applies to the snapshots from its start to the next change, but the API says nothing about it), and who set an override or when (a record holds only its two ports and its state).
   There is no durable alternative that replaces an override: a synthetic device or an NQE query models what lies beyond the collected network, not which of two collected devices are linked. Say that plainly when asked.
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
