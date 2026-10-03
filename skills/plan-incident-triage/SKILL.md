---
name: plan-incident-triage
description: Sequences the skills that check network health or triage an outage of unknown cause. Use for a health check or what is wrong now.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "investigate"
  summary: "health check, outage triage"
  maturity: "2"
  tools: "inspect-snapshots, inspect-collection, inspect-performance, check-network-compliance, inspect-history, investigate-reachability, plan-what-changed, plan-troubleshoot-connectivity"
---

# plan-incident-triage

A procedure, not a skill that runs. Forward is a model of the last collection, not a live monitor: say how old the picture is before you reason from it.

## Steps

1. **How old is the picture?** `inspect-snapshots` and `inspect-collection` (view status): newest processed snapshot, last collection, collector connected. If the picture predates the incident, ask for a fresh collection (`plan-snapshot-recovery`) before diagnosing.
2. **What changed?** `plan-what-changed` for the interval around the start of the incident: configuration and state differences are the strongest leads.
3. **Anything stressed?** `inspect-performance` unhealthy views (CPU, memory, interface errors, utilisation), only where samples exist.
4. **Policy regression?** `check-network-compliance` with `view: read`, failing checks first; `inspect-history` for when each started.
5. **Which flow?** For the symptom the person reports, `plan-troubleshoot-connectivity`.

## The minimum

Before you answer, take at least: the age of the picture (step 1), what changed (step 2), and one health or policy signal (step 3 or 4). Do not stop at "the data is old" or "something changed": say whether devices are stressed and whether policy regressed, or say that you could not measure it. Stop earlier only when the evidence already answers the question, and say so.

## Answering

A short ranked list of leads, each with its evidence and snapshot, and an explicit list of what could not be seen (live state, events between collections, devices not collected). Recommend the next check; do not claim a root cause from correlation, and never change a device.

## Health check (no incident reported)

Healthy is a claim about measured things; absence of an alarm is not health. Use steps 1, 3 and 4 above, plus collection from step 1 (`inspect-collection` view status; for missing devices `investigate-collection-failure` view `triage` first), and:

- Take at least: data freshness, collection, and either performance or policy. Stop earlier only when the evidence already answers the question, and say so.
- Trust an empty performance list only when the result says samples exist; otherwise it is unknown.
- For each failing check that matters, `inspect-history` shows when it last changed (`plan-what-changed` for the configuration behind it).
- Report each area as ok, failed or unknown with its snapshot and time. Never summarise a set of unknowns as healthy: name what was not measured, and offer the next skill for each failed area instead of fixing it.

## Worked example

[reference/example.md](reference/example.md) shows a good run end to end (illustrative, results shortened). Read it when you are unsure what a finished answer looks like; where you cannot read files, run `fwdctl describe plan-incident-triage reference/example.md`.
