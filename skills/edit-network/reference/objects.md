# edit-network: objects and their limits

## Contents
- network
- location
- cluster
- tag
- threat_exposure and asset_criticality (experimental)

## network

`create` makes an empty network (a name that already exists, ignoring case, is refused). `update` changes the name, note or retention days of an existing network (the number of days a snapshot is kept by default). `delete` removes the network and everything in it; it is refused in a build without the delete seam, and needs `confirm` equal to the network id. A workspace network is managed by `edit-workspace`.

## location

`create` takes `name`, `lat`, `lng` and optionally `city`, `adminDivision`, `country`, `id`. `assign` takes a map of device name to location id; the ids must exist on the network (`inspect-platform` area `locations`). Forward accepts a device name it does not know without effect. `update` changes any create field or `deviceGlobs` (devices whose name matches join the location); `delete` needs `confirm` = the location id. The API has no read of which device sits where, so note current assignments before changing them.

## cluster

A cluster groups devices at a location. `create` needs `location_id`, `name` and `devices`; `update` renames it or replaces its devices. `delete` needs `location_id` in the definition and `confirm` = the cluster name; the devices stay.

## tag

`update` renames a tag definition (its assignments move with it) or changes its color. `delete` removes the definition and takes the tag off every device across the network's whole timeline. Putting a tag on or off named devices is `edit-device-tags`.

## threat_exposure and asset_criticality (EXPERIMENTAL)
Threat exposure is Forward's experimental feature, switched on by the organization property `THREAT_EXPOSURE` (only Forward support changes it), gated by the security licence facet and the permissions `EDIT_THREAT_EXPOSURE`, `EVALUATE_THREAT_EXPOSURE` and `EDIT_ASSET_CRITICALITIES`; a build that does not serve its routes answers 404. Every answer says EXPERIMENTAL. Read the results with `inspect-vulnerabilities` option `threat_exposure_experimental`.
- `threat_exposure`, `name` = the definition's name. `create` `definition` {`exposure_points` [{`exposure_point` (an interface alias, such as the internet edge), `level` CRITICAL|SEVERE|HIGH|MODERATE}] (1 to 4), `max_duration_seconds` (the time limit of one analysis), optional `locations`, `criticalities` (BUSINESS_CRITICAL, CRITICAL_INFRA, CROWN_JEWEL), `subnets`}; an empty filter selects all critical assets. `update` takes any of those and `name`; an empty list clears that filter. `delete` needs `confirm` = the name and cannot be undone. `evaluate` `definition` {`snapshot_id`} starts the analysis on that snapshot (it must be processed to the reachability stage) and returns at once; follow it with `inspect-vulnerabilities` views `status`, `critical_assets`, `direct`, `transitive`.
- Evaluate and the analysis views need Forward's analysis service, which pre-beta builds do not implement (the appserver answers 500); the answer then says so and not to retry. Definitions and criticality work without it.
- `asset_criticality`: `assign` `definition` {`names`, `level`} sets the criticality of named assets (edge assets and endpoints); `delete` `definition` {`names`} removes it (`confirm` = the names joined by commas). A before value is shown only for edge assets.
