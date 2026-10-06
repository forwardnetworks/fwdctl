---
name: plan-change-review
description: Sequences read, predict, check and reach for a change. Use when asked whether a change is safe or will break anything.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "change"
  summary: "is a change safe"
  maturity: "3"
  tools: "edit-change-set, verify-change, investigate-reachability, check-network-compliance, inspect-snapshots"
---

# plan-change-review

A procedure, not a skill that runs. It orders other skills; each still returns its own evidence, and none of them touches a device.

## Steps

1. **Pick the base.** `inspect-snapshots` for the newest processed collected snapshot, and say how old it is. A stale base makes the whole review stale.
2. **Get the change as a change set.** If one exists, `verify-change` with `view: describe`. If only commands or advertisements were supplied, stage them with `edit-change-set` (follow `plan-safe-write`: dry run, show it, then apply after approval). Never write the configuration yourself: stage only what the person supplied.
3. **Predict it.** `verify-change` with `run_predict` (it costs compute and leaves a predicted snapshot that cannot be deleted; say so before running).
4. **Judge it.** `verify-change` with the flows that must keep working and the flows that must stay blocked as `expectations`.
5. **Measure the reach.** `verify-change` with `view: impact`; read the areas that differ and the subnet pairs gained or lost.
6. **Check policy.** `check-network-compliance` with `view: read` to see which checks got worse; evaluate a specific policy if one was named.
7. **Explain any surprise.** `investigate-reachability` on a flow that changed.

## Freedom

How much room each step gives (levels are defined in `plan-investigation`):

- **Fixed** (steps 2, 3): staging with `edit-change-set` and running the predict follow `plan-safe-write`; say the predicted snapshot cannot be deleted before you run it; stage only what the person supplied.
- **Guided** (steps 1, 4, 5, 6): keep the order; choose the flows, expectations and checks from what the person said.
- **Open** (steps 7): explaining a surprise is judgment: pick the flow that changed and the skill that shows why.

## Answering

State the verdict per step. **unknown** at any step means that part is not measured: say what is missing instead of calling the change safe. Safe means every expected flow behaved, nothing regressed and the reach matched what was intended, on a base you named.

## Worked example

[reference/example.md](reference/example.md) shows a good run end to end (illustrative, results shortened). Read it when you are unsure what a finished answer looks like; where you cannot read files, run `fwdctl describe plan-change-review reference/example.md`.
