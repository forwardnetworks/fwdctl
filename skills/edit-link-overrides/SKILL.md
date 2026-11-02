---
name: edit-link-overrides
description: Adds or removes a snapshot's manual and suppressed links, showing the change. Dry run unless apply is true. Use when adding an undiscovered link, ignoring a wrong one or undoing an override.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "edge-synthetic"
  summary: "add or remove link overrides"
  maturity: "2"
  class: write
  effect: "network"
  secrets: "false"
  reversible: "false"
  tools: "topology"
---

# edit-link-overrides

## Intent

Correct a snapshot's topology by hand: a **present** override adds a link Forward did not discover (a device it cannot see, a link with no
discovery protocol), an **absent** override suppresses a link that Forward found but that is wrong. It changes what Forward models for that
snapshot, and no device.

## Inputs

Every input, what it means and what to read before applying: `fwdctl run edit-link-overrides --help` (from `reference/inputs.md`). Read before applying: Undo, Staged mode.

**Writing overrides invalidates the snapshot and later ones (UNPROCESSED, not reprocessed for you): reprocess only after asking, one snapshot at a time.**

## Dry run

Without `apply: true` nothing is changed. The skill reads the snapshot's current overrides and returns `mode: dry_run` with one change that
holds the number of edits and, in the evidence, the exact edit and the override counts before and after (not the lists: read them with
`inspect-topology` kind `link_overrides`). Show it to the person who asked before applying.

The links to add are checked against the snapshot with the same derivation as `inspect-topology` (`ports_exist`): an `add_present` or
`add_absent` naming a device or interface the snapshot does not have is **refused** (status failed, nothing sent, dry run or applied)
unless `force: true`. The dry run also notes an `add_present` link that is already discovered and an `add_absent` link that is not in
the topology. If the snapshot's model could not be read, the limits say the ports were not checked and nothing is blocked.

## Procedure

1. Find the snapshot in the network; if it is not there the answer is **unknown** and nothing is changed.
2. Read the current overrides and validate the links to add. Drop the edits that change nothing (a link already present, a removal of one that is not there).
3. Nothing left: **ok**, nothing changed. Dry run: return the plan. With `apply: true`: send the edit, read the overrides back; any edit not in the requested state is **failed**, never ok.

## Evidence

One `topology` item with `snapshot_id`, `edit`, the counts `present_before`, `absent_before`, `present_after`, `absent_after`, `problems` and `notes` when there are any, `mode` and, once applied, `held`.

## Output

The envelope in `schema/skill-result.schema.json`, with `mode` and `changes`.

## Next actions

`inspect-topology` to see the links; `verify-change` to check what the topology change does to reachability and checks.

## Running this skill

Run `fwdctl run edit-link-overrides --help` for the inputs and an example, then give them as one JSON object on stdin: `fwdctl run edit-link-overrides < input.json`. It is a dry run unless the input has `"apply": true`; add that only after the dry run was accepted. Read `status` (`ok`, `failed`, `unknown`, `error`), `mode` and the `limits`; `unknown` is never a pass.

## Examples

```bash
echo '{"network_id": "<id>", "snapshot_id": "<id>", "add_present": [{"port1": "r1 Gi0/1", "port2": "sw1 Gi0/24"}]}' | fwdctl run edit-link-overrides
```

