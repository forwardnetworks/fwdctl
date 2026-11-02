---
name: edit-platform
description: Changes org admin settings: banners, webhooks, certificates, labels, integrations, backups, collection settings, endpoint profiles. Dry run unless apply. Use when setting up the org.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "inventory-topology"
  summary: "org admin settings"
  maturity: "1"
  class: write
  effect: "org"
  secrets: "true"
  reversible: "false"
  tools: "banners, webhooks, trusted certificates, device access labels, backups, integrations"
---

# edit-platform

## Intent

Change what an administrator sets up for the whole organization, not for one network. It touches no device and no network. Read the same areas with `inspect-platform`.

## Inputs

What each input means, and the fields per object and action: `fwdctl run edit-platform --help`, or `reference/inputs.md`, `reference/areas.md`. Standing up a new collector (register, run, wait, attach): `reference/collectors.md`.

Read before applying (printed by `fwdctl run edit-platform --help`, from `reference/inputs.md`): Limits.

**Backups need the system administrator role and Kubernetes; a trigger does not wait; a pushed certificate is not proven trusted by collectors.**

## Dry run

Without `apply: true` nothing changes. The result shows before, after and the undo, and names the exact `confirm` an irreversible action needs.

## Procedure

1. Read the current object. A missing one is **unknown**, nothing changed; a name that exists is refused.
2. Dry run returns the plan. With `apply: true`: one call, then read back; a read-back that does not show the change is **failed**.

## Evidence

One `state` item: `object`, `action`, `mode`, `before`, `after`.

## Next actions

`inspect-platform` (areas `banners`, `webhooks`, `certificates`, `access_labels`, `backups`, `integrations`, `collection_settings`, `saml`, `api_tokens`, `licensing`, `organizations`, `cve_index`) to see the result.

## Running this skill

Run `fwdctl run edit-platform --help` for the inputs and an example, then give them as one JSON object on stdin: `fwdctl run edit-platform < input.json`. It is a dry run unless the input has `"apply": true`; add that only after the dry run was accepted. Read `status` (`ok`, `failed`, `unknown`, `error`), `mode` and the `limits`; `unknown` is never a pass.

## Examples

```bash
echo '{"area":"webhooks","action":"update","name":"ops","definition":{"enabled":false}}' | fwdctl run edit-platform
```
