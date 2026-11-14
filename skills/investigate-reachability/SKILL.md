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

`network_id` and `dst_ip` are required; give `src_ip` or `from` (a device) for the source. Each input, with examples for each form of the call: `fwdctl run investigate-reachability --help`, or `reference/inputs.md`. Forms of the call: one flow (the default); `return_path` for both directions; `matrix` for every pair of several endpoints, naming the pairs that take different paths each way; `explain: routes` for each hop's forwarding entry, checked against the path; `explain: trace` (preview) for Forward's GUI trace of each path. To compare one flow before and after a change: `verify-change` view `flows`.

## Procedure

1. Resolve the snapshot: the one given, otherwise the newest **processed** one. None: stop, the result is `unknown`.
2. Run Forward's path search (preferring delivered flows), bounded by `max_results` and `max_seconds`.
3. Classify each path from its forwarding and security outcomes: `delivered`, `security_denied`, `missing_route`, `dropped`, `not_admitted`, `routing_loop`, `unreachable` or `incomplete_model` (the table is in reference/procedure.md).
4. Report every distinct outcome, not the first path: one path per classification first, then more distinct paths up to 16; identical paths are counted. Compare two networks by those counts.
5. Decide: any `delivered` path is **ok**; none delivered and the search completed is **failed** (the first path's last hop is the first failure point); no paths, a timeout with no delivered path, or only `incomplete_model` is **unknown** (zero paths is not proof of no connectivity). With `return_path: true` the status is about both directions. A last hop named `<device>-missing-peer` means an unmodelled peer: the status stays **unknown** and `next_actions` name `plan-synthetic-device` and `inspect-edge`. The ACL, zone and return-path details are in reference/procedure.md.
6. Report what was not measured (`limits`): truncation, timeout, predicted snapshot.

## Evidence

One `path` item per cited path: classification, both outcomes, hop count, the device
sequence, and the first-failure device and interfaces (plus `missing_peer` when the last hop is a Missing Peer). Each item names the operation
and the snapshot it came from.

## Output

The envelope in `schema/skill-result.schema.json`. `next_actions` suggests
`verify-change` after a fix, or `investigate-collection-failure` when the model looks
incomplete; `plan-synthetic-device` and `inspect-edge` when the last hop is a Missing Peer.

## Next actions

`verify-change`, `investigate-collection-failure`, `verify-change` (view `impact`).

## Running this skill

Run `fwdctl run investigate-reachability --help` for the inputs and an example, then give them as one JSON object on stdin: `fwdctl run investigate-reachability < input.json`. Read `status` (`ok`, `failed`, `unknown`, `error`) and the `limits`; `unknown` is never a pass.

## Reference

Read the file you need, when you need it (where you cannot read files: `fwdctl describe investigate-reachability reference/<file>`).

- [reference/procedure.md](reference/procedure.md): the classification table, ACL and zone evidence, the status rules for `return_path`, and the Missing Peer marker. Read when you classify a path or decide a status.
- [reference/inputs.md](reference/inputs.md): every input with an example per form of the call. Read when you build a call beyond `src_ip`, `dst_ip` and ports.
- [reference/reading-paths.md](reference/reading-paths.md): test both directions, real host addresses, a drop reason to the next read, and an empty result. Read when interpreting a path result.
- [reference/aci-pbr-loop.md](reference/aci-pbr-loop.md): a loop through an ACI service redirect: pcTags, zoning rules, the redirect target (peer-reported, unconfirmed). Read when a loop repeats one leaf.
