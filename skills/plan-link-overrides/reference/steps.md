# plan-link-overrides: the detail behind each step

Read the section for the step you are on.

## Step 1: comparing

Example: a newer snapshot has two overrides that an older one lacks, `fw-ext-01 po47 <-> core-01 po147` and `fw-ext-01 po47 <-> core-02 po147`. To read one snapshot's overrides without dumping hundreds, use `inspect-topology` with `kind: link_overrides` (a summary and a page; `device` narrows to either end). A snapshot that is still processing cannot be read; wait or pick another.

## Step 2: reading the verification

- a port that does not exist in the snapshot: Forward rejects an override whose port is not a physical interface or port channel of the snapshot it is written against (its source returns "Illegal ports in link overrides"), so the override cannot have been applied there; fix the name or the snapshot;
- the ports exist but a **present** override is not in the topology, or an **absent** one still is: the snapshot was probably not reprocessed with it (see step 3) or Forward did not apply it. Forward's documentation says overrides take precedence over discovered and inferred links and says nothing more about a conflict, and the API does not expose which side won: **unknown**, say so; test by applying and re-reading, never assume.

## Step 3: the write and the reprocess

The undo is the exact inverse edit in the dry run. State plainly: after the write, list the snapshots it invalidated (the dry run names the candidates) and **ASK the person before any `edit-snapshot` reprocess, one at a time; never chain a reprocess onto the apply**. If the read-back answers 409 (UNPROCESSED), the result is unknown, not a failure: the edit was sent, do not apply again, and verify with `inspect-topology` `kind: link_overrides` once the snapshot is processed. **writing a snapshot's overrides invalidates that snapshot** (and the later snapshots in its override range), so it reprocesses and is unavailable meanwhile (verified in Forward's source: a snapshot-scoped write calls the snapshot invalidator; `PUT` replaces *all* of the snapshot's overrides, `POST` edits). Check `inspect-snapshots` afterwards until it is processed, then re-run the comparison and `inspect-topology` `kind: links` for the pair.
The customer's own calls and the network-level alternative are in `reference/customer-curl.md`.

## Step 4: preventing a repeat

What is documented: since Forward 26.8 there are **network-level** operations (`GET`, `PUT`, `PATCH /api/networks/{id}/link-overrides`) whose writes are **staged**: they leave existing snapshots alone, apply automatically to the network's **next** snapshot, and can be pushed onto a snapshot (and the newer ones) with a **backdate**, which invalidates and reprocesses them. Forward has deprecated the snapshot-scoped
operations (the `/api/snapshots/{id}/topology/overrides` family, which `edit-link-overrides` uses) for removal in release 26.11 (Forward's 26.10 API release notes; the operations are still in Forward master as of 2026-10-31). `edit-link-overrides` writes them (`staged: true`, and a separate confirmed `backdate_to_snapshot_id`), and `inspect-topology` kind `link_overrides` shows their counts and the change since the latest processed snapshot as `staged`; the staged mode is fixture-tested, not run on a live Forward.
What is not documented, so **unknown**: whether a snapshot collected after a snapshot-scoped write inherits it (Forward stores overrides with a start and end time, which suggests a set applies to the snapshots from its start to the next change, but the API says nothing about it), and who set an override or when (a record holds only its two ports and its state).
There is no durable alternative that replaces an override: a synthetic device or an NQE query models what lies beyond the collected network, not which of two collected devices are linked. Say that plainly when asked.
