# edit-platform areas

## Contents
- banners
- webhooks
- certificates
- access_labels
- backups
- integrations

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
