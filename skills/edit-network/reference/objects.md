# edit-network: objects and their limits

## Contents
- network
- location
- cluster
- tag

## network

`create` makes an empty network (a name that already exists, ignoring case, is refused). `update` changes the name, note or retention days of an existing network (the number of days a snapshot is kept by default). `delete` removes the network and everything in it; it is refused in a build without the delete seam, and needs `confirm` equal to the network id. A workspace network is managed by `edit-workspace`.

## location

`create` takes `name`, `lat`, `lng` and optionally `city`, `adminDivision`, `country`, `id`. `assign` takes a map of device name to location id; the ids must exist on the network (`inspect-platform` area `locations`). Forward accepts a device name it does not know without effect. `update` changes any create field or `deviceGlobs` (devices whose name matches join the location); `delete` needs `confirm` = the location id. The API has no read of which device sits where, so note current assignments before changing them.

## cluster

A cluster groups devices at a location. `create` needs `location_id`, `name` and `devices`; `update` renames it or replaces its devices. `delete` needs `location_id` in the definition and `confirm` = the cluster name; the devices stay.

## tag

`update` renames a tag definition (its assignments move with it) or changes its color. `delete` removes the definition and takes the tag off every device across the network's whole timeline. Putting a tag on or off named devices is `edit-device-tags`.
