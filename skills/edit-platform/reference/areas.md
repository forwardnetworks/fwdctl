# edit-platform areas

## Contents
- banners
- webhooks
- certificates
- access_labels
- backups
- integrations
- collection_settings, saml, api_tokens
- licensing and organizations
- cve_index

## banners
A banner shows a message on the chosen networks. `network_ids` is required and `background_color` is a CSS color. The API has no banner delete: disable one with `update` and `enabled: false`. The banner id is in `inspect-platform` area `banners`.

## webhooks
Preview: Forward has not published the webhook schemas, and the feature is gated by deployment. `event_params` depends on the event type; copy it from an existing webhook read with `inspect-platform`. Credentials and templates are not set here. Deleting needs `confirm` because the credentials and template cannot be recovered.

## certificates
`certificate` is PEM text (a public certificate, not a key). Adding one does nothing on the collectors until `apply`, which pushes every certificate and restarts each collector that supports it; a second push while one runs is treated as already underway. Do not push on a schedule.

## access_labels
A device access label names devices (`device_names`, exact) or patterns (`device_globs`); access groups use it to narrow what their members can see (`edit-access`). A label with neither is refused. Deleting a label that groups use removes their restriction, so read the groups with `inspect-access` first.

## backups
`cancel` stops the running backup or restore. `delete` removes one backup for good, from the storage it is in. The backup settings, the S3 storage settings and starting a backup go through Forward's backup service, which the SDK allows for a service principal only, so they are not offered here; use the Forward UI. The calls here need the system administrator role; without it the result is an error from Forward, not an empty answer.

## integrations
`name` picks the integration. ServiceNow: `set` writes the whole configuration (the API replaces it, so give every field you want kept) with the password from the secret; `delete` removes it. Infoblox: `create` adds an instance; the API has no update or delete, so a mistake is fixed in the Forward UI. The password is not read back, and setting does not test that the remote system accepts the login. Rapid7 sources are per network and are not changed here yet. Integration reads: `inspect-platform` area `integrations`.

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
