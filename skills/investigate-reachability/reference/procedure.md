# investigate-reachability: procedure and how to read the verdict

The full procedure, the classification table and the decision rules. SKILL.md holds the short form; read this when you classify a path, decide the status, or meet a Missing Peer.

## Procedure (full)

1. Resolve the snapshot. Use the one given, otherwise the newest **processed** one. If
   there is none, stop: the result is `unknown`.
2. Run a path search preferring delivered flows, bounded by `max_results`/`max_seconds`.
3. Classify each returned path from its forwarding and security outcomes:

   | Forwarding outcome | Security outcome | Classification |
   |---|---|---|
   | DELIVERED | PERMITTED | `delivered` |
   | DELIVERED | DENIED | `security_denied` (ACL, firewall or zone policy) |
   | BLACKHOLE | any | `missing_route` (no matching rule at the last hop) |
   | DROPPED | any | `dropped` (explicit drop, for example a null route or deny) |
   | INADMISSIBLE | any | `not_admitted` (first hop does not accept the traffic: VLAN, VRF or interface state) |
   | LOOP | any | `routing_loop` |
   | UNREACHABLE | any | `unreachable` |
   | DELIVERED_TO_INCORRECT_LOCATION | any | `incomplete_model` (a device on the path was probably not collected) |

4. Each hop that Forward evaluated an ACL or a security zone on carries them (`acls` with name, where applied and action; `ingress_zone`, `egress_zone`): they name the rule or zone pair behind a `security_denied`. A hop with none has no such keys: Forward reported none, which is not proof none exist. Hops also carry `behaviors`, VRFs and, on the last, `path_ends_here` (reference/reading-paths.md).
5. Report every distinct outcome: the evidence holds one path per classification first (delivered, failures, then `incomplete_model`), then more distinct paths up to 16; identical paths are counted (`identical_paths`), and the limits say how many paths of each classification Forward returned and how many distinct ones were not shown. Compare two networks by those counts, not by the first path.
6. Decide:
   - Any `delivered` path: **ok**, deterministic.
   - No `delivered` path and the search completed: **failed**, with the classification of
     the first path and its last hop (device, ingress and egress interface) as the first
     failure point.
   - No paths, a timed-out search with no delivered path, or only `incomplete_model`:
     **unknown**. Zero paths is not proof of no connectivity; the source or destination
     may not have been located.
   - With `return_path: true` the question is both directions: Forward also searches the return flow and the status is about both. Forward delivered and the return flow delivered: **ok**. Forward delivered and the return flow blocked: **failed** (the connection cannot complete). Forward delivered and no return path, a timeout or only incomplete outcomes on the return: **unknown**. A forward failure keeps its own status. The `return_flow` summary (paths, how many delivered, verdict) is in the evidence in every case.
   **Missing Peer.** A last hop named `<device>-missing-peer` is Forward's marker for an uplink of `<device>` that carries traffic (a VLAN or subinterface) to a peer no
   synthetic device claims; Forward creates it automatically. Its ingress interface is written `<device>/<interface>` (for example `po1000`, or `po1000.698`, whose number after the
   dot is the VLAN). The finding then says plainly that traffic leaves `<device> <interface>` toward a peer that is not modelled, and the path evidence carries
   `missing_peer: {device, interface, vlan (only when the interface name carries one), hop_name}`. `next_actions` add `plan-synthetic-device` (model the peer) and `inspect-edge`
   (finds the uplinks and says which a synthetic node claims). The classification stays `incomplete_model` and the status **unknown**: a Missing Peer explains the gap, it is not a verdict.
7. Report what was not measured (`limits`): truncation, timeout, predicted snapshot.
