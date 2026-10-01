---
name: inspect-history
description: Shows how one check's status moved across recent snapshots and where it last changed, or when a device's config last changed. Use when asked when a check started failing or a config changed.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "compliance"
  summary: "when a check or config changed"
  maturity: "3"
  tools: "snapshots, checks"
---

# inspect-history

## Intent

The "when did this start" question. Forward holds snapshots, not a timeline, so history is built by reading one check on each of the last N snapshots. It is bounded: N snapshots (default 8, at most 20), one read each, in order, newest first.

## Inputs

`network_id` and exactly one of `check_id` (find it with `check-network-compliance` view read) or `device` (a device name). Optional: `snapshots` (how many to read). See `schema.json`.

## Procedure

1. List the snapshots; keep processed, collected ones (not predictions, not drafts) and take the newest N.
2. Read the check on each, newest first. Stop at the first snapshot where its status differs from the newest, or where it does not exist.
3. A snapshot that predates the check has no row for it: report **ABSENT**, which is not a pass. A result computed against an older definition is marked outdated and is not comparable.
4. **ok** with the series and the change point; **unknown** when there are no snapshots or the check is not on the newest one.

If the status never differs, the finding says the change, if any, is older than the snapshots read.

## Device configuration (`device`)

Instead of a check, give a `device`: the skill walks adjacent pairs of processed snapshots, newest first (at most N snapshots, one diff read per
pair), and stops at the first pair whose collected configuration files differ for that device. **ok** with the two snapshots and their times;
if no pair differs the change is older than the snapshots read. A change is attributed to the interval between two collections, never to a time
inside it. Fewer than two processed snapshots is **unknown**.

## Evidence

For a check: one `policy` item: `series` (snapshot, time, status, violations), `newest_status`, and `status_last_differed_at` or `not_present_at`. For a device: one `config` item with `device`, `pairs_compared` and `changed_between`.

## Output

The envelope in `schema/skill-result.schema.json`.

## Next actions

`compare-device-config` and `verify-change` between the snapshot where it differed and the next, to see what changed.

## Running this skill

Give the inputs as one JSON object on stdin: `echo '{"network_id": "<id>", "check_id": "<id>"}' | fwdctl run inspect-history`. It needs the `fwdctl` binary and `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD` in the environment. Read `status` (`ok`, `failed`, `unknown`, `error`) and the `limits`; `unknown` is never a pass. Inputs are listed above and in `schema.json`.
