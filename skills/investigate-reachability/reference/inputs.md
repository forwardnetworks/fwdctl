# investigate-reachability: inputs

`network_id` and `dst_ip` are required. Give `src_ip` or `from` (a device) to anchor the
source. Optional: `protocol` (`tcp|udp|icmp` or a number), `src_port`, `dst_port`,
`snapshot_id` (default: the newest processed snapshot), `max_results`, `max_seconds`, `return_path` (`true`: also search the return flow and report it beside the forward answer), `intent` (`PREFER_DELIVERED` default, `PREFER_VIOLATIONS`, `VIOLATIONS_ONLY`: how Forward selects the paths it returns; to see paths a default answer lacks, compare intents).
`from` names a device only (Forward's path search has no ingress-interface input; confirmed from its source): it may start the flow on any of the device's interfaces, a loopback among them. To start from one interface or subnet give `src_ip` (Forward resolves it to a location), alone or with `from`. Every path's evidence carries `source_hop` (the first hop's device and the interface the packet entered by) and the full `hops` list, so keep the paths whose `source_hop.ingress_interface` is the one you mean.


## matrix (every pair of several endpoints)

`matrix` answers "which of these endpoints reach which, and does the reply come back the way the request went" in one bulk request, instead of one flow. It does not combine with `src_ip`, `dst_ip`, `from` or `return_path`.

`matrix` is `{endpoints, transit_groups?, max_overall_seconds?}`:

- `endpoints`: 2 to 40 of `{label, ip}`. Every ordered pair is searched, so n endpoints make n x (n - 1) queries (ten endpoints: 90). Use host addresses where you have them; a router loopback only proves the control plane resolves. `protocol`, `src_port`, `dst_port`, `intent`, `max_seconds` (per query) and `snapshot_id` apply to every query.
- `transit_groups`: `[{label, pattern}]`, a regular expression matched against each hop's device name (the first group that matches labels the hop). A path is labelled with the groups it crosses, in order. Take them from the design, not from naming habits. Without groups a path is its device sequence.
- `max_overall_seconds`: time for the whole request (default 300, at most 3000). Forward runs more than ten queries as a large job with a one hour limit, and a query it has not started when the time passes comes back as a timeout with no paths.

The result has `cells` (from, to, verdict delivered, blocked or undetermined, the outcome, groups crossed, hop count), `asymmetric_pairs` and `counts`. A pair is asymmetric when both directions are delivered and the groups crossed (or, without groups, the devices) differ once the reverse is turned around. A timed out or failed query is undetermined, never counted as blocked. Each cell uses the best path Forward returned (up to three per query). It needs VIEW_PATHS and a snapshot processed to the reachability stage.
