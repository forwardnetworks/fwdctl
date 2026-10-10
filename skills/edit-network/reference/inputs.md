# edit-network: inputs

`object` and `action` (required), `network_id` (not for creating a network), `name`, `definition` (the body, one object), `confirm`, `apply` (default false).

| `object` | `action` | `name` | `definition` | Undo |
|---|---|---|---|---|
| `network` | `create` | the new network's name | | delete it |
| `network` | `update` | | `name`, `note`, `retention_days` | update back |
| `network` | `delete` | | | **none**; `confirm` = `network_id` |
| `location` | `create` | | `name`, `lat`, `lng`, `city`, `adminDivision`, `country` | delete it |
| `location` | `update` | the location name or id | any create field, `deviceGlobs` | update back |
| `location` | `delete` | the location name or id | | **none**; `confirm` = the location id |
| `location` | `assign` | | `{"<device name>": "<location id>"}` | assign back |
| `cluster` | `create` | | `location_id`, `name`, `devices` | delete it |
| `cluster` | `update` | the cluster | `location_id`, new `name`, `devices` | update back |
| `cluster` | `delete` | the cluster | `location_id` | **none**; `confirm` = the cluster name |
| `tag` | `update` | the tag | `name` (rename), `color` | update back |
| `tag` | `delete` | the tag | | **none**; `confirm` = the tag name |
| `collector` | `assign` | attach a registered collector to the network (needs `network_id`) | `definition` {`username`: the collector's username, from `inspect-platform` area `collectors`} | attach the previous collector again; **not reversible here** when the network had none (no detach in the SDK) |

Detail, including what each read and what the API cannot do: `reference/objects.md`.
