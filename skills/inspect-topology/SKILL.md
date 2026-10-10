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

**kind external.** Read the internet node (a 404 means none is modelled, and the limits say a path to a public address then ends at the edge), Forward's suggested internet connections (an empty answer means Forward has none to suggest), the intranet nodes, L3 VPNs, L2 VPNs, adjacent networks and WAN circuits, and the snapshot's manual link overrides (added = present, suppressed = absent). A node driven by an NQE query shows its query id and what the query generated: a **summary** (connections per uplink port, per VRF and per subnet discovery method, and the number of distinct uplinks and VRFs) and the first connections. Each connection shows its uplink, gateway, VLAN, VRF, subnet discovery and peer IPs; an empty name, site or subnet list is normal for a query-generated connection. **To see one node in full, give `device` (its name; the internet node is `internet`)**: the connections are then paged in a stable order (VRF, uplink, VLAN) with `limit` (default 25, at most 200) and `offset`, and the node's query is looked up in the organization's library (`in_library`, its intent and source when found; for the internet node also `excluded_subnets`, the public prefixes kept off it; when it is not there it was deleted, never committed or lives elsewhere, and the connections shown are Forward's stored result). **ok** when anything was read, **unknown** when nothing could be. Editing the edge model is out of scope (`edit-synthetic-query`, `edit-wan-circuit`).

**kind link_overrides.** One snapshot's link overrides (added = `present`, suppressed = `absent`) without dumping them: `summary` counts (present, absent, total, distinct device pairs and devices, the top 10 device pairs and devices with their present and absent counts) and **one page of rows** in a stable order (device pair, state, ports), `limit` (default 50, at most 200) and `offset`; `device` keeps the overrides with that device at either end. Each row of the page carries `ports_exist` (both devices and both interfaces are in this snapshot's model; `missing_ports` names what is not) and `link_in_topology` (the pair is among the snapshot's links); they are computed for the rows shown only, null with a limit when they could not be derived. A present override that is not in the topology, or an absent one that still is, means the snapshot was not reprocessed with it or Forward did not apply it. The external view carries the same summary and the first 25 rows. Overrides are **written per snapshot**: a newer or reprocessed snapshot can lack ones an earlier snapshot has (`compare_to_snapshot_id` shows which), and **writing overrides invalidates the snapshot** (`edit-link-overrides`, `plan-link-overrides`). Forward's override records hold only the two ports and the state: no author, time or source, and which side wins over a discovered link is not exposed by the API (Forward's docs say overrides take precedence over discovered and inferred links). The overrides read are the snapshot's own; those staged for the network's next snapshot are not read.

**kind link_overrides with `compare_to_snapshot_id`** (the retired `compare-link-overrides` skill). Link overrides are stored per snapshot, so a snapshot collected or reprocessed later can lack overrides an earlier one has. This answers "what is different" as exact overrides with counts, so a handful of missing links is not buried in hundreds. Both snapshots must exist (otherwise **unknown**) and Forward must be able to read their overrides (it refuses while a snapshot is being processed: **unknown** with the reason, nothing compared). Each override is normalised (a link has no direction: `(a, b)` and `(b, a)` are one) and compared: **added** (only in the later snapshot), **removed** (only in the earlier), **changed** (present in one, absent in the other), unchanged counted. **ok** with the counts in the finding (both snapshot ids named). No difference is a finding, stated with the totals. The `topology` evidence item carries `before_snapshot_id`, `after_snapshot_id`, `counts` (`before_total`, `after_total`, `added`, `removed`, `changed`, `unchanged`), the bounded `added`, `removed` and `changed` examples in a stable order, and `devices_involved` (top 10 devices by differing overrides). Forward gives no author, time or source for an override, so who set one and when is not available; this reads the overrides stored with each snapshot (Forward's snapshot-scoped endpoint, deprecated for removal in 26.11); overrides staged for the network's next snapshot are not read. Next: `edit-link-overrides` (dry run first, then `plan-safe-write`), `plan-link-overrides` for the whole procedure.

## Evidence

One `topology` item: kind, filter, total, window and the rows.

## Output

The envelope in `schema/skill-result.schema.json`.

## Next actions

`investigate-reachability` to trace a path across the links, `inspect-inventory` for the devices themselves.

## Running this skill

Give the inputs as one JSON object on stdin: `echo '{"network_id": "<id>"}' | fwdctl run inspect-topology`. It needs the
`fwdctl` binary and `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD` in the environment. Read `status`
(`ok`, `failed`, `unknown`, `error`) and the `limits`; `unknown` is never a pass. Inputs are listed above and in `schema.json`.
