# inspect-vulnerabilities: inputs

`network_id`, and one of four views:

- nothing else, or `view: cves` (the default): the **network** view, the CVEs that may affect devices, worst first;
- `cve_id`: one CVE with a verdict per affected device;
- `device`: one device's CVE findings;
- `view: devices`: **one row per device** with its `internet_addressable` flag, its CVE count, worst severity, exposed and unsettled CVEs, from one call to Forward. Use it to list which devices are internet addressable (with `internet_addressable: true`) instead of reading CVE after CVE. It rejects `cve_id` and `device`. The finding says how many DEVICES are addressable, not only how many CVEs; `addressable_device_names` holds every one. Only devices with at least one CVE matching the filters are listed (a flagged device with none is not), and `min_severity` is applied to each device's per-severity counts.

Optional: `snapshot_id` (default: newest processed, collected), `min_severity`, `known_exploited_only`,
`internet_addressable` (network, cve_id and devices views) and `limit` (default 25 rows shown).

**`internet_addressable` is not "open".** It is one yes or no per device, from one arbitrary qualifying interface, so a flagged load balancer can still deny a given VIP. Read it as "can receive some traffic from the internet" and trace the exact flow with `investigate-reachability` before saying a service is reachable (`plan-synthetic-device` reference/exposure.md). With the filter on, every count in the CVE views is already addressable-only; a CVE row with `exposed_devices: 0` has addressable devices whose verdict is unsettled (`unsettled_devices`). The unfiltered single-CVE view shows the first `limit` devices ordered by verdict, not by exposure; its limits give the addressable count across all of them.

## threat_exposure_experimental (EXPERIMENTAL, read only)

Forward's threat exposure feature: which critical assets are exposed to which exposure points. It is experimental in Forward, enabled by the organization property THREAT_EXPOSURE (default off, not an organization administrator's to change), and its routes exist only in newer Forward builds (an older one answers 404 and this view returns `unknown`). A different question from the CVE inputs; give one. Every answer is labelled experimental and the analysis views are Forward's raw answer.

`threat_exposure_experimental` is `{view, definition_id, asset_id, offset, limit}`:

- `definitions` (default): the network's threat exposure definitions (name, exposure points and levels, locations, criticalities, subnets, time limit). `definition` with `definition_id`: one.
- `status` with `definition_id`: the analysis state and progress on the snapshot (`snapshot_id`, latest by default).
- `critical_assets` with `definition_id`: the critical assets found exposed, paged (`offset`, `limit`; a fixed order, no sort). Each row gives the asset id for the next two views.
- `direct` and `transitive` with `definition_id` and `asset_id`: how that asset is directly exposed, and exposed through other hosts (paged).
- `edge_assets`: the network's edge assets with their criticality (BUSINESS_CRITICAL, CRITICAL_INFRA, CROWN_JEWEL), which a definition selects on.

Needs VIEW_THREAT_EXPOSURE (edge assets: VIEW_COLLECTION_SOURCES) and the security licence facet. Nothing here creates a definition, starts an analysis or changes a criticality.
