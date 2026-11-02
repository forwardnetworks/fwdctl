---
name: plan-security-posture
description: Sequences security posture skills: failing checks, CVEs, internet exposure, zone isolation. Use for a security review or segmentation.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "security"
  summary: "security review, zone isolation"
  maturity: "2"
  tools: "inspect-snapshots, check-network-compliance, inspect-vulnerabilities, investigate-reachability, inspect-topology, inspect-device-files, plan-vulnerability-response, plan-synthetic-device, inspect-inventory, verify-change, edit-checks"
---

# plan-security-posture

A procedure, not a skill that runs. Posture is a claim about measured things: say what was measured, on which snapshot, and what was not.

## Steps

1. **Data.** `inspect-snapshots`: the newest processed collected snapshot and its age.
2. **Policy.** `check-network-compliance` with `view: read`: failing checks first (security-tagged checks included), then what each found. If the person names a rule Forward has no check for, express it as an NQE check (`check-network-compliance` with `nqe`).
3. **Vulnerabilities.** `inspect-vulnerabilities` network view: worst first, known-exploited first, internet-addressable first. For one CVE or one device, follow `plan-vulnerability-response`.
4. **Exposure.** `investigate-reachability` with `from` "internet" to the addresses that must not be reachable (management, databases, internal ranges); each delivered flow is a finding. `inspect-topology` kind external shows how the internet and other external networks are modelled: if they are not (no internet node, no adjacent networks for partner links), say that exposure is not measured, and that modelling them (`plan-synthetic-device`) is what makes it measurable.
5. **Configuration hygiene.** `inspect-device-files` for specific lines (an open management protocol, a default credential pattern, a permissive any-any rule), searched, not dumped. To survey many devices for permissive rules, see `reference/permissive-rules.md` (an NQE query checked on small networks); for what to look at on one device, `fwdctl describe inspect-device-files reference/footguns.md`.

## Zone segmentation

When the question is whether zones are isolated, segmentation is intent: write down the flows that must be **blocked** and the flows that must **work** (a rule that blocks too much is also a failure), then test both.

- **State the intent.** The zones as address ranges or names (`inspect-inventory` resolves hosts and devices to addresses; `inspect-topology` kind `zones` lists the security zones Forward derived for each device).
- **Test each flow.** `investigate-reachability` per flow with the protocol and port. A must-block flow that is delivered is a violation, and the path shows the rule that failed to stop it; a must-work flow that is dropped shows the rule that did.
- **Test in bulk.** For many pairs, express the policy as an NQE check or a predefined isolation check and read it with `check-network-compliance`. Read [reference/check-vacuity.md](reference/check-vacuity.md) first: a check can pass without testing anything. `edit-checks` keeps it as a standing check (`plan-safe-write`).
- **After a change.** `verify-change` with the same flows as `expectations`: it says whether the change broke or opened any of them.

Answer each flow: expected, observed, snapshot, and for a violation the first rule or route responsible; state which zones and protocols were not tested. A finished run: [reference/segmentation-example.md](reference/segmentation-example.md).

## Freedom

How much room each step gives (levels are defined in `plan-investigation`):

- **Guided** (steps 1, 2, 3): keep the order; failing and known-exploited first.
- **Open** (steps 4, 5): choose the addresses to test from the internet and the config lines to search; searched, not dumped.

## The minimum

Before you answer, take at least: policy (step 2), vulnerabilities (step 3) and exposure (step 4). Stop earlier only when the evidence already answers the question, and say so.

## Answering

Findings ranked by exposure (internet-reachable and known-exploited first), each with its evidence. Group findings that share a root cause (one missing policy behind forty failing checks is one finding with a count), and put what is in good order in a single line. State every area as ok, failed or unknown: an area with no check, no samples or an unmodelled edge is unknown, never clean. Recommend; the only write here is a standing segmentation check, and it follows `plan-safe-write`.

## Worked example

[reference/example.md](reference/example.md) shows a good run end to end (illustrative, results shortened). Read it when you are unsure what a finished answer looks like; where you cannot read files, run `fwdctl describe plan-security-posture reference/example.md`.
