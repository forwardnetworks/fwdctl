# inspect-vulnerabilities: inputs

`network_id`, and one of four views:

- nothing else: the **network** view, the CVEs that may affect devices, worst first;
- `cve_id`: one CVE with a verdict per affected device;
- `device`: one device's CVE findings;
- `view: devices`: **one row per device** with its `internet_addressable` flag, its CVE count, worst severity, exposed and unsettled CVEs, from one call to Forward. Use it to list which devices are internet addressable (with `internet_addressable: true`) instead of reading CVE after CVE. It rejects `cve_id` and `device`. The finding says how many DEVICES are addressable, not only how many CVEs; `addressable_device_names` holds every one. Only devices with at least one CVE matching the filters are listed (a flagged device with none is not), and `min_severity` is applied to each device's per-severity counts.

Optional: `snapshot_id` (default: newest processed, collected), `min_severity`, `known_exploited_only`,
`internet_addressable` (network, cve_id and devices views) and `limit` (default 25 rows shown).

**`internet_addressable` is not "open".** It is one yes or no per device, from one arbitrary qualifying interface, so a flagged load balancer can still deny a given VIP. Read it as "can receive some traffic from the internet" and trace the exact flow with `investigate-reachability` before saying a service is reachable (`plan-synthetic-device` reference/exposure.md). With the filter on, every count in the CVE views is already addressable-only; a CVE row with `exposed_devices: 0` has addressable devices whose verdict is unsettled (`unsettled_devices`). The unfiltered single-CVE view shows the first `limit` devices ordered by verdict, not by exposure; its limits give the addressable count across all of them.
