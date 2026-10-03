# BGP policy and what is advertised: reading it from the device files

Forward's model does not hold a neighbor's route-maps, prefix-lists, communities or the policy clauses (it has the result: the Adj-RIB-Out after output policy, which is what `advertised` lists). The text is in the collected device files, which `inspect-device-files` reads. This is the sequence for "which policy decides what this neighbor is sent, and is it meant for the internet".

## Contents
- Is an advertisement meant for the internet?
- Finding the policy of one neighbor (IOS, IOS-XE, NX-OS)
- Other platforms

## Is an advertisement meant for the internet?

Advertised to an upstream is an export to that upstream, not proof of internet intent. Check three things before calling a prefix public:
1. **Who the upstream is.** A provider WAN or private carrier network (a peer AS that is not an internet transit) is not the internet, whatever it is called in the config.
2. **What the output policy permits.** A policy that denies one community and permits everything else exports locally originated routes and routes learned from internal peers alike. A policy that permits only a named prefix-list or community is an intent signal.
3. **What the input policy brings back.** An upstream that returns only a default route is a default-only attachment.
`advertised` shows each prefix's origin (`origin_type` local or learned, `next_hop`, `as_path`) so you can separate what the device originates from what it re-advertises from internal peers. Only the customer can settle whether space is meant to be routable on the internet: say so in the answer instead of inferring it.

## Finding the policy of one neighbor (IOS, IOS-XE, NX-OS)

With `inspect-device-files` on the device (use `mode: list` first to find the file names; running config is usually the largest):
1. `search` the running config for the neighbor address (`pattern` the address, with `context` 3) to get the stanza and the `route-map NAME in|out` lines. On NX-OS and with VRFs the stanza sits under `router bgp ... vrf NAME`, and the neighbor under `address-family ipv4 unicast` carries the `route-map` lines.
2. `search` for `route-map NAME` to read each clause: sequence, `permit` or `deny`, the `match` lines (community, as-path, ip address prefix-list) and `set` lines.
3. For each `match`, `search` the list it names (`ip community-list`, `ip as-path access-list`, `ip prefix-list`) and read its entries.
4. The collected `show ip bgp neighbors ... advertised-routes` style files, where present, give the communities per prefix; Forward's model does not.
A route-map clause with no match line matches everything. A neighbor with no `route-map out` sends everything the BGP table allows.

## Other platforms

Junos holds policy as `policy-statement` terms under `protocols bgp group ... export` and `import`; IOS-XR as `route-policy` blocks referenced under the neighbor address-family. Read them the same way: neighbor, then the named policy, then the sets it matches on. Say which platforms were read; do not generalise from one.
