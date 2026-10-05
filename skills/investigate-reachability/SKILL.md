---
name: investigate-reachability
description: Explains whether traffic from a source to a destination is delivered and where it first fails, from Forward's path search. Use when asked whether A can reach B or why a flow is dropped.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "investigate"
  summary: "is a flow delivered"
  maturity: "3"
  tools: "snapshots, paths"
---

# investigate-reachability

## Intent

Answer one question from the digital twin: **is this flow delivered, and if not, what
is the first failure and why?** The answer is read from Forward's path search, not
guessed from device output.

## Inputs

`network_id` and `dst_ip` are required. Give `src_ip` or `from` (a device) to anchor the
source. Optional: `protocol` (`tcp|udp|icmp` or a number), `src_port`, `dst_port`,
`snapshot_id` (default: the newest processed snapshot), `max_results`, `max_seconds`, `intent` (`PREFER_DELIVERED` default, `PREFER_VIOLATIONS`, `VIOLATIONS_ONLY`: how Forward selects the paths it returns; to see paths a default answer lacks, compare intents).
`from` names a device only (Forward's path search has no ingress-interface input; confirmed from its source): it may start the flow on any of the device's interfaces, a loopback among them. To start from one interface or subnet give `src_ip` (Forward resolves it to a location), alone or with `from`. Every path's evidence carries `source_hop` (the first hop's device and the interface the packet entered by) and the full `hops` list, so keep the paths whose `source_hop.ingress_interface` is the one you mean.
See `schema.json`.

## Procedure

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

4. Report every distinct outcome: the evidence holds one path per classification first (delivered, failures, then `incomplete_model`), then more distinct paths up to 6; identical paths are counted (`identical_paths`), and the limits say how many paths of each classification Forward returned and how many distinct ones were not shown. Compare two networks by those counts, not by the first path.
5. Decide:
   - Any `delivered` path: **ok**, deterministic.
   - No `delivered` path and the search completed: **failed**, with the classification of
     the first path and its last hop (device, ingress and egress interface) as the first
     failure point.
   - No paths, a timed-out search with no delivered path, or only `incomplete_model`:
     **unknown**. Zero paths is not proof of no connectivity; the source or destination
     may not have been located.
   **Missing Peer.** A last hop named `<device>-missing-peer` is Forward's marker for an uplink of `<device>` that carries traffic (a VLAN or subinterface) to a peer no
   synthetic device claims; Forward creates it automatically. Its ingress interface is written `<device>/<interface>` (for example `po1000`, or `po1000.698`, whose number after the
   dot is the VLAN). The finding then says plainly that traffic leaves `<device> <interface>` toward a peer that is not modelled, and the path evidence carries
   `missing_peer: {device, interface, vlan (only when the interface name carries one), hop_name}`. `next_actions` add `plan-synthetic-device` (model the peer) and `inspect-edge`
   (finds the uplinks and says which a synthetic node claims). The classification stays `incomplete_model` and the status **unknown**: a Missing Peer explains the gap, it is not a verdict.
5. Report what was not measured (`limits`): truncation, timeout, predicted snapshot.

## Evidence

One `path` item per cited path: classification, both outcomes, hop count, the device
sequence, and the first-failure device and interfaces (plus `missing_peer` when the last hop is a Missing Peer). Each item names the operation
and the snapshot it came from.

## Output

The envelope in `schema/skill-result.schema.json`. `next_actions` suggests
`verify-change` after a fix, or `investigate-collection-failure` when the model looks
incomplete; `plan-synthetic-device` and `inspect-edge` when the last hop is a Missing Peer.

## Next actions

`verify-change`, `investigate-collection-failure`, `analyze-blast-radius`.

## Running this skill

Give the inputs as one JSON object on stdin: `echo '{"network_id": "<id>"}' | fwdctl run investigate-reachability`. It needs the
`fwdctl` binary and `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD` in the environment. Read `status`
(`ok`, `failed`, `unknown`, `error`) and the `limits`; `unknown` is never a pass. Inputs are listed above and in `schema.json`.
