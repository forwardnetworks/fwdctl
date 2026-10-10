# inspect-topology: inputs

`network_id` and `kind`:

| `kind` | Returns | Narrow with |
|---|---|---|
| `links` | port pairs, each written `<device> <interface>` | `device` (links with an end on it) |
| `locations` | id, name, city, admin division, country, lat/lng, device globs, and the devices in each (assigned, anchored, matched by glob) with a count of devices in no location | nothing |
| `tags` | tag name and the devices that carry it | `device` (tags on it) |
| `zones` | each device and the security zones Forward derived for it (`zones`, `zone_count`); use it to resolve zone membership before testing segmentation | `device` (one device by exact name) |
| `aliases` | alias name, type (hosts, devices, interfaces, headers, logical network) and its definition | nothing |
| `link_overrides` | a snapshot's manual (present) and suppressed (absent) links: a summary and one page of rows with `ports_exist` and `link_in_topology`; with `compare_to_snapshot_id`, the added, removed and changed overrides between two snapshots | `device` (either end), `limit` (default 50; 25 examples per list when comparing), `offset` (not with `compare_to_snapshot_id`) |
| `external` | how the edge is modelled (see below) | `device` (one synthetic node by name) |

Optional: `snapshot_id` (default newest processed), `limit` (default 40, at most 200), `offset`, and, for kind `link_overrides` only, `compare_to_snapshot_id` (the earlier snapshot; `snapshot_id` is then the later one). An input that belongs to another kind is rejected by name. Locations and tags are
defined per network, not per snapshot, and the limits say so.
