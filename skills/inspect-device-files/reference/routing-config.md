# Reading routing protocol config, by vendor

Read when a device's text is the source for a routing question: why a neighbor is not up, which interfaces run a protocol, where a route comes from. General vendor behaviour, not checked against this repository's code: confirm on the device's own text and say which file you read. This is the config side only. Neighbor state and learned routes come from the model (`inspect-bgp-neighbors`, an NQE query), not from reading text.

## BGP

- **Where the neighbor lives.** IOS and IOS-XE: `router bgp <asn>` with `neighbor X remote-as`, then `address-family` blocks. NX-OS: `feature bgp`, then a `neighbor X` block holding its own `address-family`. IOS-XR: `neighbor X` with `remote-as` and per-neighbor `address-family`. Arista EOS: like IOS, with `neighbor X activate` per address family. Junos: `protocols bgp group G { type external; peer-as N; neighbor X; }`.
- **Inheritance hides config.** NX-OS `template peer` / `inherit peer`, IOS `peer-group` and `peer-session`, Junos group-level statements. A neighbor that looks bare may take its policy, timers or password from the template or group: read it before saying a setting is missing.
- **Activation.** On IOS a neighbor carries IPv4 unicast by default; with `no bgp default ipv4-unicast` every address family needs `neighbor X activate`. Other families (VPNv4, EVPN, IPv6) always need it. A neighbor that is up but exchanges nothing is often this.
- **Policy decides what moves.** Inbound and outbound `route-map` / `route-policy` / Junos `import` and `export` name a policy that must be read. On IOS-XR an eBGP neighbor with no route policy in a direction accepts or advertises nothing (RFC 8212 behaviour).
- **Why a session will not come up (config side).** The `remote-as` does not match the peer's AS, the neighbor address is not reachable (check `update-source` or the loopback it names), a password differs, or `ebgp-multihop` / `ttl-security hops` is missing or one-sided on a non-adjacent eBGP peer.

## OSPF

- **How an interface joins.** IOS and EOS: `network <address> <wildcard> area N` under `router ospf <pid>` (a wildcard mask, not a subnet mask), or `ip ospf <pid> area N` on the interface. NX-OS: `ip router ospf <name> area N` on the interface. Junos: `protocols ospf area N interface X`. The process id is local to the device; the area must agree on both ends of a link.
- **Adjacency needs matching** area, area type (stub or NSSA flags), hello and dead timers, authentication and MTU. `passive-interface` stops adjacencies on that interface by design.
- **Network type.** `ip ospf network point-to-point` (Junos `interface-type p2p`) changes whether a DR is elected. Check that both ends of a link agree.

## IS-IS

- **The NET** is the address of the process: an ISO address such as `49.<area>.<system id>.00` (`49` is the private address family prefix), the last byte `00`. Two routers in one area share the area part and differ in the system id.
- **Metrics.** `metric-style wide` is needed for metrics above 63 and for traffic engineering or segment routing extensions; narrow and wide on one level do not interoperate cleanly.
- **Where it runs.** IOS: `ip router isis` on the interface; Junos: the interface under `protocols isis`, with `family iso` and an ISO address on the loopback.

## Static routes

A floating static route has an administrative distance higher than the protocol it backs up (IOS `ip route ... 250`, Junos `preference`), so it stays out of the table until the other route goes. A route to `Null0` or Junos `discard` drops traffic on purpose. A static route whose next hop does not resolve is not installed.

## First-hop redundancy

HSRP: priority 100 by default, and a higher-priority router takes back the active role only if `standby preempt` is set. VRRP: preempt is on by default. Check both peers together: the group id, the virtual address and the priorities must be consistent.

## MPLS, segment routing, EVPN

Treat these blocks as load-bearing: IOS-XR `segment-routing`, `mpls ldp`, `l2vpn`, `evpn`; EOS `vxlan` interfaces with a `vni` mapping, and `router bgp` `address-family evpn`. EVPN route types differ in what they carry: type 2 is MAC and IP, type 3 is the multicast/flood list, type 5 is an IP prefix. Read the model (`inspect-topology`, an NQE query) for state; use the text to see what was intended.
