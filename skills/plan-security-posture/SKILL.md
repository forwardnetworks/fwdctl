---
name: plan-security-posture
description: Sequences the skills that assess security posture: failing checks, CVEs, internet exposure. Use for a security review.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "security"
  summary: "security review"
  maturity: "2"
  tools: "inspect-snapshots, check-network-compliance, inspect-vulnerabilities, investigate-reachability, inspect-topology, inspect-device-files, plan-vulnerability-response"
---

# plan-security-posture

A procedure, not a skill that runs. Posture is a claim about measured things: say what was measured, on which snapshot, and what was not.

## Steps

1. **Data.** `inspect-snapshots`: the newest processed collected snapshot and its age.
2. **Policy.** `check-network-compliance` with `view: read`: failing checks first (security-tagged checks included), then what each found. If the person names a rule Forward has no check for, express it as an NQE check (`check-network-compliance` with `nqe`).
3. **Vulnerabilities.** `inspect-vulnerabilities` network view: worst first, known-exploited first, internet-addressable first. For one CVE or one device, follow `plan-vulnerability-response`.
4. **Exposure.** `investigate-reachability` with `from` "internet" to the addresses that must not be reachable (management, databases, internal ranges); each delivered flow is a finding. `inspect-topology` kind external shows how the internet and other external networks are modelled: if they are not (no internet node, no adjacent networks for partner links), say that exposure is not measured, and that modelling them (`plan-synthetic-device`) is what makes it measurable.
5. **Configuration hygiene.** `inspect-device-files` for specific lines (an open management protocol, a default credential pattern, a permissive any-any rule), searched, not dumped.

## The minimum

Before you answer, take at least: policy (step 2), vulnerabilities (step 3) and exposure (step 4). Stop earlier only when the evidence already answers the question, and say so.

## Answering

Findings ranked by exposure (internet-reachable and known-exploited first), each with its evidence. State every area as ok, failed or unknown: an area with no check, no samples or an unmodelled edge is unknown, never clean. Recommend; do not change anything.

## Worked example

[reference/example.md](reference/example.md) shows a good run end to end (illustrative, results shortened). Read it when you are unsure what a finished answer looks like; where you cannot read files, run `fwdctl describe plan-security-posture reference/example.md`.
