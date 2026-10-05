---
name: plan-synthetic-device
description: Sequences the skills that model an uncollected segment as a synthetic device. Use when a path dead-ends at the edge.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "edge-synthetic"
  summary: "model an uncollected segment"
  maturity: "1"
  tools: "inspect-snapshots, investigate-reachability, inspect-vulnerabilities, edit-advanced-reachability, inspect-topology, inspect-inventory, author-nqe-query, validate-nqe-query, edit-nqe-query, edit-synthetic-query, edit-wan-circuit"
---

# plan-synthetic-device

A procedure, not a skill that runs. A synthetic device stands in for a segment Forward cannot collect so paths, exposure and change checks can cross it instead of stopping at the edge. It is a statement you make,
not something Forward observed. Read the reference file a step names only when you reach that step (where you cannot read files: `fwdctl describe plan-synthetic-device reference/<file>`).

## Steps

1. **See the gap.** `inspect-snapshots`, then `investigate-reachability` for the flow that dead-ends (a path that ends at the edge device with no next hop). `inspect-topology` with `kind: external` shows what is already modelled,
   each node's NQE query and what it generated, and Forward's suggested internet connections. If the person asks why to model it: [reference/why.md](reference/why.md).
2. **Choose the type** from what the segment does, not what it is called (a trace that ends DROPPED at an L3 VPN on a VRF named like the internet means the internet is modelled as a VPN VRF: [reference/internet-node.md](reference/internet-node.md)). [reference/types.md](reference/types.md) has the table and the deciding questions (L2 or L3, public or private, transit or terminating).
3. **Find the attachment points** with `inspect-inventory` and `inspect-device-files`: the uplink port, the gateway interface, the VLAN, the subnets each site owns. [reference/connections.md](reference/connections.md) explains each field.
4. **Build the connections as a query** when there are more than a few. For an internet node the rows can be derived from evidence: `fwdctl nqe synthesize internet --network <id> [--vrf V] [--device D] [--discovery interfaceAddresses|bgpRoutes|ipRoutes|none] [--include-unlikely]`
   runs the `inspect-edge` analysis and prints a ready query (one row per likely internet edge, parent port as the uplink, the subinterface as the gateway, the VLAN read from its name, a comment header with the snapshot, the reasons, what the discovery choice means and the claimants that would make it a double claim; it lints its own output and exits non-zero if it is not clean). Otherwise: `fwdctl nqe template <kind>`, write the rows (`author-nqe-query`), `fwdctl nqe lint --synthetic <kind>`, `validate-nqe-query`, then save with `edit-nqe-query`
   (follow `plan-safe-write`; the commit is visible to the whole organization). The row types are in `author-nqe-query` `reference/synthetic-devices.md`.
5. **Preview and attach** a query-driven node with `edit-synthetic-query`, or define a WAN circuit (exactly two connections, by hand) with `edit-wan-circuit` (follow `plan-safe-write`). Forward's own compute needs a query that is **committed** to the library (visible to the whole organization: save it under a clearly named scratch path such as `/Scratch/...` and delete it after if it is only a trial). The dry run computes the query without attaching it and shows the count, the first rows and any error; a query Forward rejects is refused.
6. **Verify.** (The fastest safe way to see a change in a trace, and how to pick a test source: [reference/see-a-change.md](reference/see-a-change.md).) Connections apply from the next processed snapshot (`plan-snapshot-recovery`, `edit-collection`), or now if the person wants it: pass `backdate_snapshot_id` to the edit skill, which invalidates that snapshot and every later one so they reprocess (their answers are unavailable meanwhile; get approval of the backdate itself). Then `investigate-reachability` for the original flow and `inspect-topology` `kind: external`. A path that now continues is the
   proof; one that still stops points at a missing subnet, a VRF mismatch or a Missing Peer.

7. **Exposure questions** ("our management or firewall interfaces have public addresses and Forward flags the devices internet addressable; can we exclude an interface, or add a second synthetic device?"): [reference/exposure.md](reference/exposure.md). It says how the flag is computed, that the exclude list is prefix-based and does not hide an address a collected interface carries, that no other synthetic device can help, what to do instead, and the before and after procedure. `inspect-edge` (`view: public_addresses`) lists the addresses with their role first.

## Freedom

How much room each step gives (levels are defined in `plan-investigation`):

- **Fixed** (steps 4, 5, 6): committing the query, attaching it or a circuit, and a backdate are writes (a committed query is visible to the whole organization; a backdate reprocesses snapshots): `plan-safe-write`, dry run first
- **Guided** (steps 1, 3, 7): see the dead-ending flow first; the exposure questions follow `reference/exposure.md`
- **Open** (steps 2): choose the type from what the segment does, not from names; build the rows from evidence

## The minimum

Before attaching: the dead-ending flow, the type and why, and a preview with no error. After: the same flow re-run on a new snapshot.

## Answering

State the type and why, the connection count and a few rows, the preview result, what paths can now do, and what is not verified: the segment's real behaviour beyond what was described, and that this is a preview feature of Forward. Never change a device.
