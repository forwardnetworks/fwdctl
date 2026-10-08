---
name: inspect-jobs
description: Lists Forward's active or completed backend jobs and flags ones running past a threshold as stuck. Use when Forward seems slow, before assuming a worker crashed.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "investigate"
  summary: "see running or finished backend jobs"
  maturity: "1"
  class: read
  secrets: "false"
  tools: "jobs"
---

# inspect-jobs

## Intent

Forward tracks every top-level backend job (an NQE execution, a snapshot-processing run, a device-processing batch, and similar) the same way its own GUI does
(Settings > System Overview > Jobs) and the same way Forward support finds a wedged job. This skill reads that list instead of guessing from symptoms: a container
that looks healthy (CPU, memory, restart count all fine) can still be sitting on a job that has been running for hours.

A job that has run past Forward's own `NQE_JOB_TIMEOUT_MINUTES` (default 20; see `edit-org-property`) has already been given up on by Forward's orchestration
layer even if the worker thread computing it has not stopped -- it is tracked here as `likely_stuck`, and `edit-jobs` is the next action.

## Inputs

- `state`: `active` (default) or `completed`.
- `network_id`: narrow to one network (a string of digits); omit for every network the login can see jobs for.
- `older_than_minutes`: the threshold for `likely_stuck` on active rows (default 20). It only flags; it never filters a row out.
- `match`: a substring of the job type or organization name. `limit`, `offset`: page the rows (default 50, at most 200).

## Where the facts come from

`GET /api/jobs/active` and `GET /api/jobs/completed` (`JobsController`), the same ADMINISTER_SYSTEM-gated admin surface the GUI's System Overview page calls.
An organization administrator already has ADMINISTER_SYSTEM. Rows may be grouped (Forward groups identical jobs); `total_count`/`count` says how many a row stands for.

## Reading a row

Active: `job_type`, `org_name`, `network_id`, `snapshot_id` (absent for a network-level job), `creation_time`, `queued`/`running` (counts, for a grouped row),
`longest_queued_time_seconds`, `longest_running_time_seconds`, `duration_seconds`, `total_count`, `cancel_link` (opaque; pass verbatim to `edit-jobs` or
`Jobs.Cancel`), `likely_stuck`.

Completed: the same shape plus `state` (`SUCCEEDED`, `FAILED`, `CANCELED` or `TIMEDOUT`), `earliest_start_time`, `latest_end_time`. A `TIMEDOUT` row with a long
`longest_running_time_seconds` is evidence the job kept a worker busy well past the point Forward's own timeout fired.

## Limits

`likely_stuck` is a heuristic, not Forward's own judgement -- a legitimately large job (a device with thousands of rules, a big snapshot) can run long without
being wedged. Cross-check against the job's own size before treating it as hung. It flags two different things under one name: a row whose
`longest_running_time_seconds` is past the threshold (actually computing too long), or a row at `running: 0` whose `duration_seconds` is past the threshold
(queued the whole time, never once dispatched to a worker -- Forward's `longest_running_time_seconds` is always 0 for this case, since it only measures time
actually running, so a threshold on it alone would never catch an orphaned queued job).

## Next actions

`edit-jobs` to end anything flagged `likely_stuck`; `inspect-environment` with `include_properties: true` to see or change `NQE_JOB_TIMEOUT_MINUTES`.

## Running this skill

`echo '{}' | fwdctl run inspect-jobs` lists active jobs. `echo '{"state": "completed", "network_id": "4610"}' | fwdctl run inspect-jobs` lists one network's
finished jobs.
