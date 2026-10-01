---
name: plan-health-check
description: Sequences the skills that answer whether the network is healthy now. Use when asked for a health check, status or morning check.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "investigate"
  summary: "is the network healthy"
  maturity: "3"
  tools: "inspect-snapshots, inspect-collection, inspect-performance, check-network-compliance, inspect-history, investigate-collection-failure"
---

# plan-health-check

A procedure, not a skill that runs. Healthy is a claim about measured things; absence of an alarm is not health.

## Steps

1. **Is the data current?** `inspect-snapshots`: the newest processed collected snapshot and its age. Old data makes every later answer old.
2. **Is collection working?** `inspect-collection` (view status): running, last task, collector connected, devices failing. For missing devices, `investigate-collection-failure`.
3. **Are devices stressed?** `inspect-performance` unhealthy views. An empty list is trusted only if the skill says samples exist; otherwise it is unknown.
4. **Does policy hold?** `check-network-compliance` with `view: read`: failing checks first.
5. **Is a failure new?** For each failing check that matters, `inspect-history` to see when it last changed (`plan-what-changed` for the configuration behind it).

## The minimum

Before you answer, take at least: data freshness (step 1), collection (step 2) and either performance or policy (step 3 or 4). Stop earlier only when the evidence already answers the question, and say so.

## Answering

Report each area as ok, failed or unknown with its snapshot and time. Do not summarise a set of unknowns as healthy: name what was not measured. Offer the next skill for each failed area instead of fixing anything.
