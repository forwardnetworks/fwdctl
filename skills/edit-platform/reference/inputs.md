# edit-platform: inputs

`area` and `action` (required), `name`, `definition` (the body, one object, never a secret), `secret_file` or `secret_env`, `confirm`, `apply` (default false).

A secret (an S3 secret key, a ServiceNow or Infoblox password, a license key) is never put in the input. Give `secret_file` (a path, mode 600; a group-readable file is refused) or `secret_env` (an environment variable name). It is read only on apply, sent once, and never echoed, logged or returned; the undo says to enter it again. A definition with a secret-looking field is refused. This skill is interactive-only: do not run it unattended.

| `area` | `action` | `name` | `definition` | Undo |
|---|---|---|---|---|
| `banners` | `create` | | `message`, `background_color`, `network_ids`, `enabled` | update with `enabled: false` (no delete) |
| `banners` | `update` | the banner id | any of the create fields | update back |
| `banners` | `delete` | the banner id | | **none**; `confirm` = the id |
| `webhooks` | `create` | | `name`, `url`, `description`, `enabled`, `disable_ssl_validation`, `event_params` | delete it |
| `webhooks` | `update` | the webhook | `name`, `description`, `url`, `enabled`, `disable_ssl_validation`, `event_params`, `template`; a new credential: `credential_type`, `credential_username` and the password as the secret | update back (a replaced credential cannot be read back) |
| `webhooks` | `delete` | the webhook | | **none**; `confirm` = the name |
| `certificates` | `add` | | `name`, `certificate` (PEM text) | delete it |
| `certificates` | `delete` | the certificate | | add it back |
| `certificates` | `apply` | | | **none**; restarts every collector; `confirm` = `apply` |
| `access_labels` | `create` | | `name`, `device_names`, `device_globs` | delete it |
| `access_labels` | `update` | the label name or id | any of the create fields | update back |
| `access_labels` | `delete` | the label name or id | | **none**; `confirm` = the name |
| `collection_settings` | `set` | `organization` | `max_device_authn_per_second`, `max_scan_connections_per_second`, `device_collection_timeout_minutes`, `command_delay_ms`, `per_device_concurrency_boost` | set back |
| `collection_settings` | `set` | a collector id or name | `concurrency`, `snmp_collection_concurrency` | set back |
| `saml` | `set` | | `custom_name`, `enabled`, `name`, `entity_id`, `sso_redirect_url`, `verification_cert` (PEM), `disable_authn_request_signing` | set back; `confirm` = `saml`; can lock users out |
| `licensing` | `apply` | | | secret = the signed license key; **none**; `confirm` = `license` |
| `organizations` | `create` | | `name`, `type`, `on_prem` | disable it |
| `organizations` | `rename` | the organization | `name` | rename back |
| `organizations` | `enable`, `disable` | the organization | | the opposite; `disable` needs `confirm` = the name |
| `organizations` | `delete` | the organization | | **none**; `confirm` = the name |
| `cve_index` | `upload` | | `path` (a .gz file), `sha256` (optional) | delete it |
| `cve_index` | `delete` | | | **none**; `confirm` = `cve_index` |
| `api_tokens` | `delete` | the token name (this login's own) | | **none**; `confirm` = the name |
| `integrations` | `set` | `servicenow` | `instance_url`, `username`, `enabled`, `auto_create`, `auto_create_impact`, `auto_create_urgency`, `auto_update`; secret = the password | set back |
| `integrations` | `delete` | `servicenow` | | **none**; `confirm` = `servicenow` |
| `integrations` | `create` | `infoblox` | `name`, `ip_address`, `username`; secret = the password | delete it |
| `integrations` | `update` | `infoblox` | `id` (the instance), `name`, `username`; optional secret = a new password (the address cannot change) | update back |
| `integrations` | `delete` | `infoblox` | `id` | **none**; `confirm` = the id |
| `backups` | `settings` | | `enabled`, `backup_time`, `num_days_to_retain`, `include_snapshots` (internal storage) | update back |
| `backups` | `s3_storage` | | `access_key`, `bucket_name`, `service_endpoint`, `disable_ssl_validation`, `certificate`, `write_only`, `disable_checksum_validation`; secret = the S3 secret key | update back (re-enter the key) |
| `backups` | `trigger` | | `name`, `include_snapshots` | **none**; `confirm` = `backup` |
| `backups` | `cancel` | | | **none**; `confirm` = `cancel` |
| `backups` | `delete` | the numeric backup id | | **none**; `confirm` = the id |

Detail: `reference/areas.md`.
