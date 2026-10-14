# Reading a path result

Read when a path result needs interpreting: a flow works one way, a search came back empty, or you must say what to look at next.

## Test both directions

A delivered forward flow proves nothing about the return. Give `return_path: true` and Forward also searches the return flow; the result carries a `return_flow` summary (paths, how many delivered, a verdict), and a limit says so when the forward flow is delivered but the return is not. Without it, or to compare the device sequence of each direction, run the reverse flow yourself (swap source and destination and the ports). A forward path can move to a new transit while the return still uses the old one, which is how asymmetry breaks stateful firewalls. A reverse flow you did not run is **not measured**, not "fails". Delivered is not the same as permitted: read the security outcome (`security_denied` means delivered by the network and denied by a rule).

## Use real host addresses

A router loopback proves the control plane resolves, not that a user's packet arrives. For an end-to-end claim, start from and end at host or service addresses (`inspect-inventory` finds them).

## From a drop reason to the next read

| First failure | Read next |
|---|---|
| ACL or zone deny | the hop's `acls` (name, where it applied, action) and `ingress_zone` / `egress_zone` name the ACL and the zone pair; then the matching rule: `inspect-device-files` search on the ACL name |
| No route | where the prefix stops: trace toward the next hop, `inspect-device-files` for the routing protocol's filters |
| Next hop does not resolve (blackhole) | whether an IGP or redistribution filter hides the next-hop subnet |
| Interface down | administratively down (config) or protocol down (the other end, a missing link): `inspect-topology` links |
| Loop | the two devices handing the packet back: compare their routes for the destination |
| NAT does not match | the device's NAT rules and the zones before and after translation. On Palo Alto PAN-OS a security rule matches the addresses as they were before NAT and the destination zone as it is after NAT; other vendors differ (SRX matches policy after destination NAT, ASA 8.3 and later matches the real addresses), so read the vendor's own order (general vendor behaviour, not checked here) |
| Reverse-path check fails | the interface the packet arrived on against the route back to its source: an asymmetric route, or a strict reverse-path check on the ingress interface |
| A route should exist but does not (BGP) | whether the neighbor advertises the prefix and whether an inbound policy filters it: `inspect-bgp-neighbors` |

## Empty is not a pass

Zero paths, or a timeout, is **unknown**: the skill says which. If you constrain a search to pass through a device and get nothing, that says nothing about the unconstrained path: run it again without the constraint.

## Intents and a path through a device

`intent` chooses which paths Forward returns, not whether a flow is allowed. `PREFER_DELIVERED` (the default) favours delivered paths and can hide a drop; to debug a failing flow use `PREFER_VIOLATIONS`, and do not use `VIOLATIONS_ONLY` to ask whether a flow works: it returns only violating paths, so a delivered flow returns none (an empty answer there is not "unreachable"; observed on a live network). In a security assessment run one `PREFER_VIOLATIONS` pass as well, because a delivered path can still violate policy. Check `timed_out` before reading an empty result. (Unverified: how Forward ranks paths within an intent; compare intents to see.)

Forward's API has no "through this device" constraint (only `from`): to test A to B via a device, run the search and read the hops of each returned path for that device. If no returned path contains it, say the traffic does not take it in the returned paths, and that only the paths returned were checked.

## Two paths are not two failure domains

Two paths that both cross one device share its fate: if that device fails, both go. Before calling a flow redundant, compare the hops of its paths and name the devices and links they have in common. Redundant means no single device or link appears on all of them.

## Hop details: ACLs and zones

Each hop that Forward evaluated an ACL or a security zone on carries `acls` (each with a `name`, a `context` for where it applied, and an `action`) and `ingress_zone` / `egress_zone`. A hop with none has no such keys: that means Forward reported none for it, not that none exist. An ACL `action` of PERMIT on the last hop does not make the flow delivered: read the forwarding outcome too.

