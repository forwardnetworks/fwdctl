# inspect-platform: inputs

`area` (required) and, for the areas that belong to a network, `network_id`. Optional: `name` (a substring of the object's name), `limit` (default 50, at most 500), `offset`.

| `area` | Belongs to | Shows |
|---|---|---|
| `credentials` | network | CLI, SNMP and HTTP credentials by id and name (never the password, key or community) |
| `jump_servers`, `proxies` | network | host, port, user and id |
| `collectors`, `collection_settings` | organization | each collector, its status, version and last connection, and `networks` (the networks it is attached to: one read per network, first 100 networks; the limits say how many were read); the organization's collection timeouts, retries and rates; `collection_settings` also shows the organization's `disabledCommands` (built-in collection commands switched off, by device type and version range; edit with `edit-platform`) |
| `endpoint_profiles` | organization | every SNMP, CLI and HTTP endpoint profile (header values removed) |
| `schedules` | network | collection schedules |
| `controller_setups` | no | controller-managed setups of every network and workspace: controllers, hosts, jump servers, logins (ids), last test |
| `cloud_setups` | network | cloud accounts, controller-managed and Mist setups, with regions and collect flags |
| `custom_command_groups` | network | the custom command and OID groups of the network; `command` keeps the groups holding a command |
| `locations`, `tag_definitions` | network | locations, and the device tags defined on the network |
| `access_labels`, `api_tokens` | organization | device access labels; this login's own API tokens (names and dates) |
| `webhooks`, `banners`, `certificates`, `organizations`, `cve_index` | organization | as named; a webhook with its last test result; `banners` also reads the system announcement and the critical states |
| `org_properties` | organization (`org_id`, or `network_id` to find it) | every organization property: effective value and its source (org, global override or compiled default), plus the appserver build |
| `dashboards`, `scorecards`, `scorecard_trends` | network | custom dashboards (raw layout); scorecard definitions and the latest snapshot's scores; each scorecard's score over the last 90 days (first, latest, min, max, change). **Unpublished Forward API** |
| `integrations` | organization (`network_id` adds Rapid7) | ServiceNow, Infoblox and Rapid7, without passwords |
| `licensing`, `backups`, `saml` | organization | licenses without the key; backup settings and the last backup; SAML status |

Detail and what each area cannot show: `reference/areas.md` (`fwdctl describe inspect-platform reference/areas.md`).

`view` is optional. `effective` (area `collection_settings` only) lists every collection setting for the organization and each collector with Forward's default, whether it is set, and whether it differs.

`command` (area `custom_command_groups` only): keep the groups whose commands contain this text, ignoring case.

`view: effective` with area `custom_command_groups` takes `name`, the classic device, and lists the custom command groups that apply to it.
