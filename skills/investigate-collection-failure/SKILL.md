---
name: investigate-collection-failure
description: Finds why Forward could not collect or model devices, grouping failures by credentials, network path, device session and processing. Use when a snapshot is incomplete or devices are missing.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "snapshots"
  summary: "why not collected"
  maturity: "3"
  tools: "snapshots, collect"
---

# investigate-collection-failure

## Intent

Say whether the newest collection is healthy and, if it is not, **which kind of failure**
it is (credentials, network path, device session, processing) and how many devices each
affects. The answer is read from Forward's own snapshot metrics and task records.

## Inputs

What each input means, and the fields per object and action: `fwdctl run investigate-collection-failure --help`, or `reference/inputs.md`, `reference/timing.md`, `reference/views.md`.

## Evidence

`collection` items: the failure categories with counts and types, the processing failures,
the task status, and the missing neighbours. Each names the operation and snapshot.

## Output

The envelope in `schema/skill-result.schema.json`.

## Next actions

`check-network-compliance` once collection is healthy.

## Running this skill

Give the inputs as one JSON object on stdin: `echo '{"network_id": "<id>"}' | fwdctl run investigate-collection-failure`. It needs the
`fwdctl` binary and `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD` in the environment. Read `status`
(`ok`, `failed`, `unknown`, `error`) and the `limits`; `unknown` is never a pass. Inputs are listed above and in `schema.json`.
