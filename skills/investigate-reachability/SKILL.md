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

What each input means, and the fields per object and action: `fwdctl run investigate-reachability --help`, or `reference/inputs.md`. For several endpoints at once, `matrix` searches every ordered pair in one bulk request and names the pairs whose two directions take different paths (asymmetric routing).

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

4. Each hop that Forward evaluated an ACL or a security zone on carries them (`acls` with name, where applied and action; `ingress_zone`, `egress_zone`): they name the rule or zone pair behind a `security_denied`. A hop with none has no such keys: Forward reported none, which is not proof none exist.
5. Report every distinct outcome: the evidence holds one path per classification first (delivered, failures, then `incomplete_model`), then more distinct paths up to 6; identical paths are counted (`identical_paths`), and the limits say how many paths of each classification Forward returned and how many distinct ones were not shown. Compare two networks by those counts, not by the first path.
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

## Reference

Read the file you need, when you need it (where you cannot read files: `fwdctl describe investigate-reachability reference/<file>`).

- [reference/reading-paths.md](reference/reading-paths.md): test both directions, real host addresses, a drop reason to the next read, and an empty result. Read when interpreting a path result.
- [reference/aci-pbr-loop.md](reference/aci-pbr-loop.md): a loop through an ACI service redirect: pcTags, zoning rules, the redirect target (peer-reported, unconfirmed). Read when a loop repeats one leaf.
