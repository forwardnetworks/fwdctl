---
name: verify-change
description: Judges a change: view verify (worked? broke nothing?), describe (what a change set edits), impact (how far it reaches). Use when asked if a change is safe or what a change set does.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "change"
  summary: "verify, describe, size a change"
  maturity: "3"
  tools: "snapshots, changes, paths"
---

# verify-change

## Intent

Compare the network **before** and **after** a change and say, from measured data, whether
the intended outcome holds and whether anything regressed.

## Views

`view` picks the question; an input that belongs to another view is rejected by name. `verify` (the default; `verdict` is the same view) judges a change; `impact` measures how far it reaches; `describe` reads a change set that has not been run.

## Inputs (view verify)

`network_id`, plus either `change_set_id` (a Predict change set; `run_predict: true` lets
the skill run Predict if no processed prediction exists) or both `before_snapshot_id` and
`after_snapshot_id`. Optional `expectations`: flows that must end `delivered` or
`blocked` after the change (`dst_ip`, `expect`, optionally `src_ip`, `from`, `protocol`,
`src_port`, `dst_port`). `connectivity_timeout_seconds` (default 300) bounds the wait for the
connectivity diff to settle. See `schema.json`.

## Procedure

1. Resolve before and after. For a change set, before is its base snapshot and after is its
   newest processed predicted snapshot. If there is none and `run_predict` is not set, stop:
   `unknown` (running Predict costs compute, so it is opt-in). Both snapshots must be processed.
2. Read the subnet-connectivity diff **once it has settled**. A read right after a prediction
   returns all zeros with `is_partial_result` set; those zeros mean "not finished". Zero
   subnet pairs compared means nothing was compared, not that nothing changed.
3. Compare checks before and after. A check with more violations, or one that went from
   PASS to FAIL/ERROR/TIMEOUT, is a **regression**. This needs no statement of intent.
4. Evaluate each expectation with a path search on the **after** snapshot.
   `delivered` needs a delivered path. `blocked` needs a real failure and no delivered path
   from a completed search. No paths, a timeout or an incomplete model is undetermined.
5. Decide:
   - Any regression, or any unmet expectation: **failed**.
   - Expectations given, all met, no regressions: **ok**.
   - Any expectation undetermined (and nothing failed), or no expectations given: **unknown**.
     Without a stated intent the skill reports the impact but does not judge it.

**view impact.** Takes the same selector (`change_set_id`, or before and after snapshots) but no expectations. It counts the differences in every area Forward can count and compares subnet connectivity, then reports the extent. It never judges and never reports a regression: use the default view for a verdict. Areas that were not measured are named in the limits, and nothing is said about them.

**view describe.** Reads a change set without touching it: it never runs a prediction, stages a command or commits (`verify-change` view verify runs and compares, `edit-change-set` builds). Inputs: `network_id`, `change_set_id` (required), and optionally `device` (a firewall whose rule diff to include); it takes none of the view verify inputs.

1. Find the change set in the network. If it is not there the answer is **unknown**, not an error in the network.
2. List its predicted snapshots and take the newest PROCESSED one.
3. Read the change set's checks on the base snapshot and on that prediction; a check that is bad on the prediction and was not bad on the base, or has more violations, got worse.
4. With `device`, read the rule diff and count the entries by kind of change.
5. **failed** when any check got worse on the prediction; **ok** when a prediction was read and no check got worse; **unknown** when the change set has not been predicted (run view verify with `run_predict`) or its prediction checks could not be read. A change set with no checks attached says so in the limits, since an unchanged verdict then proves nothing. Evidence is one `predict` item with `devices_edited`, `predictions`, `checks_that_got_worse` and, with `device`, `security_rules_diff`.

## Evidence

`predict` for the connectivity diff (settled, compared, newly isolated, newly connected),
`policy` for each regressed check, `path` for each evaluated expectation. Each names the
operation and snapshot it came from.

## Output

The envelope in `schema/skill-result.schema.json`. `next_actions`: `investigate-reachability`
for a failed expectation, `verify-change` with `view: impact` when connectivity changed.

## Next actions

`investigate-reachability`, `check-network-compliance`; for a change set that has not been predicted, view verify with `run_predict`.

## Running this skill

Give the inputs as one JSON object on stdin: `echo '{"network_id": "<id>"}' | fwdctl run verify-change`. It needs the
`fwdctl` binary and `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD` in the environment. Read `status`
(`ok`, `failed`, `unknown`, `error`) and the `limits`; `unknown` is never a pass. Inputs are listed above and in `schema.json`.
