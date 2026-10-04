---
name: edit-wan-circuit
description: Manages one WAN circuit (a synthetic device for a provider's point-to-point L2 link), showing before and after. Dry run unless apply is true. Use when modelling a leased line.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "edge-synthetic"
  summary: "model a WAN circuit"
  maturity: "1"
  class: write
  effect: "network"
  secrets: "false"
  reversible: "true"
  tools: "synthetic nodes, snapshots"
---

# edit-wan-circuit

## Intent

A WAN circuit stands in for a point-to-point layer 2 link a provider carries between two sites you cannot collect: a bridge with exactly **two** connections, one per end, each an edge device and port (a different VLAN is allowed
on each side). It makes paths that cross the link continue instead of stopping at the edge. It is defined by hand (an NQE query cannot generate one), and it is a statement you make, not something Forward observed.
Why and when to model one: `plan-synthetic-device`.

## Inputs

`network_id`, `name`, and either `connection1` and `connection2` (each `device`, `port`, optional `vlan` 1 to 4095 and `name`) to create or replace the circuit, or `delete: true`. Optional: `backdate_snapshot_id` and `apply` (default false). See `schema.json`.

## Dry run

Without `apply: true` nothing is changed. The skill reads the circuit and returns `mode: dry_run` with one change (`before` and `after`) and, if a backdate is asked for, the snapshots it would invalidate.

## Procedure

1. Check the input: two connections with a device and a port each, or a delete. Read the circuit (absent is fine).
2. Nothing to do (deleting an absent circuit, or the same circuit): **ok**, nothing changed.
3. Dry run: return the plan. With `apply: true`: put or delete, then read the circuit back; not in the requested state is **failed**, never ok.

## Backdate (optional): apply it now

By default a change reaches the **next** processed snapshot. With `backdate_snapshot_id`, after the change the circuits are backdated to that snapshot: that snapshot and **every later one are invalidated**, set to
UNPROCESSED. Forward does **not** reprocess them by itself: run `edit-snapshot` (action reprocess) for each one, then `edit-advanced-reachability` if internet exposure is needed (it only runs after a snapshot is PROCESSED). The
dry run lists them. Ask for it only when the person wants it applied now, and get approval of the backdate separately. It cannot be undone (reprocessing recomputes the same data); the circuit change can.

## Undo

The change's `undo`: run this skill again with the prior circuit (its `before`), or `delete: true` if it did not exist.

## Evidence

One `topology` item: `name`, `existed`, `before`, `after`, `mode`, `backdate` and, once applied, `held`.

## Output

The envelope in `schema/skill-result.schema.json`, with `mode` and `changes`.

## Next actions

`inspect-topology` (kind external) to see the circuit; `investigate-reachability` to test a path across it.

## Running this skill

Give the inputs as one JSON object on stdin: `echo '{"network_id": "<id>", "name": "wan-01", "connection1": {"device": "r1", "port": "Gi0/1", "vlan": 100}, "connection2": {"device": "r2", "port": "Gi0/1", "vlan": 200}}' | fwdctl run edit-wan-circuit`.
It needs the `fwdctl` binary and `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD` in the environment. Read `status`
(`ok`, `failed`, `unknown`, `error`), `mode` and the `limits`; `unknown` is never a pass. Add `"apply": true` only after the dry run was accepted.
