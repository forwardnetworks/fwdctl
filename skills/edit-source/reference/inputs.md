# edit-source: inputs

`network_id`, `object`, `action`, `name`, `definition`, `secret_file` or `secret_env`, `confirm`, `apply` (default false).

| `object` | `action` | `definition` (not secret) | Secret | Undo |
|---|---|---|---|---|
| `credential` | `create` | `type` (`CLI`, `HTTP`, `SNMP`), `name`, and by type: `username`, `kind`, `privilegeLevel`, `loginType`, `version` (`V2C`, `V3`), `port`, `timeoutSec`, `authType`, `privacyProtocol`, `authUsername`, `autoAssociate` | password; SNMP v2c `communityString`; v3 `password`, `privacyPassword` | delete it |
| `credential` | `update` | `type`; `name` is the credential id; any of `name`, `username`, `loginType`, `privilegeLevel`, `autoAssociate`, `privilegedModePasswordId` | optional: a new password, community string or key | update back (a rotated secret cannot be read back) |
| `credential` | `delete` | `type`; `name` is the credential id | | **none**; `confirm` = the id |
| `jump_server` | `create` | `host`, `port`, `username`, `auth` (`key`, `password`), `sshCert` | the key or password | delete it |
| `jump_server` | `update` | name = the id; any of `host`, `port`, `username`, `supportsPortForwarding`, `vrf`, `authenticationTimeoutSeconds`, `maxSessions`, `maxStartups`; `secret_is` (`password` default, or `sshKey`) says what an optional secret is | optional: a new password or key (a JSON file may add `sshCert`) | update back |
| `jump_server` | `delete` | name = the id | | **none**; `confirm` = the id |
| `proxy` | `create`, `update` | `name`, `host`, `port`, `protocol`, `disableCertChecking`; update's `name` is the id | none | update back |
| `classic_device` | `create`, `update`, `delete` | `name`, `host`, `type`, `port`, credential ids (`cliCredentialId`, `httpCredentialId`, `snmpCredentialId`), `jumpServerId`, `collectorId` (the numeric id, `C42` or `42`, of a collector in this org), `collect`, `note`; the `name` input = the device for update and delete | none (ids only) | delete or re-add; `confirm` = the device name for delete |
| `cloud_account` | `create`, `rotate`, `test`, `delete` | `type` (AWS, AZURE, GCP, ...), `name`, `collect`, `regions`, `proxyServerId`, `username` (AWS access key id), `clientId`, `tenant`, `clientEmail`, `privateKeyId`, `subscriptionIds`; `name` input = the account for rotate, test, delete | the AWS secret key or Azure client secret as text; GCP `privateKey` or an `apiKey` in a JSON file | delete a new one; rotate has none; `confirm` = the name for delete |
| `rapid7_source` | `create`, `update`, `delete` | `name`, `baseUrl`, `credentialId` (an existing credential), `disableSslValidation`, `collectionDisabled`, `reportNames`; update's `name` is the source and update REPLACES | none | update back; delete has none, `confirm` = the name |
| `controller_setup` | `create`, `update`, `delete` | create: `name`, `controllers`, `managedDevices` (each with `name`, `type`, `host`, credential ids, `jumpServerId`); update: `managedDevices` (REPLACES the list) | none (existing credential ids) | delete it; update back; delete has none, `confirm` = the name |
| `mist_setup` | `create`, `delete` | `name`, `region`, `apiKeyId` (an existing credential), `collect`, `collectorId`, `hosts`, `concurrency` | none | delete it; delete has none, `confirm` = the name |
| `schedule` | `create`, `update`, `delete` | `enabled`, `timeZone`, `daysOfTheWeek`, `times` or `periodInSeconds`, `startAt`, `endAt`; `name` = the schedule id for update and delete | none | replace or delete; `confirm` = the id for delete |

Detail, including the SNMP v3 secret file format: `reference/objects.md`.
