# Reading a path result

Read when a path result needs interpreting: a flow works one way, a search came back empty, or you must say what to look at next.

## Test both directions

A delivered forward flow proves nothing about the return. Run the reverse flow (swap source and destination and the ports) and compare the device sequence of each path. A forward path can move to a new transit while the return still uses the old one, which is how asymmetry breaks stateful firewalls. A reverse flow you did not run is **not measured**, not "fails". Delivered is not the same as permitted: read the security outcome (`security_denied` means delivered by the network and denied by a rule).

## Use real host addresses

A router loopback proves the control plane resolves, not that a user's packet arrives. For an end-to-end claim, start from and end at host or service addresses (`inspect-inventory` finds them).

## From a drop reason to the next read

| First failure | Read next |
|---|---|
| ACL or zone deny | the matching rule: `inspect-device-files` search on the rule or ACL name |
| No route | where the prefix stops: trace toward the next hop, `inspect-device-files` for the routing protocol's filters |
| Next hop does not resolve (blackhole) | whether an IGP or redistribution filter hides the next-hop subnet |
| Interface down | administratively down (config) or protocol down (the other end, a missing link): `inspect-topology` links |
| Loop | the two devices handing the packet back: compare their routes for the destination |

## Empty is not a pass

Zero paths, or a timeout, is **unknown**: the skill says which. If you constrain a search to pass through a device and get nothing, that says nothing about the unconstrained path: run it again without the constraint. Rows back means the traffic takes another route; none means it is unreachable.

## Intents and a path through a device

`intent` chooses which paths Forward returns, not whether a flow is allowed. `PREFER_DELIVERED` (the default) favours delivered paths and can hide a drop; to debug a failing flow use `PREFER_VIOLATIONS`, and do not use `VIOLATIONS_ONLY`, which hides drops. In a security assessment run one `PREFER_VIOLATIONS` pass as well, because a delivered path can still violate policy. Check `timed_out` before reading an empty result. (Unverified: how Forward ranks paths within an intent; compare intents to see.)

Forward's API has no "through this device" constraint (only `from`): to test A to B via a device, run the search and read the hops of each returned path for that device. If no returned path contains it, say the traffic does not take it in the returned paths, and that only the paths returned were checked.
