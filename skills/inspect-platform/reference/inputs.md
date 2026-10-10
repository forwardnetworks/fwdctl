# inspect-platform: inputs

`area` (required) and, for the areas that belong to a network, `network_id`. Optional: `name` (a substring of the object's name), `limit` (default 50, at most 500), `offset`.

| `area` | Belongs to | Shows |
|---|---|---|
| `credentials` | network | CLI, SNMP and HTTP credentials by id and name (never the password, key or community) |
| `jump_servers`, `proxies` | network | host, port, user and id |
| `collectors`, `collection_settings` | organization | each collector, its status, version and last connection, and `networks` (the networks it is attached to: one read per network, first 100 networks; the limits say how many were read); the organization's collection timeouts, retries and rates |
| `endpoint_profiles` | organization | every SNMP, CLI and HTTP endpoint profile (header values removed) |
| `schedules` | network | collection schedules |
| `cloud_setups` | network | cloud accounts, controller-managed and Mist setups, with regions and collect flags |
| `locations`, `tag_definitions` | network | locations, and the device tags defined on the network |
| `access_labels`, `api_tokens` | organization | device access labels; this login's own API tokens (names and dates) |
| `webhooks`, `banners`, `certificates`, `organizations`, `cve_index` | organization | as named; a webhook with its last test result |
| `org_properties` | organization (`org_id`, or `network_id` to find it) | every organization property: effective value and its source (org, global override or compiled default), plus the appserver build |
| `dashboards`, `scorecards`, `scorecard_trends` | network | custom dashboards (raw layout); scorecard definitions and the latest snapshot's scores; each scorecard's score over the last 90 days (first, latest, min, max, change). **Unpublished Forward API** |
| `integrations` | organization (`network_id` adds Rapid7) | ServiceNow, Infoblox and Rapid7, without passwords |
| `licensing`, `backups`, `saml` | organization | licenses without the key; backup settings and the last backup; SAML status |

Detail and what each area cannot show: `reference/areas.md` (`fwdctl describe inspect-platform reference/areas.md`).
