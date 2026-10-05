---
name: edit-source
description: Adds device logins and paths: CLI, SNMP, HTTP credentials, jump servers, proxies; secrets come from a file. Dry run unless apply is true. Use when onboarding devices.
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

`network_id`, `object`, `action`, `name`, `definition`, `secret_file` or `secret_env`, `confirm`, `apply` (default false). See `schema.json`.

| `object` | `action` | `definition` (not secret) | Secret | Undo |
|---|---|---|---|---|
| `credential` | `create` | `type` (`CLI`, `HTTP`, `SNMP`), `name`, and by type: `username`, `kind`, `privilegeLevel`, `loginType`, `version` (`V2C`, `V3`), `port`, `timeoutSec`, `authType`, `privacyProtocol`, `authUsername`, `autoAssociate` | password; SNMP v2c `communityString`; v3 `password`, `privacyPassword` | delete it |
| `credential` | `update` | `type`; `name` is the credential id; any of `name`, `username`, `loginType`, `privilegeLevel`, `autoAssociate`, `privilegedModePasswordId` | optional: a new password, community string or key | update back (a rotated secret cannot be read back) |
| `credential` | `delete` | `type`; `name` is the credential id | | **none**; `confirm` = the id |
| `jump_server` | `create` | `host`, `port`, `username`, `auth` (`key`, `password`), `sshCert` | the key or password | delete it |
| `jump_server` | `update` | name = the id; any of `host`, `port`, `username`, `supportsPortForwarding`, `vrf`, `authenticationTimeoutSeconds`, `maxSessions`, `maxStartups`; `secret_is` (`password` default, or `sshKey`) says what an optional secret is | optional: a new password or key (a JSON file may add `sshCert`) | update back |
| `jump_server` | `delete` | name = the id | | **none**; `confirm` = the id |
| `proxy` | `create`, `update` | `name`, `host`, `port`, `protocol`, `disableCertChecking`; update's `name` is the id | none | update back |
| `classic_device` | `create`, `update`, `delete` | `name`, `host`, `type`, `port`, credential ids (`cliCredentialId`, `httpCredentialId`, `snmpCredentialId`), `jumpServerId`, `collect`, `note`; the `name` input = the device for update and delete | none (ids only) | delete or re-add; `confirm` = the device name for delete |
| `cloud_account` | `create`, `rotate`, `test`, `delete` | `type` (AWS, AZURE, GCP, ...), `name`, `collect`, `regions`, `proxyServerId`, `username` (AWS access key id), `clientId`, `tenant`, `clientEmail`, `privateKeyId`, `subscriptionIds`; `name` input = the account for rotate, test, delete | the AWS secret key or Azure client secret as text; GCP `privateKey` or an `apiKey` in a JSON file | delete a new one; rotate has none; `confirm` = the name for delete |
| `rapid7_source` | `create`, `update`, `delete` | `name`, `baseUrl`, `credentialId` (an existing credential), `disableSslValidation`, `collectionDisabled`, `reportNames`; update's `name` is the source and update REPLACES | none | update back; delete has none, `confirm` = the name |
| `controller_setup` | `create`, `update`, `delete` | create: `name`, `controllers`, `managedDevices` (each with `name`, `type`, `host`, credential ids, `jumpServerId`); update: `managedDevices` (REPLACES the list) | none (existing credential ids) | delete it; update back; delete has none, `confirm` = the name |
| `mist_setup` | `create`, `delete` | `name`, `region`, `apiKeyId` (an existing credential), `collect`, `collectorId`, `hosts`, `concurrency` | none | delete it; delete has none, `confirm` = the name |
| `schedule` | `create`, `update`, `delete` | `enabled`, `timeZone`, `daysOfTheWeek`, `times` or `periodInSeconds`, `startAt`, `endAt`; `name` = the schedule id for update and delete | none | replace or delete; `confirm` = the id for delete |

Detail, including the SNMP v3 secret file format: `reference/objects.md`.

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
