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

`view` picks the question; an input that belongs to another view is rejected by name. `verify` (the default; `verdict` is the same view) judges a change; `impact` measures how far it reaches (with a change set it also checks each staged BGP advertisement against the forwarding entry it should move on its device, base against predicted, and says `recorded_but_unchanged` when a withdraw or inject was recorded and the entry did not move; the difference areas, subnet connectivity, and a routing area: BGP neighbor sessions removed, added or changed state, and IPv4/IPv6 route and next-hop counts per device and VRF, with `device` that device's per-prefix route changes; a route change on a device the change set does not edit is flagged as possible baseline drift, because a predicted snapshot can differ from the collected base with no change staged); `preflight` is the Predict demo checklist in one call: the scope plus the release, whether received BGP advertisements are collected on the BGP devices (modify and withdraw do nothing, silently, without it), whether the base snapshot is a fresh collection and the existing change sets; `scope` answers "what of this network can Predict model on this release" (the license facet, the BGP families in config that Predict does not model, the platforms where propagation stops, every `predict_*` flag the login can read (`flags`, each with its source: read from the tenant, or the default of the release when it cannot be read) and whether OSPF and static are re-propagated from those flags; it reads and judges no change); `list` lists the network's change sets (id, name, base snapshot, `devices_edited` = the devices of the last COMMITTED version, with `draft_devices` for a change set that is staged but has nothing committed yet; none is an answer, an unlicensed organization is a licence limit, not an empty list); `describe` reads a change set that has not been run (each prediction carries `finished`: a predicted snapshot reports UNPROCESSED while it is still being created or computed, so only PROCESSED, FAILED, CANCELED and TIMED_OUT are final; the devices it edits and, per device, what is staged are read from its head commit and its draft (the change-set list never carries them): the number of CLI lines, a whole-config replacement, pending firewall rule changes, and the BGP advertisements injected or modified and the ones withdrawn, so a withdraw can be confirmed as recorded; `draft_devices` counts this login's uncommitted draft); `paths` is the GUI's "view interesting paths": it ranks sampled paths through firewalls and load balancers best score first, top 10 per kind, with each path's processing stages (IN_ACL, XLATE_DNAT, MPLS, ...), hop count and the decoded path query, on `after_snapshot_id` or on a change set's predicted snapshot (`change_set_id`), so the same question can be asked before and after a change. It needs the path-search licence and advanced reachability on the snapshot (otherwise unknown, naming `edit-advanced-reachability`), runs synchronously for at most `connectivity_timeout_seconds` per kind (default 45, at most 300), and samples: a path not listed is not proven absent.

## Inputs

What each input means, per view: `fwdctl run verify-change --help`, or `reference/inputs.md`.

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

**view impact.** Takes the same selector (`change_set_id`, or before and after snapshots) but no expectations. It counts the differences in every area Forward can count and compares subnet connectivity, then reports the extent. It never judges and never reports a regression: use the default view for a verdict. Areas that were not measured are named in the limits, and nothing is said about them. When the ACL area changed it also names the devices whose ACLs changed (`acl_devices`, the most changed first, with rules added, deleted and modified); with `device` it lists that device's changed ACL rules (`acl_changes`: added, deleted or modified, with action, name, where it applies, match conditions and the configuration lines, and for a modified rule what it was). A rule diff is evidence of what changed, not a verdict on whether it is safe.

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

## Reference

Read the file you need, when you need it (where you cannot read files: `fwdctl describe verify-change reference/<file>`).

- [reference/predict-limits.md](reference/predict-limits.md): what Predict does and does not model (unverified behaviours, labelled). Read before calling a change safe.
