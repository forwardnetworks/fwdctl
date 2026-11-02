---
name: inspect-topology
description: Reads layout: links, sites, tags, aliases, link overrides (compare_to_snapshot_id diffs two snapshots); kind external shows the internet node, L3 VPNs. Use when asked what a device links to.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "inventory-topology"
  summary: "links, sites, tags, overrides"
  maturity: "3"
  tools: "snapshots, topology, locations, tags, aliases"
---

# inspect-topology

## Intent

Answer layout and grouping questions from Forward's model, as exact rows with the snapshot they came from. It reads
and never judges. For devices, interfaces, VLANs and hosts use `inspect-inventory`.

## Inputs

What each input means, and the fields per object and action: `fwdctl run inspect-topology --help`, or `reference/inputs.md`.

## Procedure

1. Resolve the snapshot; it must be processed. Otherwise `unknown`.
2. Read the kind, filter by `device` if given, and page with `limit` and `offset`.
3. Decide:
   - Rows: **ok**, with the total and the window; the limits say how to page.
   - None: **unknown**. An empty network, a wrong device name (matched exactly) and a feature that was never collected
     look the same, so the skill never says "no links" or "no tags".

**kind external, kind link_overrides** (the fields, paging and the compare mode are in `reference/kinds.md`). Three things to hold on to: a 404 for the internet node means none is modelled, and a path to a public address then ends at the edge; link overrides are written **per snapshot**, so a newer or reprocessed snapshot can lack ones an earlier one has (`compare_to_snapshot_id` shows which), and **writing overrides invalidates the snapshot** (`edit-link-overrides`, `plan-link-overrides`); nothing read means **unknown**, never an empty network. Editing the edge model is out of scope (`edit-synthetic-query`, `edit-wan-circuit`).

## Evidence

One `topology` item: kind, filter, total, window and the rows.

## Output

The envelope in `schema/skill-result.schema.json`.

## Next actions

`investigate-reachability` to trace a path across the links, `inspect-inventory` for the devices themselves.

## Running this skill

Run `fwdctl run inspect-topology --help` for the inputs and an example, then give them as one JSON object on stdin: `fwdctl run inspect-topology < input.json`. Read `status` (`ok`, `failed`, `unknown`, `error`) and the `limits`; `unknown` is never a pass.
