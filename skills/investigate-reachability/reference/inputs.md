# investigate-reachability: inputs

`network_id` and `dst_ip` are required. Give `src_ip` or `from` (a device) to anchor the
source. Optional: `protocol` (`tcp|udp|icmp` or a number), `src_port`, `dst_port`,
`snapshot_id` (default: the newest processed snapshot), `max_results`, `max_seconds`, `intent` (`PREFER_DELIVERED` default, `PREFER_VIOLATIONS`, `VIOLATIONS_ONLY`: how Forward selects the paths it returns; to see paths a default answer lacks, compare intents).
`from` names a device only (Forward's path search has no ingress-interface input; confirmed from its source): it may start the flow on any of the device's interfaces, a loopback among them. To start from one interface or subnet give `src_ip` (Forward resolves it to a location), alone or with `from`. Every path's evidence carries `source_hop` (the first hop's device and the interface the packet entered by) and the full `hops` list, so keep the paths whose `source_hop.ingress_interface` is the one you mean.

