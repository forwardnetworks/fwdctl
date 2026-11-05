# investigate-reachability: inputs

`network_id` and `dst_ip` are required. Give `src_ip` or `from` (a device) to anchor the
source. Optional: `protocol` (`tcp|udp|icmp` or a number), `src_port`, `dst_port`,
`snapshot_id` (default: the newest processed snapshot), `max_results`, `max_seconds`, `return_path` (`true`: also search the return flow and report it beside the forward answer), `intent` (`PREFER_DELIVERED` default, `PREFER_VIOLATIONS`, `VIOLATIONS_ONLY`: how Forward selects the paths it returns; to see paths a default answer lacks, compare intents).

`explain`: `routes` adds a `route` to every hop of the paths shown: the forwarding entry its device holds for `dst_ip` (the longest prefix in the hop's VRF, from Forward's forwarding table, one NQE read for every hop): `vrf`, `vrf_from` (how the VRF was told: `forward` from the hop's ingress_vrf, `self_interface`, `ingress_interface`, `ingress_subinterfaces`, `only_vrf`, or `assumed_default`), `prefix`, `next_hops` (ip, interface, vrf, type, protocol), and `path_check` against the hop's `egress_interface`: `agrees` (a next hop leaves by it), `svi_member_port` (the route is out a routed VLAN interface whose VLAN holds that port: routed to the SVI, then switched out the port), `not_comparable` (no next hop names an interface, as for a recursive BGP or OSPF next hop: use the hop's egress), or `disagrees` (`vrfs_agreeing_with_path` then names the VRFs whose entry does leave by it). `agrees_with_path` is true for the first two, false for `disagrees`, absent when not comparable. One flow only (not `matrix`), IPv4. It does not apply PBR, NAT or route leaking; the hop's `egress_interface` is Forward's own answer and wins (a recursive next hop, BGP or OSPF remote, has no interface in the table: read the egress from the hop).
`from` names a device only (Forward's path search has no ingress-interface input; confirmed from its source): it may start the flow on any of the device's interfaces, a loopback among them. To start from one interface or subnet give `src_ip` (Forward resolves it to a location), alone or with `from`. Every path's evidence carries `source_hop` (the first hop's device and the interface the packet entered by) and the full `hops` list, so keep the paths whose `source_hop.ingress_interface` is the one you mean.


## matrix (every pair of several endpoints)

`matrix` answers "which of these endpoints reach which, and does the reply come back the way the request went" in one bulk request, instead of one flow. It does not combine with `src_ip`, `dst_ip`, `from` or `return_path`.

`matrix` is `{endpoints, transit_groups?, max_overall_seconds?}`:

- `endpoints`: 2 to 40 of `{label, ip}`. Every ordered pair is searched, so n endpoints make n x (n - 1) queries (ten endpoints: 90). Use host addresses where you have them; a router loopback only proves the control plane resolves. `protocol`, `src_port`, `dst_port`, `intent`, `max_seconds` (per query) and `snapshot_id` apply to every query.
- `transit_groups`: `[{label, pattern}]`, a regular expression matched against each hop's device name (the first group that matches labels the hop). A path is labelled with the groups it crosses, in order. Take them from the design, not from naming habits. Without groups a path is its device sequence.
- `max_overall_seconds`: time for the whole request (default 300, at most 3000). Forward runs more than ten queries as a large job with a one hour limit, and a query it has not started when the time passes comes back as a timeout with no paths.

The result has `cells` (from, to, verdict delivered, blocked or undetermined, the outcome, groups crossed, hop count), `asymmetric_pairs` and `counts`. A pair is asymmetric when both directions are delivered and the groups crossed (or, without groups, the devices) differ once the reverse is turned around. A timed out or failed query is undetermined, never counted as blocked. Each cell uses the best path Forward returned (up to three per query). It needs VIEW_PATHS and a snapshot processed to the reachability stage.
