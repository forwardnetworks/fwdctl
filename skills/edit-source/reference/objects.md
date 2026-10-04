# edit-source: objects, secrets and limits

## Contents
- The secret file
- credential
- jump_server
- proxy
- classic_device
- schedule
- cloud_account

## The secret file

One file or one environment variable per call. For CLI and HTTP credentials and jump servers it holds the single secret as plain text (a trailing newline is ignored). For SNMP v3 it holds a JSON object: `{"password": "<auth password>", "privacyPassword": "<privacy password>"}`; for SNMP v2c it is the community string as plain text. The file must not be readable by group or others (`chmod 600`). The value is read only when `apply` is true.

## credential

`type` CLI takes `username`, `kind` (omit for a login credential; Forward's other kinds are privilege escalation, expert and shell), `privilegeLevel`, `privilegedModePasswordId` (an id of a stored privileged-mode password, not a password) and `autoAssociate`. HTTP takes `username`, `loginType`, `autoAssociate`. SNMP takes `version` V2C or V3, `port`, `timeoutSec`, and for V3 `authUsername`, `authType` (MD5, SHA, SHA_256, SHA_384, SHA_512) and `privacyProtocol` (DES, AES_128, AES_192, AES_256); V3 requires a username, and a privacy password requires an auth type. Forward returns only an id for the stored secret. Delete needs the credential's id and type and `confirm` equal to the id.

## jump_server

A jump server authenticates by key (the secret is the private key; `sshCert` is an optional public certificate, not secret) or by password. `port` defaults to 22. The SDK can create one; it cannot update or delete one.

## proxy

A proxy has no secret. `create` needs `name`, `host`, `port` and `protocol`; `update` replaces all of a proxy's fields, so restate them. The SDK has no proxy delete.

## classic_device

A device in the list Forward collects (CLI/SNMP/HTTP devices). `create` needs `name` and `host`; credentials and the jump server are given by id (`inspect-platform` areas `credentials` and `jump_servers`). `update` changes host, type, port, the CLI and HTTP credential, `collect` and `note` (it patches); the SNMP credential, jump server and extra CLI credentials cannot be changed in place, so delete and re-add. `delete` removes it from the list (`confirm` = its name); past snapshots keep it. Nothing here touches the device.

## schedule

A collection schedule is days of the week (0 Sunday to 6 Saturday) plus either `times` (HH:mm) or `periodInSeconds` (with optional `startAt`, `endAt`), in a Forward time zone (UTC and about fifty regional IDs such as America/Chicago). `update` REPLACES the whole schedule: restate every field. A duplicate schedule is refused by Forward. Forward does not return the next run time.

## cloud_account

An AWS, Azure, GCP (and similar) account Forward collects. `create` takes the non-secret settings in `definition` and the secret from the secret file: plain text is the AWS secret access key or Azure client secret; a JSON object may carry `password`, `privateKey` (a GCP service-account key) or `apiKey`. `rotate` replaces the stored credentials of an existing account (the earlier secret cannot be read back); `test` asks the collector to try the connection and records a result per region (`inspect-collection` view `config` shows it); `delete` needs `confirm` equal to the name. Changing other settings of an existing account is done in the Forward UI: this skill does not restate a whole account, because a partial restatement can clear its credentials. Controller-managed (ACI, SD-WAN) and Mist setups are not covered here yet.
