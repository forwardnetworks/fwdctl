---
name: inspect-platform
description: Reads how Forward is set up, secrets removed: credentials, jump servers, collectors, cloud setups, webhooks, licensing, SAML. Use when asked what is configured.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "access"
  summary: "platform setup, no secrets"
  maturity: "1"
  tools: "credentials, jump servers, proxies, collectors, schedules, cloud setups, locations, tags, webhooks, integrations, licensing, backups, banners, certificates, saml, organizations"
---

# inspect-platform

## Intent

Answer "what is set up on this Forward" for the things an administrator maintains, one `area` at a time, without ever returning a secret. It reads only. The `edit-*` skills change these objects.

## Inputs

What each input means, and the fields per object and action: `fwdctl run inspect-platform --help`, or `reference/inputs.md`, `reference/areas.md`.

## Procedure

1. Check `area` and that `network_id` is given exactly when the area belongs to a network.
2. Read it with the Go SDK. Marshal the rows, remove every field whose name says it is a secret, shorten long bodies, filter by `name`, page.
3. Decide: rows returned are **ok**; an empty list is **unknown** (what this login was allowed to see, not proof nothing exists); a 403 says which permission or role is missing (`inspect-access` view `explain`).

## Limits worth stating

Removal is by field name and errs on the side of hiding, so an id that happens to be stored under a password-like name is also hidden. Nothing here tests that a credential or account works. Some areas need an organization administrator or a specific permission (licensing, backups, SAML, other users' tokens).

## Evidence

One `state` item per call: `area`, `total`, `offset` and the rows.

## Output

Status ok or unknown, a finding ("N credentials"), the rows, and limits that say how many secret values were removed.

## Next actions

`inspect-access` for roles and a refused read; `inspect-collection` for how collection uses what is listed; the matching `edit-*` skill to change it.

## Running this skill

`echo '{"network_id":"<id>","area":"credentials"}' | fwdctl run inspect-platform`, or `echo '{"area":"webhooks"}' | fwdctl run inspect-platform` for an organization area.
