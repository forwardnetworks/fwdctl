---
name: edit-source
description: Adds devices to the collection list (pin to a collector), logins, jump servers, proxies; secrets from a file. Dry run unless apply: true. Use when onboarding devices.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "snapshots"
  summary: "device logins, jump servers"
  maturity: "1"
  class: write
  effect: "network"
  secrets: "true"
  reversible: "false"
  tools: "credentials, jump servers, proxies"
---

# edit-source

## Intent

Set up how Forward reaches devices: the logins it uses (CLI, SNMP, HTTP credentials), the jump servers and proxies in between, the devices to collect, and when collection runs (schedules). It touches no device and never shows a secret. `inspect-platform` lists what exists (names and ids only).

## Secrets

A secret (a password, SNMP community string, SSH key) is never put in the input. Give `secret_file` (a path, mode 600; a group-readable file is refused) or `secret_env` (an environment variable name). The text is the one secret, or for an object that has several (SNMP v3) a JSON object of named secrets. The value is not echoed, logged, kept in the operations log or returned; the undo says to enter it again. A definition that contains a secret-looking field is refused before anything is sent. This skill is interactive-only: do not run it unattended.

## Inputs

What each input means, and the fields per object and action: `fwdctl run edit-source --help`, or `reference/inputs.md`, `reference/objects.md`.

## Dry run

Without `apply: true` nothing changes, and no secret file is read. The result shows the object with `<secret>` where the secret goes, the undo, and the exact `confirm` when needed. With `apply: true` the secret is read, sent once, and the object is read back by name.

## Limits

The SDK has no proxy delete; a jump server changes with `update` (Forward re-handles the devices that go through it); a credential is changed in place with `update` (the fields given change, the rest stay; a secret is optional). A credential is attached to devices by `autoAssociate` or in the device configuration; this skill does not attach it.

## Evidence

One `state` item: `object`, `action`, `mode`, `before`, `after` (secrets as `<secret>`).

## Next actions

`inspect-platform` (areas `credentials`, `jump_servers`, `proxies`) to see the result; `inspect-collection` for the devices that use them.

## Running this skill

`echo '{"network_id":"N","object":"credential","action":"create","definition":{"type":"CLI","name":"ops","username":"admin"},"secret_file":"/path/to/pw"}' | fwdctl run edit-source` is the dry run; add `"apply": true` after it is accepted.
