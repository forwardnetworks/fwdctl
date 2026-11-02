---
name: inspect-performance
description: Reads device and interface health from performance data: unhealthy devices, highest CPU, memory, utilization, loss or errors, and trends. Use when asked what is busiest or dropping packets.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "inventory-topology"
  summary: "CPU, memory, loss, errors"
  maturity: "3"
  tools: "performance"
---

# inspect-performance

## Intent

Answer "is it healthy" from measured performance data, and never from silence. Forward returns an empty list both when
everything is fine and when performance was never collected; this skill tells them apart.

## Inputs

What each input means, and the fields per object and action: `fwdctl run inspect-performance --help`, or `reference/inputs.md`.

Read before relying on a result (printed by `fwdctl run inspect-performance --help`): When there are no samples.

**An empty answer is unknown, never health: with no raw samples say so, and the limits say what stops polling.**

## Procedure

1. Ask Forward for the view.
2. Decide:
   - Readings: **ok**. Unhealthy devices or interfaces found: **failed**, with each one and Forward's reason.
   - **No samples: unknown.** A network with performance collection off, a wrong device name and a permissions gap all return
     nothing, so the skill never says "healthy" or "no problems" from an empty answer.
   - An "unhealthy" list that is empty is trusted only if raw CPU samples exist for the network; otherwise it is unknown.
3. Metric names are Forward's exact strings; a wrong one is an `error` that names the valid ones.

## Evidence

One `state` item: the view's readings, or the summary of the series.

## Output

The envelope in `schema/skill-result.schema.json`.

## Next actions

`inspect-inventory` for the devices behind a reading, `investigate-reachability` when a link looks saturated.

## Running this skill

Run `fwdctl run inspect-performance --help` for the inputs and an example, then give them as one JSON object on stdin: `fwdctl run inspect-performance < input.json`. Read `status` (`ok`, `failed`, `unknown`, `error`) and the `limits`; `unknown` is never a pass.

