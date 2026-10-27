# edit-platform areas

## Contents
- banners
- collectors
- endpoint_profiles
- webhooks
- certificates
- access_labels
- backups
- integrations
- collection_settings, saml, api_tokens
- licensing and organizations
- cve_index

## banners
A banner shows a message on the chosen networks. `network_ids` is required and `background_color` is one of RED, ORANGE, YELLOW, GREEN or PURPLE (Forward's banner colors, as in the UI's banner manager; case does not matter; a CSS color is refused). `delete` removes one for good (`confirm` = the id); `update` with `enabled: false` hides it instead. The banner id is in `inspect-platform` area `banners`.

## webhooks
Preview: Forward has not published the webhook schemas, and the feature is gated by deployment. `event_params` depends on the event type; copy it from an existing webhook read with `inspect-platform`, or for a first webhook use the shape a field session derived from Forward's source and had accepted live: `{"type":"SNAPSHOT_READY","networkIds":["<network id>"]}`. **An empty `networkIds` list means every network in the organization**, so an org-wide webhook would send events about networks you did not mean to expose: always list the networks. The failure events (`INTENT_VERIFICATION_FAILURE`, `NQE_VERIFICATION_FAILURE`) are scoped by check directories (`networkIdToCheckDirs`, `networkIdToFwdCheckDirs`, `networkIdToOrgCheckDirs`; not tried). A credential is set by an `update`, not by `create`: definition `credential_type` (for example `BASIC_AUTH`) and `credential_username`, with the password from `secret_file` or `secret_env`. So a webhook to a receiver that requires authentication is two steps: `create`, then `update` with the credential. Forward tests the connection when a webhook is created and again when the url, SSL validation or credential changes, so the first test fails with 401 against an authenticated receiver until the update lands. Templates are not set here. Deleting needs `confirm` because the credentials and template cannot be recovered.

## certificates
`certificate` is PEM text (a public certificate, not a key). Adding one does nothing on the collectors until `apply`, which pushes every certificate and restarts each collector that supports it; a second push while one runs is treated as already underway. Do not push on a schedule.

## access_labels
A device access label names devices (`device_names`, exact) or patterns (`device_globs`); access groups use it to narrow what their members can see (`edit-access`). A label with neither is refused. Deleting a label that groups use removes their restriction, so read the groups with `inspect-access` first.

## backups
`cancel` stops the running backup or restore. `delete` removes one backup for good, from the storage it is in. `settings` changes the scheduled backup on internal storage. `s3_storage` changes the S3 target; its secret key is write-only (never read back or returned), so the read-back checks the other fields and does not prove the key works. `trigger` starts a backup now and returns without waiting (`confirm: backup`). All need the system administrator role, and backups exist only on Kubernetes deployments; elsewhere Forward does not map the routes and the result says so.

## integrations
`name` picks the integration. ServiceNow: `set` writes the whole configuration (the API replaces it, so give every field you want kept) with the password from the secret; `delete` removes it. Infoblox: `create` adds an instance; `update` and `delete` take `definition.id` (the numeric instance id from `inspect-platform`); an instance's address cannot be edited, so change it by delete and create. `delete` needs `confirm` = the id. The password is not read back, and setting does not test that the remote system accepts the login. Rapid7 sources are per network and are not changed here yet. Integration reads: `inspect-platform` area `integrations`.

## collection_settings
`organization` changes the limits every collection in the organization runs under (authentication and scan rates, the per-device timeout, the delay between commands); a collector id or name changes that collector's concurrency. Read the current values first with `inspect-platform` area `collection_settings`. A limit set too tight makes collections slow or time out; set the earlier values back to undo.

## saml
Replaces the single sign-on configuration. Enabled settings need `entity_id`, `sso_redirect_url` and the identity provider's certificate as PEM text. A wrong value locks people out of single sign-on: keep a session that signs in with a local password open until a sign-in through the identity provider works. `confirm` is `saml`. The result does not prove that sign-in works, only that Forward stored the settings.

## api_tokens
Deletes one of this login's own API tokens by name. Nothing that used it can sign in afterwards, and if this session signs in with that token the limits say so. Creating a token is not offered: Forward returns the new secret once and these skills never return a secret, so create tokens in the Forward UI.

## licensing and organizations
`licensing apply` reads the signed license key from the secret, asks Forward to decode it (a read, done even in the dry run so the plan shows the tier and expiry) and applies it with `confirm: license`. The key is never returned. It does not remove earlier licenses, and the read-back only checks that a license is present.
`organizations` works on the organizations of a multi-organization deployment and needs the platform administrator role; a login without it gets Forward's permission error. `disable` stops that organization's users from signing in and deletes nothing. `delete` removes the organization with its networks, snapshots and users and needs `confirm` equal to its name; read what it holds first. A host that embeds the skills can forbid it (it sets `Session.OrganizationDeleter`); the result then says the host does not allow deleting organizations.

## cve_index
The vulnerability index the vulnerability skills match against. `upload` replaces it with a gzip file (for an installation that cannot reach the vendor feed; the file is read on apply, with the optional `sha256` checked); `delete` goes back to the index bundled with Forward, and is refused as "nothing to delete" when the bundled one is already in use. Forward accepts either change and applies it in the background, so completion is not proven here: read `inspect-platform` area `cve_index` for the digest.

## collectors
Register a new collector (its credential goes only to the `env_file` you name) or delete one. Standing one up end to end, with the run, wait and attach steps, is in `collectors.md`. Attaching it to a network is `edit-network` object `collector`.

## endpoint_profiles
A CLI endpoint profile says which commands Forward runs on the endpoints that use it: the predefined `UNIX` set (ifconfig, netstat -ln, hostname, uname -a) and/or custom commands. It is organization-wide. Endpoints are attached to it with `edit-source` object `endpoint` (`profileId`). Forward collects a profile only if every command in it is on the organization's approved list; the dry run reads that and names the unapproved ones. `update` replaces a CLI profile's `command_sets` and/or `custom_commands` in place, for every endpoint that uses it in any network. `create` takes the new profile's name as the top-level `name` or as `definition.name` (give one, or both if they agree). SNMP and HTTP profiles are created and updated here too (`definition.type` SNMP or HTTP): SNMP takes `detector_oid` and `detector_patterns` (the OID Forward reads to recognise the device, and the patterns its value must match; both required), `name_detector_oid` and `name_detector_patterns` (where the device name comes from), `oid_sets`, `custom_oids` `[{name, oid}]` (the values to collect, for example ENTITY-MIB OIDs) and `response_timeout_sec`; HTTP (GET only, Basic auth or none) takes `https`, `auth_type` (`NONE` or `BASIC_AUTH`), `headers`, `detector_uri`, `detector_patterns` and `endpoints` `[{name, path}]` (at least one; results land in `network.endpoints` as `httpResults`, SNMP as `snmpOutputs`, CLI as `cliCommandResponses`, readable with `fwdctl nqe run`; `fwdctl context schema httpResults` lists the fields). A dry run saves nothing; apply saves the profile, which is not tried: a connectivity test of an endpoint that uses it shows whether it detects and collects (`inspect-collection` view `connectivity`). `update` of an SNMP or HTTP profile replaces the fields given. `edit-endpoint-profile` still copies an existing SNMP profile and moves endpoints onto it in one step.

## collection_settings: disabled commands

`set` on `organization` with `disable_commands` or `enable_commands` switches a built-in collection command off or on **org-wide**, for a device type (`panos_ssh`, `cisco_ios_xe_ssh`, `f5_ssh`, ...) and a version range (default all versions). Forward replaces the whole map on a patch, so each entry is added or removed by reading the map, changing a copy and sending all of it back, read again right before the write (there is no compare-and-swap). The command is a regular expression that must match the whole command text in lower case (the pattern is lowercased too, so a class like `\S` becomes `\s`: write plain lower-case literals; a dry run warns about upper case), for example `request system external-list show .*`. It takes effect at the next collection and does not change snapshots already collected. A device whose REQUIRED command is disabled shows the collection error `COMMAND_DISABLED` and, per Forward staff, is left out of path search: check `investigate-collection-failure` view `devices` after the next collection. Disabling the PAN-OS external-list show command does not stop the collector downloading each list's URL itself. Forward has it from 26.8.3; an older build refuses it. `inspect-platform` area `collection_settings` shows the current map (`disabledCommands`).

