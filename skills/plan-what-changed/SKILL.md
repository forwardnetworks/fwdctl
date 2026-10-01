---
name: plan-what-changed
description: Sequences the skills that answer what changed and when. Use when asked what changed or when something started.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "change"
  summary: "what changed and when"
  maturity: "2"
  tools: "inspect-snapshots, inspect-history, compare-device-config, find-nqe-query, compare-nqe-results, verify-change"
---

# plan-what-changed

A procedure, not a skill that runs. Forward holds snapshots, not a timeline: a change is always "between two collections", never at a time inside one.

## Steps

1. **Which snapshots?** `inspect-snapshots`: the newest processed collected snapshots and their times. Pick the two that bracket the question (the newest, and the last one before the symptom). A predicted snapshot is not history.
2. **When did it start?** For a check or a symptom: `inspect-history` with `check_id`. For a device: `inspect-history` with `device`. Both name the interval of snapshots where it changed.
3. **What changed in configuration?** `compare-device-config` between the two snapshots of that interval: the devices whose files differ, then one device's added and removed lines (redacted unless asked otherwise).
4. **What changed in state?** For a kind of data (BGP neighbours, interfaces, VLANs): `find-nqe-query` for a saved query, then `compare-nqe-results` between the two snapshots: rows added, removed, changed.
5. **What did it do?** If a change set or an intended change is in play, `verify-change` on it: expected behaviour against the predicted network, and the areas it touched.

## The minimum

Before you answer, take at least: the two snapshots (step 1) and the configuration difference between them (step 3). Stop earlier only when the evidence already answers the question, and say so.

## Answering

Say which two snapshots and their times, what differs, and what the evidence cannot show: anything between the two collections, and changes Forward does not collect. Do not name a cause from a difference alone; a difference in the same interval is a lead, not a cause. Offer `investigate-reachability` for a path that broke, and never apply a fix.

## Worked example

[reference/example.md](reference/example.md) shows a good run end to end (illustrative, results shortened). Read it when you are unsure what a finished answer looks like; where you cannot read files, run `fwdctl describe plan-what-changed reference/example.md`.
