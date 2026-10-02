---
name: edit-advanced-reachability
description: Starts advanced reachability for a processed snapshot that never had it, showing cost first. Dry run unless apply is true. Use when internet exposure is PENDING_ADVANCED_REACHABILITY.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "snapshots"
  summary: "start advanced reachability"
  maturity: "1"
  class: write
  effect: "snapshot"
  secrets: "false"
  reversible: "false"
  tools: "snapshots"
---

# edit-advanced-reachability

## Intent

Forward computes a snapshot's flow analysis in two steps: processing (always) and **advanced reachability** (by default only on request: organization property `ADVANCED_REACHABILITY_ANALYSIS` is `ON_DEMAND`, unless set to `ASYNC`).
Internet exposure (`internet_addressable` in `inspect-vulnerabilities`) is computed from it, so a snapshot that never had it has no exposure answer (`PENDING_ADVANCED_REACHABILITY`). This starts it for one snapshot. It adds computed analysis; it changes no
device and no collected data.

## Inputs

`network_id`, `snapshot_id`. Optional: `apply` (default false). See `schema.json`.

## Dry run

Without `apply: true` nothing is changed. The skill reads the snapshot and returns `mode: dry_run` with one change (state before, expected after), the device count and the cost: asynchronous and compute-heavy.

## Procedure

1. Find the snapshot. Not in the network: **unknown**, nothing changed.
2. Refuse (**failed**, nothing changed) unless the snapshot is PROCESSED and its advanced reachability state is UNPROCESSED. PROCESSING: already running. PROCESSED: already computed. FAILED, CANCELED or TIMED_OUT: Forward ignores a request in a final state, so reprocess the snapshot
   (`edit-snapshot-reprocess`) first. A snapshot that reports no state is **unknown**.
3. Dry run: return the plan. With `apply: true`: ask Forward to compute, then read the snapshot back once and report the state.
3a. **Disabled is not never triggered.** Before it starts anything the skill reads the organization's effective `DISABLE_FLOW_COMPUTATION` and `ADVANCED_REACHABILITY_ANALYSIS`. If `DISABLE_FLOW_COMPUTATION` is true it refuses (**failed**, nothing changed): Forward does not run the stages after reachability, so the snapshot stays UNPROCESSED and the request would give no exposure; the property is changed by an organization administrator of an on-premises Forward or by Forward support, and a snapshot picks the change up after it is reprocessed. If it is false the limits say what the API cannot rule out (a network-level override of the property, or a license without path analysis, returned by no API: **UNKNOWN**), and with `ASYNC` that Forward should have started the computation itself. If the properties cannot be read the limits say it is UNKNOWN whether flow computation is enabled; the request is not refused on a guess.
4. The skill does not wait. Watch `inspect-snapshots` (`advanced_reachability`) until PROCESSED; then `inspect-vulnerabilities` with `internet_addressable` works if the snapshot has an internet node.

## Undo

None: it adds computed analysis and removes nothing. A reprocess of the snapshot clears it. The change says `reversible: false`.

## Evidence

One `state` item with `snapshot_id`, `snapshot_state`, `advanced_reachability_before`, `devices`, `mode` and, once applied, `advanced_reachability_after`.

## Output

The envelope in `schema/skill-result.schema.json`, with `mode` and `changes`.

## Limits worth stating

Asynchronous: Forward answers at once and computes in the background. Compute-heavy on a large network (Forward's compute workers; bounded by `REACHABILITY_MAX_CONCURRENT_DEVICES_PER_WORKER` and `REACHABILITY_TIMEOUT_MINUTES`). Exposure also needs an internet node and flow computation enabled.
A reprocess or a backdate clears the state again. Needs view-paths access and the role a reprocess needs.

## Next actions

`inspect-snapshots` to watch the state; `inspect-vulnerabilities` for internet_addressable once PROCESSED.

## Running this skill

Give the inputs as one JSON object on stdin: `echo '{"network_id": "<id>", "snapshot_id": "<id>"}' | fwdctl run edit-advanced-reachability`.
It needs the `fwdctl` binary and `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD` in the environment. Read `status`
(`ok`, `failed`, `unknown`, `error`), `mode` and the `limits`; `unknown` is never a pass. Add `"apply": true` only after the dry run was accepted.
