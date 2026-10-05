---
name: plan-segmentation-check
description: Sequences the skills that verify zone segmentation and change impact. Use when asked whether zones are isolated.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "security"
  summary: "zone isolation"
  maturity: "2"
  tools: "inspect-snapshots, inspect-inventory, investigate-reachability, verify-change, check-network-compliance, edit-checks"
---

# plan-segmentation-check

A procedure, not a skill that runs. Segmentation is intent: write down the flows that must be blocked and the flows that must work, then test both.

## Steps

1. **Data.** `inspect-snapshots`: the snapshot and its age.
2. **State the intent.** The zones as address ranges or names (`inspect-inventory` resolves hosts and devices to addresses; `inspect-topology` kind `zones` lists the security zones Forward derived for each device), the flows that must be **blocked**, and the flows that must **work** (a rule that blocks too much is also a failure).
3. **Test each flow.** `investigate-reachability` per flow with the protocol and port. A must-block flow that is delivered is a violation: the path shows the rule that failed to stop it. A must-work flow that is dropped shows the rule that did.
4. **Test in bulk.** For many pairs, express the policy as an NQE check or a predefined isolation check and read it with `check-network-compliance`; that scales better than one path query per pair.
5. **Keep it.** `edit-checks` to make the rule a standing check (follow `plan-safe-write`).
6. **After a change.** `verify-change` with the same flows as `expectations`: it says whether the change broke or opened any of them.

## Freedom

How much room each step gives (levels are defined in `plan-investigation`):

- **Fixed** (steps 5): a standing check is a write: `plan-safe-write`, dry run first.
- **Guided** (steps 1, 3, 6): keep the order; every flow is tested with its protocol and port.
- **Open** (steps 2, 4): the intent comes from the person; choose per-flow or bulk testing by how many pairs there are.

## Answering

Each flow: expected, observed, snapshot, and for a violation the first rule or route responsible. State which zones and protocols were not tested.

## Reference

Read the file you need, when you need it (where you cannot read files: `fwdctl describe plan-segmentation-check reference/<file>`).

- [reference/check-vacuity.md](reference/check-vacuity.md): checks that pass without testing anything, real endpoints, cutover and invariant checks (unverified behaviours, labelled). Read before trusting or staging a check.
