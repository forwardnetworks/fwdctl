---
name: edit-jobs
description: Cancels active backend jobs running past a threshold instead of restarting the worker running them. Dry run unless apply is true. Use when a job looks stuck.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "investigate"
  summary: "cancel jobs stuck past a threshold"
  maturity: "1"
  class: write
  effect: "org"
  secrets: "false"
  reversible: "false"
  tools: "jobs"
---

# edit-jobs

## Intent

A job that has run past Forward's own `NQE_JOB_TIMEOUT_MINUTES` (default 20) is doomed by Forward's own definition: its orchestration layer has already
given up on it even if the worker thread computing it has not stopped, and nothing upstream will clear it on its own. The alternative to this skill is
restarting the worker pod running the job, which kills every OTHER job that worker happens to be running too. This skill finds the specific job and
cancels it (`DELETE /api/jobs/{cancelLink}`, the same action Forward's own GUI and Forward support use), leaving the rest of the worker's work alone.

It is meant to run unattended on a schedule as the first response to a wedged job, before anyone reaches for a pod restart.

## Inputs

What each input means, and the fields per object and action: `fwdctl run edit-jobs --help`, or `reference/inputs.md`.

## Dry run

Lists every job past the threshold as a planned `cancel_job` change (job type, network, snapshot if any, how long it has run, its `cancel_link`).
Nothing is sent to Forward. Always run the dry run first when driving this by hand; a scheduled/unattended caller may go straight to `apply: true` --
that is exactly the case this skill exists for.

## Apply

Cancels each matched job (`Jobs.Cancel` in the SDK terms). **Not reversible**: canceling ends the job; nothing restores it, and rerunning the same NQE
query or operation is a new job, not an undo. A job already finished or already canceled by the time this runs is reported as a failed cancellation for
that one row, not a crash of the whole run -- `status` is `failed` only if at least one matched job could not be canceled; jobs that did cancel are still
reported as canceled.

## Refusals

`network_id` that is not a number; `older_than_minutes` negative.

## Procedure

1. `GET /api/jobs/active`. Filter to `network_id` (if given) and `longest_running_time_seconds >= older_than_minutes*60`.
2. Nothing matched: **ok**, "nothing is stuck".
3. More than `limit` matched: cancel (or plan) only the first `limit`, and say so in the omission.
4. Dry run: return the plan. `apply: true`: `DELETE /api/jobs/{cancelLink}` for each, per-job success or failure.

## Evidence

One `state` item: the threshold, how many matched, and the canceled/failed lists with what each row was.

## Next actions

`inspect-jobs` to confirm nothing past the threshold remains, or to see what is still running if some cancellations failed.

## Running this skill

`echo '{}' | fwdctl run edit-jobs` is the dry run (20-minute threshold, every network). `echo '{"apply": true}' | fwdctl run edit-jobs` cancels them.
`echo '{"network_id": "4610", "older_than_minutes": 20, "apply": true}' | fwdctl run edit-jobs` scopes it to one network, for a scheduled check.
