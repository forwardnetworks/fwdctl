---
name: edit-workspace
description: Makes a temporary workspace network, adds endpoints to a workspace, or deletes one. Dry run unless apply is true. Use when trying a collection change away from the production network.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "snapshots"
  summary: "temporary workspace network"
  maturity: "1"
  class: write
  effect: "network"
  secrets: "true"
  reversible: "true"
  tools: "networks, endpoints"
---

# edit-workspace

## Intent

A workspace is a separate network under a production one, with its own sources, collections and snapshots; it changes nothing in the parent and expires on its own. It lets a
collection change (a new endpoint profile, an extra OID) be tried on a handful of sources without collecting the whole production network, which `edit-collection` would do.
This skill creates one, adds endpoints to it, and deletes it. It never touches a network that is not a workspace.

## Inputs

What each input means, and the fields per object and action: `fwdctl run edit-workspace --help`, or `reference/inputs.md`.

Read before applying (printed by `fwdctl run edit-workspace --help`, from `reference/inputs.md`): Credentials, What it does not do.

**Endpoints are not copied into a workspace, and credentials come across only when the org property `COPY_CREDENTIALS` allows it.**

## Dry run and procedure

Without `apply: true` nothing is changed. The skill reads the networks (and the parent's classic devices or endpoints), refuses what cannot work (a name in use, a device the parent lacks, creating
under a workspace, adding endpoints to a production network, a missing credential id, a wrong confirm name) and returns `mode: dry_run` with the exact payload as the change. On apply it makes the
call and reads the result back (the workspace is listed under its parent with its retention; each endpoint is listed; a deleted workspace is gone); a state that is not the requested one is **failed**.

## Undo

Create: delete the workspace (it also expires after its retention). Add endpoints: they go with the workspace. Delete: **not reversible** (its snapshots, sources and endpoints go with it); confirm the name and creator first.

## Next actions

`edit-collection` on the workspace, then `inspect-collection` and an NQE query over the new snapshot.

## Running this skill

Run `fwdctl run edit-workspace --help` for the inputs and an example, then give them as one JSON object on stdin: `fwdctl run edit-workspace < input.json`. It is a dry run unless the input has `"apply": true`; add that only after the dry run was accepted. Read `status` (`ok`, `failed`, `unknown`, `error`), `mode` and the `limits`; `unknown` is never a pass.

## Examples

```bash
echo '{"network_id": "<parent>", "create_workspace": {"name": "zz-test", "note": "temporary", "devices": ["<small device>"], "omissions": ["NQE_CHECKS"]}}' | fwdctl run edit-workspace
```
