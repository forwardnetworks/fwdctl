# What a connection is, and building them from a query

Source: Forward's documentation, "Synthetic Devices" (overview, choosing a device, configuring connections, automated setup). Paraphrased; Forward's pages are the authority.

Each connection ties the synthetic device to a collected interface and says what is reachable across it:
- **uplink interface**: the physical port on a collected device that meets the segment. **Gateway interface**: the last L3 interface that routes toward the segment (blank means the uplink). On an access or trunk
  port the gateway is the SVI of the VLAN (Forward infers it when uplink and gateway are on the same device); on a routed port they are the same interface; on a dot1q subinterface the VLAN comes from the subinterface.
  When uplink and gateway are on different devices, Forward infers nothing: name the VLAN and the gateway yourself.
- **VLAN**: the on-wire VLAN on the uplink; null (not 0) when untagged; 1 to 4095.
- **VRF** (L3 VPN, intranet node): connections interconnect only within the same VRF; blank means one default VRF.
- **site**: groups connections that reach the same segment through different gateways, so Forward does not model a routing loop across the device.
- **subnets**: the subnets this connection advertises (the site owns them and offers them across the segment). **Subnet discovery** infers them from the gateway instead: `ipRoutes` (its routing table; set
  whether it advertises the default route), `bgpRoutes` (its Adj-RIB-Out; optionally the peer addresses), `interfaceAddresses`, or `none` (then `subnets` must be non-empty).
- **backdoor interfaces**: L3 interfaces that give backdoor connectivity between sites, so traffic can bypass the segment.
- **connection name**: labels the interface created on the synthetic device.

For the internet node specifically (what its subnets mean, when `advertisesDefaultRoute` applies, a VRF already modelled as an L3 VPN) read `internet-node.md` in this folder (read it from the skill step, not from here).

A **Missing Peer** appears automatically when an uplink carries a VLAN or subinterface that no connection claims: it is not an error, it marks an unmodelled uplink to decide about.

## From an NQE query

For many connections, attach a saved query (`edit-synthetic-query`): each row becomes a connection (source NQE, "dynamic"); manual connections coexist; at most one query per device. See
`author-nqe-query` `reference/synthetic-devices.md` for the row types, `fwdctl nqe template <kind>` for the starter query and `fwdctl nqe lint --synthetic <kind>` for the offline row check.
