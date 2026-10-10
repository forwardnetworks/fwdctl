---
name: edit-platform
description: Changes org admin settings (banners, webhooks, certificates, labels, integrations, backups). Dry run unless apply. Use when setting up the org.
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

What each input means, and the fields per object and action: `fwdctl run edit-platform --help`, or `reference/inputs.md`, `reference/areas.md`.

## Dry run

Without `apply: true` nothing changes. The result shows before, after and the undo, and names the exact `confirm` an irreversible action needs.

## Procedure

1. Read the current object. A missing one is **unknown**, nothing changed; a name that exists is refused.
2. Dry run returns the plan. With `apply: true`: one call, then read back; a read-back that does not show the change is **failed**.

## Limits

Backups need the system administrator role (Forward answers 403 otherwise) and exist only on Kubernetes deployments (elsewhere the result says the deployment does not serve the route). A trigger does not wait. Restore is not here; Rapid7 sources are in `edit-source` (they belong to a network); an uploaded CVE index is applied in the background and not waited for; licensing, SAML and organizations are for a system or platform administrator; a token cannot be created here because its secret would be returned; a trigger starts the backup and does not wait. Deleting an access label widens the access of every group that used it.

Webhook credentials and templates are not set here (webhook schemas are unpublished by Forward). A certificate is not trusted by collectors until `apply` pushes it, and a push does not wait for the collectors, so completion is not proven.

## Evidence

One `state` item: `object`, `action`, `mode`, `before`, `after`.

## Next actions

`inspect-platform` (areas `banners`, `webhooks`, `certificates`, `access_labels`, `backups`, `integrations`, `collection_settings`, `saml`, `api_tokens`, `licensing`, `organizations`, `cve_index`) to see the result.

## Running this skill

`echo '{"area":"webhooks","action":"update","name":"ops","definition":{"enabled":false}}' | fwdctl run edit-platform` is the dry run; add `"apply": true` after it is accepted.
