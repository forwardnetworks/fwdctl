---
name: plan-troubleshoot-connectivity
description: Sequences the skills that find why traffic does not reach a destination. Use when asked why A cannot reach B.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "investigate"
  summary: "why A cannot reach B"
  maturity: "2"
  tools: "inspect-snapshots, inspect-inventory, investigate-reachability, inspect-topology, inspect-device-files, investigate-collection-failure, plan-what-changed"
---

# plan-troubleshoot-connectivity

A procedure, not a skill that runs. Forward answers from a collected model, so first make sure the model is current, then ask the path search, then explain.

## Steps

1. **Is the data current?** `inspect-snapshots`: the newest processed collected snapshot and its age. If the problem started after it, the answer is about the past; say so, and consider `plan-snapshot-recovery`.
2. **Pin down the flow.** Source, destination, protocol and port. When you have names, not addresses, find them first with `inspect-inventory` (kind hosts or devices). Internet-bound: destination 8.8.8.8; inbound from the internet: `from` "internet".
3. **Ask the path search.** `investigate-reachability`: delivered or not, the first failure and its reason (an ACL, a missing route, a down interface, a NAT or VRF mismatch). Read every returned path, not only the first.
4. **Explain the failing hop.** `inspect-topology` for what the failing device connects to (is the link there, is it a manual override); `inspect-device-files` for the literal config line behind the reason (search it, do not dump it).
5. **Is it new?** `plan-what-changed` for the interval where the flow last worked.
6. **Is the model complete?** A path that stops at the edge device with no next hop may be a gap in the model (an uncollected provider core, the internet, a partner network), not a real failure: say so, and use `plan-synthetic-device`. A device that failed collection also leaves a gap: `investigate-collection-failure` (view devices) names it and the cause, and (view neighbors) lists the unmodelled neighbours, BGP peers first.
7. **Cloud.** When an end is a cloud instance, `inspect-inventory` kind `cloud` finds its VPC, then `cloud_routes` (the subnet's route table and next hop), `cloud_security` (the security group rules at both ends, in both directions) and `cloud_gateways` (peering, VPN, internet or NAT gateway). Say that the path search was not shown to trace between cloud instances, and that network ACLs, cloud firewalls and transit gateways need `author-nqe-query`.
8. **Try the neighbours.** If the flow is dropped, test the reverse flow and a flow to a sibling destination: a one-way or partial failure points to a different cause.

## Freedom

How much room each step gives (levels are defined in `plan-investigation`):

- **Fixed** (steps 1, 3): the age of the data and the path search always come first.
- **Guided** (steps 2): pin down source, destination, protocol and port before asking.
- **Open** (steps 4, 5, 6, 7, 8): what to look at after the failing hop depends on the reason it gives; say what was not looked at.

## The minimum

Before you answer, take at least: the age of the data (step 1), the path search (step 3) and, when the flow fails, one explanation of the failing hop (step 4: the topology or the device text). Stop earlier only when the evidence already answers the question, and say so.

## Answering

Name the first failing device and the reason in Forward's words, the snapshot, and what was not modelled (a device Forward cannot collect, traffic that depends on dynamic state). A delivered flow does not prove the application works. Recommend a fix, never apply one to a device.

## Worked example

[reference/example.md](reference/example.md) shows a good run end to end (illustrative, results shortened). Read it when you are unsure what a finished answer looks like; where you cannot read files, run `fwdctl describe plan-troubleshoot-connectivity reference/example.md`.

## Reference

Read the file you need, when you need it (where you cannot read files: `fwdctl describe plan-troubleshoot-connectivity reference/<file>`).

- [reference/bgp-session.md](reference/bgp-session.md): BGP session down, nothing learned or nothing sent: the rungs in order, and what Forward does not model. Read for a BGP question.
