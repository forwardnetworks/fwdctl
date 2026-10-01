# The internet node: what a connection is, and what to do with a VRF you already model

Source: Forward's documentation, "Synthetic Devices" (reference, choosing a device, configuring connections, troubleshooting) and "Path Analysis" (reference, troubleshooting). Paraphrased; Forward's pages are the authority. Where this page says **inferred** or **unknown**, Forward does not say: test it.

## Contents

What it is; what the connection's subnets mean; a worked example; a VRF already modelled as an L3 VPN connection; symptom to cause; what "owns every public address" implies for 8.8.8.8; internal public address space and the exclude list (what it is, what is subtracted, how to derive the set, the NQE queries, read and edit).

## What it is

The internet node is **the public internet beyond your edge**. One exists per network (name `internet`, cannot be deleted or renamed) and it is **inert until it has a connection**. A connection is the **uplink and gateway where your network hands
traffic to the public internet**: the edge router's port toward the ISP (or the firewall's outside interface). It has no VRF: all its connections share one routing domain.

Once it has a connection it **owns every public address that is not located elsewhere**: all public space, minus the subnets located on your collected devices or at a connected site, minus what you list as **excluded subnets**.
Private and reserved addresses are never routed by it (rejected if you list them in its subnets or exclusions).

## What the connection's subnets mean

The subnets of a connection are the subnets **your side owns and offers across the segment**: Forward installs them as routes on the synthetic device pointing back at that gateway. For the internet node that means **your own public space behind this
uplink** (your NAT or egress pool, a public block you announce), which then localizes to your site instead of to the internet node. They are **not** "the public space reachable beyond it": the internet node already owns that automatically.
So an edge with no public space of its own needs no listed subnets, and listing the internet's prefixes is wrong.

A public prefix leaves the internet node only when it reaches the node through one of its connections (a BGP advertisement, a learned or advertised subnet, a gateway interface address, an entry in `subnets`) or is **excluded**. Public space you route
internally but never advertise to the node keeps landing on the internet node: add it to the node's exclude subnets.

| Discovery on the connection | Means | Use when |
|---|---|---|
| `interfaceAddresses` | only the gateway interface's own addresses | the edge has a public /30 (or similar) and nothing else of yours is public |
| `ipRoutes` | public subnets the gateway forwards out a port other than the uplink to the node; never the default route unless the flag below is set | the edge routes your public blocks inward |
| `bgpRoutes` | what the gateway advertises to its eBGP peers across the connection (needs Adj-RIB-Out; `peerIps` narrows it) | the edge speaks BGP to the ISP |
| `none` | exactly the `subnets` you list, which must be non-empty | you know your public blocks and want no inference |

**`advertisesDefaultRoute: true`** (only with `ipRoutes`) counts a default route that the gateway forwards out a port **other than the uplink to the node**. An edge router whose default route leaves **through the uplink to the ISP** is the common
case and the flag does nothing for it: leave it `false`. Forward drops the default route by default because an unverified one multiplies path counts. Set it only when the gateway really advertises a default route inward-facing and you accept that
(**unknown** what the node then owns for that connection: check with a before/after trace).

## Worked example: an edge router whose default route leaves through a /30 to an unmodelled ISP

`edge1` has `Gi0/0/0` = `203.0.113.1/30`, default route `0.0.0.0/0 via 203.0.113.2` (the ISP, not collected). You own `198.51.100.0/24` (NAT pool, routed inward).

- One internet-node connection: uplink `edge1 Gi0/0/0`, gateway the same, VLAN null (routed port), discovery `ipRoutes` with `advertisesDefaultRoute: false`. `198.51.100.0/24` is found as yours and stops being the internet's; `203.0.113.0/30`
  is the gateway's own subnet. Without a public block of your own, use `interfaceAddresses`; `none` requires listed subnets.
- A host's flow to `8.8.8.8` runs to `edge1`, follows the default route out `Gi0/0/0`, enters the internet node and is located there. In a trace it ends **at the internet node**, which is the proof.
- Before the connection existed that same trace ended at `edge1` with no next hop: that dead end is why the node is added.

## When the VRF is already modelled as an L3 VPN connection

A common mistake: the provider's internet access rides a VRF (named `INET`, `INTERNET`, `DIA`) and a query already models it as a **connection on an L3 VPN** (a transit device that forwards only between connections in the same VRF and **drops
anything it cannot route to one**). A flow to a public address then enters that L3 VPN and is dropped, because no other connection in that VRF owns `8.8.8.8`.

**Confirmed from Forward's docs:** the L3 VPN is transit-only and drops what it cannot route; the internet node owns unassigned public space once it has a connection; localization checks the internet node before route-table entries; the internet node has no VRF.

**Inferred, not documented:** a port and VLAN face one segment, so the same uplink and VLAN is probably claimed by one synthetic device, not two (a collected uplink carrying a VLAN no connection claims also produces a Missing Peer).

**Unknown:** whether Forward accepts the same uplink and VLAN on both the L3 VPN and the internet node, which one wins, and what a duplicate does to existing paths.

Decision rule: **does the VRF carry only internet access?**
1. Yes: move it. Attach the internet node on that uplink and VLAN and remove that VRF's connections from the L3 VPN's query (a `where vrf != "INET"` on its rows), so the segment is claimed once.
2. It also carries private sites (a real provider VPN that also gives internet): keep it as the L3 VPN and give the internet node a **different** attachment point if one exists (the real firewall or proxy egress). If none exists, stop and ask.
3. Do not assume either. Run the **same trace before and after** on a new snapshot (`investigate-reachability`, edit with `backdate_snapshot_id` if they want it now) and keep the change only if the path now ends at the internet node and the paths that
   worked before still do. The `edit-*` dry run shows the connection count but cannot show how traffic behaves.

## Symptom to cause

| What you see | Likely cause |
|---|---|
| a trace to a public address ends **DROPPED at an L3 VPN node**, ingress on a VRF named like the internet | the internet is modelled as a VPN VRF, not as the internet node: the L3 VPN has no connection that owns the address |
| a trace to a public address ends at the edge device with no next hop | no synthetic device on that uplink at all |
| a public source appears at the internet node when it is yours | its prefix never reached the node through a connection: add it to `subnets`, discover it, or exclude it |
| a private address is missing from the internet node | expected: it routes public only; use an intranet node |

## What "owns every public address not found elsewhere" implies for 8.8.8.8

Once the internet node has a connection, `8.8.8.8` (on no interface, in no site, not excluded) is **located at the internet node** and the trace's destination is the node; while a transit L3 VPN sits in front of it with a connection in
that VRF, a path that reaches the VPN first is judged by the VPN (drops what it cannot route). So an existing `dir` connection does not stop `8.8.8.8` being the internet node's: it decides whether the path **gets there**. The node needs its
own connection on the egress; an L3 VPN claiming the same egress is the conflict to resolve with the rule above.

## Internal public address space: the exclude list

Some networks use public addresses internally (a customer's own /16 blocks routed inside, never advertised to an ISP). The internet node would claim them as "unassigned public space", so a flow to them is treated as internet-bound. They are
kept off the node by Forward's **excluded subnets**.

**What the exclude list is (confirmed in Forward's API and source):** a plain list of public CIDR blocks on the **internet node only** (`subnetsToExclude`; no other synthetic device has one). It is **not** driven by an NQE query and not a column of the connection
rows. It is changed with `PATCH /api/networks/{id}/internet-node` and `{"subnetsToExclude": [...]}`, which **replaces the whole list** (read the current list first and send it back with your additions). Each entry must be a valid public CIDR with host bits
zero; a private or reserved block is rejected on input. It applies from the next processed snapshot.

**What is subtracted from the internet node's space** (Forward's address-location index, computed per snapshot): all public space, minus the routes installed on the node itself (what the node's connections advertise: their `subnets`, or
what discovery found), minus the excluded list. An address that matches an interface of a collected device or a NAT pool is located there first, before the internet node is considered. Nothing in Forward's code rejects an overlap between
the exclude list and a connection's `subnets`: both are simply subtracted. **Unknown** (not in Forward's docs or the code read): how the node's routes interact with the rest of path search for an excluded prefix; trace to confirm.

**What to exclude, to derive the set:** the public prefixes your network routes internally that nothing already claims:
1. the public prefixes present in the routing tables of the collected devices (for example in the VRF the internet access lives in), as an NQE query over `ipv4Entries` (route tables) filtered to public, non-default prefixes;
2. minus the prefixes already reaching the node through a connection (what the gateway advertises toward the upstream: with `bgpRoutes` discovery, the Adj-RIB-Out to the upstream peer, the `peerIps` of the connection; with `ipRoutes` the routes forwarded out a non-uplink port);
3. minus prefixes that localize on a collected interface or NAT pool (located first, no exclusion needed);
4. what remains is the exclude set. A prefix advertised *to* the upstream is yours and is already off the internet node; a prefix you only learned *from* the upstream (a default route is the usual case) is not yours and must not be excluded.
Aggregate before saving (a /16 rather than a thousand /24s inside it) and keep the list as short as the truth allows; it is replaced as a whole, so every edit is a read, a diff and a replace.

**Deriving the set with NQE** (both queries were run read-only on a real network). The prefixes the gateway advertises to its upstream (the Adj-RIB-Out after output policy; only on Junos, IOS, IOS-XE, NX-OS and IOS-XR devices; `adjRibOutPost` and `bgpRib` are
nullable, so the checks are required):

    foreach device in network.devices
    where isPresent(device.bgpRib)
    foreach afi in device.bgpRib.afiSafis
    foreach neighbor in afi.neighbors
    where isPresent(neighbor.adjRibOutPost)
    foreach route in neighbor.adjRibOutPost.routes
    select { device: device.name, vrf: route.vrf, neighbor: neighbor.neighborAddress, prefix: route.prefix }

A neighbor in `bgpRib` is per device, not per VRF, and its Adj-RIB-Out holds the routes of several VRFs and address families (`route.vrf` is null for the default VRF), so filter on `route.vrf` and the family before counting or subtracting; summing over the peer overstates it. Which count to trust: `inspect-bgp-neighbors` reports `advertised_prefixes` (the device's own session counter, what it says it sends) and `adj_rib_out_distinct_prefixes` (distinct prefixes in the Adj-RIB-Out after output policy for that VRF). Use the distinct count for exclude-set work. The two can differ by a few (counter timing, aggregates, multipath duplicates); a large gap means the join or the VRF is wrong.

and the prefixes a device routes in a VRF (add `where device.name == "..."` and `instance.name == "..."` first: a whole network returns more rows than a query may; this one hit the 1,048,576-row cap):

    foreach device in network.devices
    foreach instance in device.networkInstances
    where isPresent(instance.afts) && isPresent(instance.afts.ipv4Unicast)
    foreach entry in instance.afts.ipv4Unicast.ipEntries
    select { device: device.name, vrf: instance.name, prefix: entry.prefix }

Take the second, keep the public non-default prefixes, subtract the first (filtered to the upstream neighbor), and what remains is the candidate exclude set. Aggregate it, review it with the person, and write it with `edit-internet-exclusions`.

**Read and edit:** `inspect-topology` with `kind: external` and `device: internet` shows the current list (`excluded_subnets`); `edit-internet-exclusions` adds, removes or replaces entries (dry run first, the whole list before and after, written once and read back; the
prior list is the undo). Never write the list with a full `PUT` of the node: that replaces connections, translations and exclusions together.
