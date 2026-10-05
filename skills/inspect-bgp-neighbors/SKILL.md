---
name: inspect-bgp-neighbors
description: Lists BGP neighbors per device and VRF with peer, remote AS, session state, prefix counts and whether the peer is modelled. Use when asked who a device peers with or who the upstream is.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "inventory-topology"
  summary: "BGP peers and sessions"
  maturity: "1"
  class: read
  reversible: "n/a"
  tools: "nqe"
---

# inspect-bgp-neighbors

## Intent

The peers a collected device has that are not themselves collected are the unmodelled network beyond it: the upstream, a provider, a partner. This skill lists the neighbors with what is needed to identify them, unmodelled peers first, and pairs each with the number of routes
advertised to it after output policy, for the neighbor's own VRF.

## Inputs

`network_id`. Optional filters: `device`, `vrf`, `peer` (an address), `peer_as`, `state` (for example `ESTABLISHED`), `unmodelled_only`; `skip_adj_rib_out`; paging `limit` (default 50, at most 200) and `offset`; `snapshot_id`. See `schema.json`.

**`advertised`** (an object; needs `device` and `peer`, and `vrf` for a non-default VRF) answers a different question: which prefixes this device sends to this peer. It lists them instead of neighbor rows, as the minimal set (a prefix inside another advertised prefix is dropped), paged by `limit` (default 100, at most 1,000) and `offset`, with `distinct_prefixes` and `after_dropping_covered` counts and `by_containing_16`. `advertised: {"outside": ["204.64.0.0/14", ...]}` keeps only prefixes not inside any given block (a prefix wider than a block counts as outside). The result may include private, shared or documentation space the device advertises (`edit-internet-exclusions` refuses those), so review it before using it as an exclusion set. Each prefix carries `origin_type` (`local` when its next hop is 0.0.0.0, `learned` otherwise: re-advertised from a peer), `next_hop`, `origin_code` and `as_path`, with summaries `by_origin_type`, `learned_by_next_hop` and `by_origin_code`. Communities and route-map clauses are not in Forward's model: `reference/policy.md` (`fwdctl describe inspect-bgp-neighbors reference/policy.md`) says how to read the neighbor's policy from the device files and why an advertisement to an upstream is not proof of internet intent. Read from the Adj-RIB-Out after output policy, IPv4 unicast, for that peer address and VRF; only Junos, IOS, IOS-XE, NX-OS and IOS-XR devices report it, and no rows is **unknown**, not 'sends nothing'.

## Procedure

1. Read the snapshot; none ready is **unknown**.
2. Read every BGP neighbor (paged, bounded) and, unless skipped, the Adj-RIB-Out route and distinct-prefix counts per device, peer, address family and route VRF (null is the default VRF); the row takes IPv4 unicast of the neighbor's own VRF.
3. Filter, count by state and by peer AS, order unmodelled peers first, page. No match is **unknown** (a wrong filter and no BGP look the same).

## Limits worth stating

State and prefix counts are null where a platform does not report them; an empty `peer_device` means the peer is not a collected device, not that the session is down; `adj_rib_out_*` exists only for Junos, IOS, IOS-XE, NX-OS and IOS-XR. A BGP neighbor in the model is per device, not per VRF, and its Adj-RIB-Out holds routes of several VRFs and families, so counts are joined on device, peer and VRF; `adj_rib_out_other_vrfs_routes` is the remainder for that peer address in other VRFs and families, so nothing is hidden. Which number to trust: `advertised_prefixes` is the device's own session counter (what it reports sending); `adj_rib_out_distinct_prefixes` is the count of distinct prefixes in the Adj-RIB-Out after output policy for that VRF and is the one to use for exclude-set work. They can differ by a few (counter timing, aggregates, multipath duplicates); a large gap means the join or the VRF is wrong. The finding and the evidence name the snapshot read.

## Evidence

One `nqe` item: `total`, `unmodelled_peers`, `by_state`, `by_peer_as`, `offset` and `neighbors` (each: `device`, `vrf`, `peer`, `peer_as`, `local_as`, `state`, `description`, `peer_device`, `advertised_prefixes`, `received_prefixes`, `adj_rib_out_routes`, `adj_rib_out_distinct_prefixes`, `adj_rib_out_other_vrfs_routes`) and `snapshot_id`.

## Output

The envelope in `schema/skill-result.schema.json`.

## Next actions

`inspect-edge` for the handoffs; `plan-synthetic-device` (`reference/internet-node.md`) to derive an exclude set from what is advertised.

## Running this skill

Give the inputs as one JSON object on stdin: `echo '{"network_id": "<id>", "unmodelled_only": true, "device": "edge1"}' | fwdctl run inspect-bgp-neighbors`.
It needs the `fwdctl` binary and `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD` in the environment. Read `status`
(`ok`, `failed`, `unknown`, `error`) and the `limits`; `unknown` is never a pass.
