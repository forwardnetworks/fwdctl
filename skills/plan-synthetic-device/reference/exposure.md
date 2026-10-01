# Internet exposure and the internet node's exclude list

Source: Forward's own source (class and function names below, read in its repository) and its documentation ("Vulnerability Analysis", "Synthetic Devices" troubleshooting, "Path Analysis"). **UNKNOWN** marks what neither says: test it, never assume.
Also checked read-only on a real network: the connection settings, the public interface addresses and traces from the internet node.

## Contents

The question; the sequence that must have happened first; how `internetAddressable` is computed; why a firewall has no zone; what the exclude list does and does not do; what exposure analysis cannot be told; what to do instead; whether another synthetic device helps; before and after, step by step; what stays unknown.

## The question

"None of our internal equipment should be exposed, yet management and firewall interfaces carry public addresses, and Forward flags those devices as receiving traffic from the internet. Can we exclude the management interface (or, on a firewall, the customer-facing
interface)? Must we invent a second synthetic device?" Short answer: **no per-interface or per-device exclusion exists, the exclude list is the wrong tool for an address that a collected interface carries, and a second synthetic device cannot help.** The flag is a
reachability result: change what the model lets through, or filter the report, not the address.

## The sequence that must have happened first

Exclusions matter only at the end of this chain, so check it from the start (`inspect-snapshots`, `inspect-vulnerabilities`):

1. **The internet is modelled.** An internet node with at least one connection (`inspect-topology` `kind: external`). Without it the answer is `INTERNET_NODE_NOT_DEFINED`.
2. **Advanced reachability is computed for that snapshot** (`inspect-snapshots` shows `advanced_reachability` PROCESSED). It is a separate, heavier step after normal processing. `UNPROCESSED` means it was **never computed** (it is not "still running": that reads PROCESSING), so a month-old snapshot that is UNPROCESSED never ran it.
3. **Forward then computes internet exposure itself.** When the flow DAG is merged, Forward triggers the exposure job (`ReachabilitySubscriberApp`); until then any request for it is answered `PENDING_ADVANCED_REACHABILITY` (`SecurityAnalysisAppService.getInternetExposureAsync`: DAG_MERGE not finished). Only now does `internet_addressable` exist per device.
4. **Only then do exclusions, discovery modes and ACLs change what is flagged**, and each change applies from the next snapshot, which needs steps 2 and 3 again.

**What starts step 2.** The organization property `ADVANCED_REACHABILITY_ANALYSIS`: `ASYNC` starts it automatically after processing (`ReachabilitySubscriberApp`; skipped for predicted snapshots and when the network property `DISABLE_FLOW_COMPUTATION` is set); `ON_DEMAND`, the default, never does. On demand it is
started from the Vulnerability Analysis page ("Run advanced reachability analysis") or by Forward's published `POST /api/snapshots/{id}?action=computeAdvancedReachability`, which `edit-advanced-reachability` calls (dry run first). It is asynchronous (the call returns 204 and the state moves UNPROCESSED, PROCESSING, PROCESSED) and
compute-heavy on a large network (Forward's compute workers; `REACHABILITY_MAX_CONCURRENT_DEVICES_PER_WORKER` bounds devices per worker, `REACHABILITY_TIMEOUT_MINUTES` the run). The call needs a snapshot that finished the reachability stage (otherwise 409).
**Forward ignores the request when the state is already final** (PROCESSED, FAILED, CANCELED, TIMED_OUT; `SnapshotService.processAdvancedReachabilityAnalysis`), so a FAILED run is retried only after a reprocess.
**What a reprocess or backdate does:** invalidating a snapshot resets its stage states to "files saved" (`SnapshotTrackingRepoImpl` invalidate keeps only the accepted and saved-files stages), so its advanced reachability returns to UNPROCESSED and exposure to PENDING; under `ASYNC` it is started again after processing, under `ON_DEMAND` it must be requested again. What
the license or edition does to it is **UNKNOWN** (no facet check was found on this path).

## How `internetAddressable` is computed

(`InternetExposureJob`, `SecurityAnalysisWorkerService.computeInternetExposure`, `InternetExposure`, `InternetExposureError`; docs: Vulnerability Analysis, "How Receives traffic from internet is determined".)

1. **Per snapshot, after advanced reachability.** It is one job that follows the DAG merge (see the sequence above). Until then the answer is the error
   `PENDING_ADVANCED_REACHABILITY` (the API refuses an `internetAddressable` filter with a 400, `inspect-vulnerabilities` answers unknown with the reason, and the page shows **unknown**). The other errors: `INTERNET_NODE_NOT_DEFINED` (no internet node) and `REACHABILITY_COMPUTATION_DISABLED`. The public API can start the analysis (`computeAdvancedReachability`, see the sequence above).
2. **Source:** the internet node's `self` port, as the first device of the flow. The code adds no header constraint of its own: no source, destination, protocol or port filter, only "valid" flows. The internet node itself drops private and reserved sources and private destinations out its `self` port
   (`InternetNodeDeviceGenerator`). **UNKNOWN:** whether the interface filter narrows the source address.
3. **Destination addresses are not listed or probed one by one.** Forward propagates the whole header space from the node through the model, so what can arrive is decided by what the **internet node forwards**: a route per connection subnet (what the discovery mode yields:
   interface addresses, IP routes, BGP routes, or the listed subnets) and a default route to its own `self` port when no connection carries one (`L3SyntheticWanDeviceGenerator`). A public address the node has no route to is dropped at the node.
4. **ACLs, routing and NAT are honoured** at every hop (the search runs in regular ACL mode, so firewall policy and ACLs on the path matter; the docs list ACLs, routing, NAT). A path that is blocked is not exposure.
5. **A device qualifies when a valid path ENDS on one of its own interface addresses** (a terminal node whose output port is the device's `self` port, or a loopback interface), in any VRF. A path that only transits does not count. Forward then confirms the candidate with a path search limited to those ports.
6. **The result is one yes or no per device.** The store keeps **one arbitrary qualifying interface per device** (`InternetExposure.trafficReceivingInterfaces`: "if a device has multiple, one is chosen arbitrarily"). The API field is `internetAddressable` on each device (what `inspect-vulnerabilities`
   `internet_addressable` reads). It says nothing about which interface, and a firewall is flagged if **any** of its interfaces terminates a valid path.

## Why a firewall has no zone, and what the role rests on (inspect-edge, view public_addresses)

Checked read-only on a real network: nearly all of its firewalls were Cisco ASA and almost all of those were virtual contexts (name differs from the physical device), and `device.securityZones` held no row. Forward models security zones only for the platforms that have them (its parsers
fill zones for FTD managed by FMC, Check Point and similar); an ASA has no zone, and its nameif and security level are not NQE fields (the schema has no such interface field). The interface description is populated (it often reads `outside` or `inside`). So `inspect-edge` (`view: public_addresses`) assigns a firewall role only from a zone when one exists, then the
interface description, name or VRF saying inside or outside. The context name suffix (`<chassis>_<agency>-ext-dmz`, `-int-dmz`, `-lb`) and the link's other side (the device type of what the interface connects to) are reported on the row and used to qualify the basis, not to assign a role: a context name says where the context sits, not which side an interface faces,
and on this network the links lead to other contexts, switches and unmodelled devices, which separates nothing. What remains is `unknown` and listed for review (`firewall_role_unknown`, `unknown_by_group`). The skill does not check who owns a public prefix (no registry lookup): candidates carry `ownership_not_verified`, and `prefix_groups` shows the /8 and /16 spread so an odd block stands out.

## What the exclude list does, and does not do

(`IpLocationIndexer.computeInternetNodeSubnets`, `IpLocationIndex.getUnicast`, `InternetNode.subnetsToExclude`.)

- The list is **public CIDR blocks on the internet node only**. It is **prefix-based**: there is no per-interface, per-device or per-address exclusion, and no tag or NQE filter feeding it.
- It changes one thing: which addresses the **location index** assigns to the internet node. Internet-owned space = all public space, minus the subnets the node already routes to its connections, minus the excluded list.
- Location is looked up in a fixed order: **interface-attached subnet first**, then NAT, only then the internet node, then routes. An address a collected interface carries is therefore located on that interface **before** the internet node is considered. Excluding it changes nothing about its location.
- **The exposure computation never reads the list or the location index** (the code above uses the internet node's name, its routes and the path DAG; `subnetsToExclude` appears only in the location index, the node itself and the node merge). So, from the code: **excluding a prefix that is
  on a collected interface does not change whether that interface's device is reported internet addressable.** Not proven live (applying it is a write that these skills only dry-run); the before and after procedure below is how to prove it on your network.
- What the list is for: public space the network routes internally but never advertises to the node, which would otherwise be located at the internet node, so a trace to or from it starts or ends at the node instead of at the internal route.

## What to do instead (in the order to try)

1. **Read what the node really forwards.** `inspect-topology` `kind: external` shows each connection's discovery mode. `interfaceAddresses` sends only the gateway interface's own addresses into the network: the node routes nothing else toward your devices (on a network modelled this way, traces from the internet to internal public addresses ended at the node itself). `ipRoutes` sends every public subnet the gateway
   forwards inward, including management and firewall prefixes that were never advertised to the ISP. `bgpRoutes` sends only what the gateway advertises to its eBGP peers after output policy, which is the closest to "what the internet can reach". If exposure is wider than the true edge, the discovery mode
   or a manual subnets list is the lever; `backdoor ports` subtract subnets a side link carries. (Edit through `edit-synthetic-query` when an NQE query drives the node.)
2. **Trace the flagged address.** `investigate-reachability` with `from: "internet"` and `dst_ip` the address: where the path ends and which ACL or route decides. If it is delivered, the model says the address is reachable: the finding is real for that model. If it is dropped at the internet node, the node has no route to it.
   `inspect-edge` (`view: public_addresses`) lists the candidates with their role so a few can be traced.
3. **Fix the cause in the network, when the path is real:** an ACL or control-plane policy on the edge, moving a management address into a private or management VRF address, a different address on the interface. Then collect again (the answer changes with the next snapshot, never retroactively).
4. **Filter the report, not the model.** `inspect-vulnerabilities` returns per device; keep the devices you have judged not exposed in your own list (for example by tag, `edit-device-tags` plus `inspect-inventory`, or by NQE) and say so. Forward's Custom Vulnerability Labels annotate a CVE and operating system with a status such as
   Mitigated or Accepted risk (docs, Vulnerability Analysis); they annotate the verdict, they do not change `internetAddressable`. **UNKNOWN:** whether the exposure flag is ever hidden by a label.
5. **Use the exclude list only for its purpose** (internal public space nothing else claims), with `edit-internet-exclusions` and its dry run; see `reference/internet-node.md`, "Internal public address space".
6. **Intent checks** (`check-network-compliance`) are independent of the flag; **UNKNOWN:** whether any check or exception can be tied to `internetAddressable`; none was found in the code read.

## Does a second synthetic device help? No

Only the internet node has an exclude list. The intranet node, adjacent network and L3 VPN have no such field in their configuration, and Forward states that an intranet node "never absorbs unassigned public addresses" and that adding one does not change where public addresses land. A second node cannot subtract
space from the internet node, and the exposure source is always the internet node. Do not model a fake device for this.

## Before and after, step by step

1. **Before.** Pick the snapshot (`inspect-snapshots`). Run `inspect-edge` (`view: public_addresses`) for the device group and note its snapshot id and counts. Read `inspect-vulnerabilities` with `internet_addressable: true` (count of exposed devices, or the devices of one CVE); if it answers unknown with `PENDING_ADVANCED_REACHABILITY`, advanced reachability has not been computed
   for that snapshot: start it with `edit-advanced-reachability` (dry run first, asynchronous), wait for `inspect-snapshots` to show PROCESSED, and **that is not "zero exposed"**. Trace 3 to 5 representative addresses with `investigate-reachability` `from: "internet"` and keep each `last_hop` and `classification`.
2. **Change** one thing at a time (a discovery mode, an ACL, an exclusion), with the dry run and approval (`plan-safe-write`).
3. **Get a snapshot that reflects it.** A change applies from the next processed snapshot, or now with `backdate_snapshot_id` on the edit skill (it invalidates that snapshot and the ones after it); `reference/see-a-change.md` says how to prove which snapshot holds it. Advanced reachability must then be computed again for the new snapshot (and PROCESSED) before exposure exists for it.
4. **After.** Repeat the same traces and the same `inspect-vulnerabilities` count on the new snapshot. Compare the path ends, the count and the device list. A trace that did not move, or a count that did not move, means the change was not the cause or has not reached that snapshot.
5. Say what was and was not measured: the snapshot ids, the addresses traced, and that exposure is per device.

## What stays unknown

Whether the interface filter constrains the source address; what a license or edition does to advanced reachability; whether any check or label can be tied to `internetAddressable`; the live effect of excluding a prefix on a device's flag (from the code: none for an address on a collected interface). Test these before promising them.
