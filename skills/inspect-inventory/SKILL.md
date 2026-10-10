---
name: inspect-inventory
description: Reads network contents: size, vendors, devices, interfaces, VLANs, VRFs, hosts, cloud VPCs; kind ip_owner finds an IP's interface. Use when asked how many devices exist or who owns an IP.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "inventory-topology"
  summary: "devices, interfaces, VLANs, IP owner"
  maturity: "3"
  tools: "snapshots, query"
---

# inspect-inventory

## Intent

Answer inventory questions from Forward's model of the network, as exact rows with the snapshot they came from. It
reads and never judges: a row is a fact about the collected network, not a verdict.

## Inputs

What each input means, and the fields per object and action: `fwdctl run inspect-inventory --help`, or `reference/inputs.md`, `reference/kinds.md`.

## Procedure

1. Resolve the snapshot; it must be processed. Otherwise `unknown`.
2. Run the kind's query with `limit` and `offset`. Filters are passed as query parameters, never spliced into the text.
3. Decide:
   - Rows returned: **ok**, with the rows, the total, and the window shown. If more rows exist than were returned, the
     limits say so and how to page.
   - Zero rows: **unknown**. The entity may not exist, or the filter may be wrong (names are matched exactly); the skill
     cannot tell which. For `summary`, zero devices means an empty or unmodeled network, not "nothing to report".
4. On a predicted snapshot the limits say the rows describe a prediction, not collected state.

**Cloud kinds, `devices` with `compare_to_snapshot_id`, a snapshot that is not ready, and `ip_owner`** each have notes that decide how to read the answer: an empty cloud result is not proof of no cloud resources, the device diff flags probable renames and says when a snapshot cannot be diffed, and `ip_owner` finds the interface and VRF that own an address. Read `reference/kinds.md` (`fwdctl describe inspect-inventory reference/kinds.md`) before answering from any of those.

## Evidence

One `state` item holding the kind, the filters, the total row count, the window, and the rows.

## Output

The envelope in `schema/skill-result.schema.json`. `next_actions` point at the investigation the inventory suggests.

## Next actions

`investigate-reachability`, `inspect-vulnerabilities`, `investigate-collection-failure`, `check-network-compliance`.

## Running this skill

Give the inputs as one JSON object on stdin: `echo '{"network_id": "<id>"}' | fwdctl run inspect-inventory`. It needs the
`fwdctl` binary and `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD` in the environment. Read `status`
(`ok`, `failed`, `unknown`, `error`) and the `limits`; `unknown` is never a pass. Inputs are listed above and in `schema.json`.
