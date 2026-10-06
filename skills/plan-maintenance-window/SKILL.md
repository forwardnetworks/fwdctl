---
name: plan-maintenance-window
description: Sequences before snapshot, change, after data and comparison around a window. Use when planning or closing a maintenance window or cutover.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "change"
  summary: "before and after a change"
  maturity: "2"
  tools: "inspect-snapshots, edit-snapshot, edit-change-set, verify-change, edit-collection, inspect-history, compare-device-config, check-network-compliance"
---

# plan-maintenance-window

A procedure, not a skill that runs. Forward does not push the change; it brackets it with evidence.

## Before

1. **Baseline.** `inspect-snapshots` for the newest processed collected snapshot; if it is old, `edit-collection` for a fresh one (follow `plan-safe-write`) and wait for it.
2. **Label it.** `edit-snapshot` (action note) ("before change X") so the baseline is findable later (follow `plan-safe-write`).
3. **Predict.** If the commands are known, `plan-change-review` predicts the effect before anyone touches a device.
4. **Write down what must stay true.** The flows that must keep working and the policy that must hold: these become `expectations` for the after-check.

## After

5. **Collect.** `edit-collection` and wait; confirm with `inspect-snapshots` that the new snapshot is processed, not merely started.
6. **What changed.** `compare-device-config` between the baseline and the new snapshot: only the devices the window planned to touch should differ; anything else is a finding.
7. **What broke.** `verify-change` with the saved expectations (before and after snapshot ids) and `check-network-compliance` for checks that got worse.
8. **Close.** `edit-snapshot` (action note) on the new snapshot ("after change X, verified"), or `plan-troubleshoot-connectivity` for what failed.

## Freedom

How much room each step gives (levels are defined in `plan-investigation`):

- **Fixed** (steps 1, 2, 5, 8): collecting and labelling snapshots are writes: `plan-safe-write`, dry run first; the after-snapshot must be processed, not merely started.
- **Guided** (steps 3, 4, 6, 7): keep the order; the expectations are the person's, written down before the after-check.

## Answering

Per expectation: held or violated, with the snapshots compared. Say what was not covered (flows nobody listed). An unprocessed or missing after-snapshot makes the whole result **unknown**.

## Worked example

[reference/example.md](reference/example.md) shows a good run end to end (illustrative, results shortened). Read it when you are unsure what a finished answer looks like; where you cannot read files, run `fwdctl describe plan-maintenance-window reference/example.md`.
