---
name: edit-checks
description: Creates one check on a snapshot from a definition, or deactivates one. Dry run unless apply is true. Use when asked to add a policy check, turn a query into a check, or switch one off.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "compliance"
  summary: "create or deactivate a check"
  maturity: "3"
  class: write
  reversible: "false"
  tools: "checks"
---

# edit-checks

## Intent

Put a check into Forward, or take one out of service, without touching a device. Checks change verdicts and reports, so nothing happens
until `apply` is true, and the blast radius is stated up front.

## Inputs

`network_id`, `action` (`create` or `deactivate`). For `create`: `definition` (Forward's check definition: a predefined check type, or an
NQE query), optional `name`, `note`, `tags`, `priority`, `persistent`. For `deactivate`: `check_id`. Optional for both: `snapshot_id`
(default the newest processed one), `apply` (default false). See `schema.json`. Find predefined types and existing checks with
`inspect-checks`; validate an NQE query with `validate-nqe-query` first.

## Dry run

Without `apply: true` the skill changes nothing. It returns `mode: dry_run` with the change it would make:
- create: the name, definition and scope. A name already used on the snapshot gives **unknown** and creates nothing, so a check is
  never doubled.
- deactivate: the check's name, status and definition.
Show that to the person who asked before applying.

## Procedure

1. Resolve the snapshot. If there is none, **unknown**, nothing changed.
2. create: refuse a duplicate name. With `apply`, create the check **non-persistent** (local to the snapshot) unless `persistent: true`,
   read what Forward returns (id, status, violations) and report it.
3. deactivate: read the check first (a missing check is **unknown**). With `apply`, deactivate that one check only. The skill never
   deactivates a snapshot's checks wholesale.

## Undo

- A created check is undone by deactivating it: the applied change names the exact call. Its history stays readable.
- A deactivation cannot be undone through the API (there is no reactivate). The change carries the check's definition so it can be
  created again, and the finding says so. This is why the skill is not marked reversible.
- A `persistent` check is inherited by later snapshots and Predict runs; say that before applying it.

## Evidence

One `policy` item with the name, scope, mode and, once applied, the created id and its status and violations.

## Output

The envelope in `schema/skill-result.schema.json`, with `mode` and `changes`.

## Next actions

`inspect-checks` to read the result, `check-network-compliance` to evaluate a policy.

## Running this skill

Give the inputs as one JSON object on stdin: `echo '{"network_id": "<id>", "action": "deactivate", "check_id": "<id>"}' | fwdctl run edit-checks`.
It needs the `fwdctl` binary and `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD` in the environment. Read `status`
(`ok`, `failed`, `unknown`, `error`), `mode` and the `limits`; `unknown` is never a pass. Add `"apply": true` only after the dry run was accepted.
