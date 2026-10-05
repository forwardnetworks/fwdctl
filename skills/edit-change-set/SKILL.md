---
name: edit-change-set
description: Builds a Predict change set from CLI commands or BGP advertisements and optionally predicts it, touching no device. Dry run unless apply is true. Use when staging or testing a change.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "change"
  summary: "stage a Predict change set"
  maturity: "3"
  class: write
  effect: "network"
  secrets: "false"
  reversible: "false"
  tools: "predict change sets"
---

# edit-change-set

## Intent

Turn "what if we did X" into a predicted, checkable answer. Predict works on Forward's copy of the network: the draft, its validation and
its prediction reach no device. The skill **authors no configuration**. It stages exactly the commands and advertisements it is given,
so where they come from (a person, an approved ticket) is on the caller.

## Inputs

`network_id`, `name`. Then `devices` (a list of `{device, commands}`, commands as CLI text) and/or `bgp_advertisements` (`device`,
`external_peer`, `prefix`, `next_hop`, optional `vrf`, `type`, `origin`, `local_pref`, `as_path`, `med`, `communities`). Optional:
`description`, `snapshot_id` (the base; default the newest processed one), `run` (predict it; needs `apply`), `apply` (default false).
`firewall_rules` stages security rule changes on a firewall (PAN-OS and similar): `{op: add, device, rule: {name, action, sourceZones, destinationZones, sourceAddresses, destinationAddresses, applications, services}, predecessor}` or `{op: remove, device, rule_id}`, in rulebase `scope_id` (default LOCAL) and `rulebase_id` (default PRIMARY); the result shows each firewall's staged rule diff, and `verify-change` predicts and judges it. See `schema.json`. Model new BGP originations with `bgp_advertisements`: Predict discards CLI `network` statements.

## Dry run

Without `apply: true` nothing is created. The skill returns `mode: dry_run` with the change set it would build (name, base snapshot,
devices) and says the commands cannot be validated until the draft exists. Show that before applying.

## Procedure

1. Resolve the base snapshot. A change set with the same name on the same base is left alone: **unknown**, nothing created.
2. With `apply`: create the draft, then validate every device's commands with Forward. Any rejected line, or a validator that cannot
   run, **fails** the skill and the draft is deleted again; nothing half-built is left. The finding lists the rejected lines.
3. Stage the validated commands and advertisements.
4. With `run` as well: run the prediction and wait for it. Without it the limits say the change was not predicted.

An empty validation result is not proof the commands are right: the validator only knows the commands in its grammar.

## Undo

- The draft is undone by deleting the change set; the applied change names its id. Nothing reached a device, so there is nothing else to
  restore.
- A prediction cannot be undone: the predicted snapshot stays after the draft is deleted. That is why `run` is a separate switch and
  the skill is not marked reversible.

## Evidence

One `predict` item: the plan, the `change_set_id`, any `command_errors`, and the `predicted_snapshot_id` when run.

## Output

The envelope in `schema/skill-result.schema.json`, with `mode` and `changes`.

## Next actions

`verify-change` with `view: describe` to read it back, `verify-change` to compare the prediction with the base, `verify-change` with `view: impact` for who is reached.

## Running this skill

Give the inputs as one JSON object on stdin: `echo '{"network_id": "<id>", "name": "open 443", "devices": [{"device": "fw1", "commands": "..."}]}' | fwdctl run edit-change-set`.
It needs the `fwdctl` binary and `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD` in the environment. Read `status`
(`ok`, `failed`, `unknown`, `error`), `mode` and the `limits`; `unknown` is never a pass. Add `"apply": true` only after the dry run was accepted.
