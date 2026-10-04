---
name: edit-source
description: Adds device logins and paths: CLI, SNMP, HTTP credentials, jump servers, proxies; secrets come from a file. Dry run unless apply is true. Use when onboarding devices.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
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

`network_id`, `object`, `action`, `name`, `definition`, `secret_file` or `secret_env`, `confirm`, `apply` (default false). See `schema.json`.

| `object` | `action` | `definition` (not secret) | Secret | Undo |
|---|---|---|---|---|
| `credential` | `create` | `type` (`CLI`, `HTTP`, `SNMP`), `name`, and by type: `username`, `kind`, `privilegeLevel`, `loginType`, `version` (`V2C`, `V3`), `port`, `timeoutSec`, `authType`, `privacyProtocol`, `authUsername`, `autoAssociate` | password; SNMP v2c `communityString`; v3 `password`, `privacyPassword` | delete it |
| `credential` | `delete` | `type`; `name` is the credential id | | **none**; `confirm` = the id |
| `jump_server` | `create` | `host`, `port`, `username`, `auth` (`key`, `password`), `sshCert` | the key or password | none through the API |
| `proxy` | `create`, `update` | `name`, `host`, `port`, `protocol`, `disableCertChecking`; update's `name` is the id | none | update back |
| `classic_device` | `create`, `update`, `delete` | `name`, `host`, `type`, `port`, credential ids (`cliCredentialId`, `httpCredentialId`, `snmpCredentialId`), `jumpServerId`, `collect`, `note`; the `name` input = the device for update and delete | none (ids only) | delete or re-add; `confirm` = the device name for delete |
| `cloud_account` | `create`, `rotate`, `test`, `delete` | `type` (AWS, AZURE, GCP, ...), `name`, `collect`, `regions`, `proxyServerId`, `username` (AWS access key id), `clientId`, `tenant`, `clientEmail`, `privateKeyId`, `subscriptionIds`; `name` input = the account for rotate, test, delete | the AWS secret key or Azure client secret as text; GCP `privateKey` or an `apiKey` in a JSON file | delete a new one; rotate has none; `confirm` = the name for delete |
| `schedule` | `create`, `update`, `delete` | `enabled`, `timeZone`, `daysOfTheWeek`, `times` or `periodInSeconds`, `startAt`, `endAt`; `name` = the schedule id for update and delete | none | replace or delete; `confirm` = the id for delete |

Detail, including the SNMP v3 secret file format: `reference/objects.md`.

## Dry run

Without `apply: true` nothing changes, and no secret file is read. The result shows the object with `<secret>` where the secret goes, the undo, and the exact `confirm` when needed. With `apply: true` the secret is read, sent once, and the object is read back by name.

## Limits

The SDK has no update or delete for jump servers and no proxy delete, and credentials cannot be edited in place here: delete and create, or change them in the UI. A credential is attached to devices by `autoAssociate` or in the device configuration; this skill does not attach it.

## Evidence

One `state` item: `object`, `action`, `mode`, `before`, `after` (secrets as `<secret>`).

## Next actions

`inspect-platform` (areas `credentials`, `jump_servers`, `proxies`) to see the result; `inspect-collection` for the devices that use them.

## Running this skill

`echo '{"network_id":"N","object":"credential","action":"create","definition":{"type":"CLI","name":"ops","username":"admin"},"secret_file":"/path/to/pw"}' | fwdctl run edit-source` is the dry run; add `"apply": true` after it is accepted.
