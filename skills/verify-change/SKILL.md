---
name: verify-change
description: Judges a change and Predict fit: verify, describe, impact, list, scope, preflight, paths. Use for: is a change safe, what a change set does, Predict fit, interesting paths.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "change"
  summary: "verify, scope, preflight a change"
  maturity: "3"
  tools: "snapshots, changes, paths"
---

# verify-change

## Intent

Compare the network **before** and **after** a change and say, from measured data, whether
the intended outcome holds and whether anything regressed.

## Views

`view` picks the question; an input that belongs to another view is rejected by name. One line each (the full text of every view and the verify procedure: `reference/views.md`):

- `verify` (default; `verdict` is the same): judge a change against checks and stated expectations: ok, failed or unknown.
- `describe`: read a change set without touching it: its devices and what is staged on each (incl. BGP advertisements), predictions with `finished`, checks that got worse.
- `impact`: measure how far a change reaches (differences, connectivity, routing; with a change set, each staged advertisement against its forwarding entry, `recorded_but_unchanged`). Never judges.
- `list`: the network's change sets with their predictions. `scope` and `preflight`: whether Predict fits (license, flags, platforms, features). `paths`: interesting paths on a snapshot.
- `ai_summary`: Forward's own AI explains a difference in words (`summary_kind` config, impact, failure or overview). Opt-in, labelled generated text, works on any two snapshots; unknown when the organization has AI off. Use it beside `impact`, never instead of it.

- `history`: a change set's saved versions (note, who, when), its summary and its directory. `config`: one device's configuration in the draft or in a saved version (`commit_id`). `affected`: the devices a prediction changed and whether modelling finished (`after_snapshot_id`). `bgp_diffs`: what a device's staged BGP advertisements change. `objects`: a firewall's objects or zones in a change set (`object_kind`). `checks`: the NQE checks on a change set. `commands`: the OSes Predict models, or with `os` (and `protocol`) the commands it accepts.

Read the verify procedure before judging a change: `unknown` is never a pass, and a settled connectivity read is required.

## Inputs

What each input means, per view: `fwdctl run verify-change --help`, or `reference/inputs.md`.

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

## Reference

Read the file you need, when you need it (where you cannot read files: `fwdctl describe verify-change reference/<file>`).

- [reference/views.md](reference/views.md): the full text of each view and the verify procedure.
- [reference/predict-limits.md](reference/predict-limits.md): what Predict does and does not model (unverified behaviours, labelled). Read before calling a change safe.
