# investigate-reachability: inputs

## Examples

One flow, TCP 443 from a host address:

```json
{"network_id": "NETWORK_ID", "src_ip": "10.1.1.5", "dst_ip": "10.2.2.9", "protocol": "tcp", "dst_port": "443"}
```

Start on a device, and check the way back too:

```json
{"network_id": "NETWORK_ID", "from": "edge-1", "dst_ip": "10.2.2.9", "return_path": true}
```

Each hop's forwarding entry for the destination, checked against the path:

```json
{"network_id": "NETWORK_ID", "src_ip": "10.1.1.5", "dst_ip": "10.2.2.9", "explain": "routes"}
```

Every pair of several endpoints, naming the pairs that take different paths each way (see "matrix" below):

```json
{"network_id": "NETWORK_ID", "matrix": {"endpoints": [{"label": "a", "ip": "10.1.1.5"}, {"label": "b", "ip": "10.2.2.9"}]}}
```

Every device path from one place that passes a firewall and leaves at an edge interface, with the packet as it leaves (search form, preview; see "search form" below):

```json
{"network_id": "NETWORK_ID", "from": "branch-1", "dst_ip": "203.0.113.0/24", "through": ["fw-1"], "leaving": {"at": "edge-1 eth1", "dst_port": "443"}}
```

## Every input

`network_id` and `dst_ip` are required. Give `src_ip` or `from` (a device) to anchor the
source. Optional: `protocol` (`tcp|udp|icmp` or a number), `src_port`, `dst_port`,
`snapshot_id` (default: the newest processed snapshot), `max_results`, `max_seconds`, `return_path` (`true`: also search the return flow and report it beside the forward answer), `intent` (`PREFER_DELIVERED` default, `PREFER_VIOLATIONS`, `VIOLATIONS_ONLY`: how Forward selects the paths it returns; to see paths a default answer lacks, compare intents).

`explain`: `routes` adds a `route` to every hop of the paths shown: the forwarding entry its device holds for `dst_ip` (the longest prefix in the hop's VRF, from Forward's forwarding table, one NQE read for every hop): `vrf`, `vrf_from` (how the VRF was told: `forward` from the hop's ingress_vrf, `self_interface`, `ingress_interface`, `ingress_subinterfaces`, `only_vrf`, or `assumed_default`), `prefix`, `next_hops` (ip, interface, vrf, type, protocol), and `path_check` against the hop's `egress_interface`: `agrees` (a next hop leaves by it), `svi_member_port` (the route is out a routed VLAN interface whose VLAN holds that port: routed to the SVI, then switched out the port), `not_comparable` (no next hop names an interface, as for a recursive BGP or OSPF next hop: use the hop's egress), or `disagrees` (`vrfs_agreeing_with_path` then names the VRFs whose entry does leave by it). `agrees_with_path` is true for the first two, false for `disagrees`, absent when not comparable. One flow only (not `matrix`), IPv4. It does not apply PBR, NAT or route leaking; the hop's `egress_interface` is Forward's own answer and wins (a recursive next hop, BGP or OSPF remote, has no interface in the table: read the egress from the hop). Each hop also gets `device_type` and `vendor` from the inventory, and a hop whose behaviors have no `L3` (an L2 switch, an overlay hop) gets `route: {lookup: false, note}` instead of an empty entry.

`explain`: `trace` (PREVIEW, UNDOCUMENTED) adds one evidence (operation `trace`) holding Forward's GUI flow trace of each distinct path, up to 16: per path `id`, `group`, `trace_type` (VALID, DROPPED, BLACKHOLE...), `path` (the devices), `descriptor`, and `trace`, Forward's response passed through unchanged (deviceHops with inPorts/outPorts, headers after each hop, networkFunctions by layer with fileLines and actionBuckets, behavior.processingStages, ingressHeaders), or `error` for a path that could not be traced. It reads routes that are not in Forward's published API and can change between builds; the limits say so on every answer. Works on any snapshot id, including a predicted one (not yet run live on a prediction). fileLines are 0-based line ranges in a collected file: read them with `inspect-device-files` (add 1). The trace places the source as Forward's GUI does, which can differ from the path search: give `from` to pin it.
`from` names a device only (Forward's path search has no ingress-interface input; confirmed from its source): it may start the flow on any of the device's interfaces, a loopback among them. To start from one interface or subnet give `src_ip` ALONE (Forward resolves it to a location). With `from`, `src_ip` is only the packet's source header (Forward's path-search API: with `from` the srcIp is "considered as a packet header only"): the flow starts on the device whatever the address, so a source the device would not accept (a reverse-path check, a policy that names a source address) can leave no path. Every path's evidence carries `source_hop` (the first hop's device and the interface the packet entered by) and the full `hops` list, so keep the paths whose `source_hop.ingress_interface` is the one you mean.


## matrix (every pair of several endpoints)

`matrix` answers "which of these endpoints reach which, and does the reply come back the way the request went" in one bulk request, instead of one flow. It does not combine with `src_ip`, `dst_ip`, `from` or `return_path`.

`matrix` is `{endpoints, transit_groups?, max_overall_seconds?}`:

- `endpoints`: 2 to 40 of `{label, ip}`. Every ordered pair is searched, so n endpoints make n x (n - 1) queries (ten endpoints: 90). Use host addresses where you have them; a router loopback only proves the control plane resolves. `protocol`, `src_port`, `dst_port`, `intent`, `max_seconds` (per query) and `snapshot_id` apply to every query.
- `transit_groups`: `[{label, pattern}]`, a regular expression matched against each hop's device name (the first group that matches labels the hop). A path is labelled with the groups it crosses, in order. Take them from the design, not from naming habits. Without groups a path is its device sequence.
- `max_overall_seconds`: time for the whole request (default 300, at most 3000). Forward runs more than ten queries as a large job with a one hour limit, and a query it has not started when the time passes comes back as a timeout with no paths.

The result has `cells` (from, to, verdict delivered, blocked or undetermined, the outcome, groups crossed, hop count), `asymmetric_pairs` and `counts`. A pair is asymmetric when both directions are delivered and the groups crossed (or, without groups, the devices) differ once the reverse is turned around. A timed out or failed query is undetermined, never counted as blocked. Each cell uses the best path Forward returned (up to three per query). It needs VIEW_PATHS and a snapshot processed to the reachability stage.

## search form (through, leaving)

`through` or `leaving` switches to the search form: Forward's Search-page path query (PREVIEW: the GUI's trace-facets route, not in Forward's published API). It answers "which distinct device paths match", grouped by outcome, with the total number of flows; with `explain: trace` each path found (up to 16) also carries Forward's trace for the same query, so a path with `through` hops or a `leaving` side is traced as searched; it does not combine with `return_path`, `explain: routes` or `matrix`. `intent` and `max_seconds` do not apply; `max_results` caps the device paths listed (at most 32).

- `from` names where the traffic enters: `DEVICE`, `"DEVICE INTERFACE"` (a space between, Forward's own form), `zone:DEVICE ZONE`, `vrf:[DEVICE ]VRF`, `host:NAME` or an address or prefix. Without `from`, `src_ip` is the place it enters; with `from`, `src_ip` filters the source address. `dst_ip`, `protocol`, `src_port` and `dst_port` filter the packet where it enters, so they need `from` or `src_ip`.
- `through`: up to 8 hops the path must pass, in order, each a location as above (not a host, address or prefix), optionally prefixed `ingress:` or `egress:` (default: anywhere through it).
- `leaving`: `{at, src_ip?, dst_ip?, src_port?, dst_port?, protocol?}`. `at` (required) is where the traffic leaves the network: a device, `"DEVICE INTERFACE"`, zone or VRF (an address or prefix is refused: a subnet there matched nothing live; put the address in `dst_ip`); the other fields describe the packet as it leaves, after any NAT.
- When an address in the query is at more than one place, Forward answers with the places instead of paths: the result is `unknown` with `possible_locations`; name the device and ask again.
- No match means no modelled flow takes that route in the snapshot. A hop named in another form (an interface spelling, a VRF) matches nothing, so check the names with `inspect-inventory` first.
- Seen live: on 26.11 (a two-device lab) the interface form `"DEVICE INTERFACE"`, an interface in the leaving side returning paths, and a subnet in the leaving side returning nothing (so `at` takes a device or interface); on a 31-device demo network a device `through` hop narrowing the paths, `egress:` accepted, header filters (protocol, port, destination prefix) narrowing them, and a device as `leaving.at` returning the paths that leave there. Zones, VRFs, hosts and `ingress:` are read from Forward's source and not yet confirmed live.

