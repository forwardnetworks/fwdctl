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

## Credentials

Endpoints are **not** copied into a workspace. Credentials come across only when the organization property `COPY_CREDENTIALS` is `ENABLED` (or `ENABLED_FOR_ADMINS` and the creator is an admin); it
is an org-wide setting, not an option of the create call, and this skill only **reads** it (`inspect-environment` shows it too) and says in the dry run what it will do. When it is `ENABLED`, **all** of the
parent's secrets are copied with the same ids, so an added endpoint can keep the parent endpoint's credential id (the default when `credential_id` is omitted; a missing one shows as a connectivity
error on the first collection). When it is `DISABLED`, a person creates the credential **inside the workspace in the Forward UI** and its id is the `credential_id`. No secret is ever an input
here or shown in a result.

## Dry run and procedure

Without `apply: true` nothing is changed. The skill reads the networks (and the parent's classic devices or endpoints), refuses what cannot work (a name in use, a device the parent lacks, creating
under a workspace, adding endpoints to a production network, a missing credential id, a wrong confirm name) and returns `mode: dry_run` with the exact payload as the change. On apply it makes the
call and reads the result back (the workspace is listed under its parent with its retention; each endpoint is listed; a deleted workspace is gone); a state that is not the requested one is **failed**.

## Undo

Create: delete the workspace (it also expires after its retention). Add endpoints: they go with the workspace. Delete: **not reversible** (its snapshots, sources and endpoints go with it); confirm the name and creator first.

## What it does not do

It does not collect (`edit-collection` with the workspace as `network_id` does, collecting only what the workspace holds), create credentials, or set the `COPY_CREDENTIALS` property (read it with `inspect-environment`; changing it is an organization admin's setting). Jump servers and proxies are
not carried over for added endpoints.

## Next actions

`edit-collection` on the workspace, then `inspect-collection` and an NQE query over the new snapshot.

## Running this skill

`echo '{"network_id": "<parent>", "create_workspace": {"name": "zz-test", "note": "temporary", "devices": ["<small device>"], "omissions": ["NQE_CHECKS"]}}' | fwdctl run edit-workspace`.
Read `status`, `mode`, the `changes` and the `limits`; `unknown` is never a pass. Add `"apply": true` only after the dry run was accepted.
