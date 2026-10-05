# BGP session down or empty: walk the rungs in order

Read when the question is "why is this BGP session not up", "why is a prefix not learned or not advertised", or "who is the upstream and is it healthy". A failure at a low rung looks like a failure at a higher one, so check in this order and stop at the first that fails.

1. **Is the neighbor modelled at all?** `inspect-bgp-neighbors` for the device (and `vrf`). A neighbor that is configured but down is listed with its state, so an absent row means not configured in that VRF or not collected: confirm with `inspect-device-files` search on the neighbor address. An empty `peer_device` means the peer is not a collected device, which is normal for an upstream.
2. **Can the packets get there?** `investigate-reachability` from the device's source address to the peer address, protocol TCP, destination port 179. One call answers routing and any ACL or firewall in the way. Read the first failure, not only "dropped".
3. **What state is the session in?** Read `state` from `inspect-bgp-neighbors`; do not count rows. A snapshot taken minutes after a build or a restart can show CONNECT, ACTIVE or IDLE as ordinary convergence: say how old the snapshot is before calling it a fault, and collect again if it is stale (`plan-snapshot-recovery`).
4. **Established but nothing learned or sent?** That is a policy or advertisement problem, not a session problem. `inspect-bgp-neighbors` with `advertised` shows what is sent (Junos, IOS, IOS-XE, NX-OS and IOS-XR only; no rows is unknown). The route-map or prefix-list is in the device files (`reference/policy.md` of `inspect-bgp-neighbors`).
5. **Learned but traffic still does not flow?** Test the flow (`investigate-reachability`). A route can be in the RIB and still be a blackhole when its next hop is not resolvable, for example because an IGP filter hides the next-hop subnet: trace to the next hop address.

## Not in Forward's model

Counters, CPU, the BGP state machine, negotiated capabilities, password mismatches, flap history and logs are not collected as model data. Do not infer them: say they are not measured, and that the device itself (its logs) is where they live.
