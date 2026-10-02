---
name: investigate-collection-failure
description: Finds why Forward could not collect or model devices, grouping failures by credentials, network path, device session and processing. Use when a snapshot is incomplete or devices are missing.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "snapshots"
  summary: "why devices were not collected"
  maturity: "3"
  tools: "snapshots, collect"
---

# investigate-collection-failure

## Intent

Say whether the newest collection is healthy and, if it is not, **which kind of failure**
it is (credentials, network path, device session, processing) and how many devices each
affects. The answer is read from Forward's own snapshot metrics and task records.

## Inputs

`network_id`. Optional `snapshot_id` (default: the newest snapshot in **any** state, since a
failed collection is the point) and `view`: `summary` (default) counts failures by cause and takes
`collector_task_id`; `triage` is first contact with a network — one call merging `summary`, the slowest devices and the worst-failing platform group, takes no inputs of its own; `devices` names the failed devices and takes `failure` (a category such as
`credentials`, `network_path`, `device_session`, `unclassified`, `processing`, or a type such as
`CONNECTION_REFUSED`), `device` (name contains) and `limit`/`offset`; `exceptions` lists the collectors' logged exceptions and takes `device`, `limit`/`offset`; `neighbors` lists unmodelled
neighbours and takes `limit`/`offset`; `slow` ranks devices by collection time and takes `device`, `limit`, `offset`, `compare_to_snapshot_id`; `history` reads how long each recent collection took and takes `limit` (snapshots, default 10, at most 30); `logs` reads one
device's collection log and takes `device` (required), `failure` (here the minimum level TRACE, DEBUG, INFO, WARN (default) or ERROR),
`limit`, `offset`. See `schema.json`.

**view triage.** First contact with a network's collection health in one call: runs `summary`, the 5 slowest devices (`slow`) and the
worst-failing OS-version group (`platforms`), and merges them into one finding ("...; slowest: DEVICE; worst platform: VENDOR OS VER (N failed of M)")
with the detail of each behind separate evidence items. It does not read the organization's license capacity (no API route exposes it) or
per-device check compliance; for the full detail behind any one line, read that view on its own. No snapshot, or one still processing, is the
whole answer and nothing else is added.

**view devices.** Reads each device's recorded result from the snapshot's device model (NQE
`device.snapshotInfo.result`) and returns one row per failed device: stage, category, error type,
vendor, OS, model, collection IP. For processing failures it adds Forward's exception for them: the
device names and the first line of the stack trace (the parser's class and message). That needs the
DEBUG_SNAPSHOTS permission and is reported as not read otherwise; when Forward's exception list holds no record for the failures (measured: a network with 12 parser failures returned none) the limits say so and the message is not available. Forward's API gives no line or
file region for a parser exception, so go on to `inspect-device-files` for the device's raw config.
The snapshot's metrics count every failed device, but the model lists only devices Forward built a
record for; when the two differ, the limits say so.

**view platforms** (takes `failure`, `device`, `limit`, `offset`). Failures rolled up by vendor, OS and OS version against **all** devices of that platform in the snapshot (failed, total, failure rate, error types),
worst first. A rate near 100% on one OS version while the rest of the vendor is clean points at that version's parser or support, not at credentials or the network. Devices Forward could not identify are
grouped as one platform of their own.

**view changes** (takes `limit`, `offset`; `snapshot_id` picks the newer snapshot). The same device read on the newest processed snapshot before it: new failures, recovered devices, devices still failing,
failures whose type changed, and how many new failures are on a device whose OS version changed between the two snapshots (a lead for a parser or support gap, not proof).

**view slow.** Forward's per-device collection metrics: collection duration, the slowest command and its duration, source and device type,
jump server, and the merged collection-and-processing error (so every error class, not only failures), slowest first, with the median, p95
and max. Forward keeps one slowest command per device, not every command, and saves nothing for an imported, forked or partially collected
snapshot (then **unknown**). The stats also give `sum_ms` (total device time), `collection_wall_ms` and `implied_parallelism` (total device time over the collection's wall time: the average number of devices being collected at once, computed for the whole run, not for a `device` filter), the
collector's configured concurrency beside it (`stats.collector`; 128 when unset, said so) with the share of it in use, `by_device_type` and `by_connection` (devices, total and median time, errors; the eight that took the most total time) and `errors_by_type`. The finding carries the headline numbers because a
table or CSV rendering prints the finding, not the stats. Read the parallelism against the concurrency: well under it means the collector's slots are not the limit (a long tail of slow devices, a per-second rate limit or the devices themselves are);
near it means the collector is. Checked on a 38,709-device collection: 173 devices at once against a configured 1,024, firewalls (2.7% of devices) using 17% of the total device time (98 of 577 hours), with a median 201 s against 33 s for switches. `errors_by_type` counts every error class, including devices Forward
tags but did not collect (LICENSE_EXHAUSTED was seen on tens of thousands), while the snapshot's collection-failure count (view summary) was seen to leave those out: the two totals need not agree. `stats.in_flight` is the run over time, from each device's own start time and duration: `devices_in_flight` per bucket (5 minutes, longer for a long run), the peak and when the profile first reached 90% of it, `finished_by_seconds` (when 50%, 90%, 99% and all devices had finished) and `idle_gaps`: stretches of at least three buckets with fewer than one device running, with how many devices started after the longest gap and which connection and type most of them were, and the finding says so (a run can go quiet in the middle and then start a second batch, which the average hides; seen: 31,378 devices started in the first 100 minutes, none in the next 70, then 7,322 mostly Cisco IOS-XE routers). A device's recorded start can be when it was handed to the collector rather than when it got a slot, so the count can exceed the configured concurrency (1,788 in the first five minutes against 1,024 was seen); read it as "started and not finished". `compare_to_snapshot_id` sets a second collection beside this one: `stats.compare` has both sides and the change in total device time, median, p95 and max, devices, implied parallelism, and per device type and connection (the ten groups that changed most, by total time), and the finding carries the summary (seen: the same 575 to 577 hours of device time on two days, but 321 devices at once against 173, and medians of 31 and 33 s, so the per-device cost did not change and the concurrency did). A baseline with no metrics (a reprocess, an import) is said, not guessed. The finding names the collector's concurrency and says when it is not the default. There is no per-device command count or sum, so a device whose
collection time far exceeds its slowest command (a PAN-OS firewall at 1,500 to 2,100 s with a slowest command of 20 to 80 s was seen) has time Forward does not attribute; the log (view logs) is the only other evidence.
`stats.no_recorded_duration` lists devices with no recorded collection (count, by type, up to 50 named, each with its `start_offset_seconds`): cancelled, timed out or never collected, which only the device's log (view logs, `failure` INFO) tells apart. `stats.org_collection_settings` holds the organization's per-device collection timeout (Forward's default 180 minutes), retries and `devices_that_ran_to_the_timeout`; a gap that ends at the timeout after the run began is said in the finding. `stats.queue` is Forward's own series of devices queued and running over the run, bucketed, with the maximum queued, running and slots held during the longest idle gap against the concurrency limit (nothing queued and nothing running is an idle collector; running far below the limit with devices queued means a jump-server or vCenter cap or a dispatch delay held work back), and `no_recorded_duration.end_states` counts the collector task's subtasks by Forward's status and lists the devices that TIMED_OUT, were CANCELED or FAILED with how long each ran (a timed-out device's run time should match the per-device timeout). `started_after_the_longest_gap` names the first ten devices that started after the gap. Forward's PAN-OS collector tries `request system external-list show` for each external list, and on an authorization failure downloads each list's URL from the collector instead, repeatedly; a PAN-OS device whose collection time far exceeds its slowest command is often this (log lines "Unable to get external list ... Trying to download from the list's URL directly"). Forward has no setting to skip it or bound the download; the fix is on the device (permission for that command, or no external lists).

**view history** (takes `limit`: snapshots, default 10, at most 30). How long each recent collection took, from the snapshots themselves, so it reaches further back than the few tasks `inspect-collection` lists: per collected snapshot its `collection_seconds` and
`processing_seconds` (Forward's own figures), `devices` and `devices_change` against the next older collected snapshot (a jump is a change in what is collected, not a slower collector), the devices collected and failed, and, where the snapshot records its
collector task, `task_seconds` (the task's start to finish) and `collection_end_to_processed_seconds` (the collection ending to the snapshot being usable). It gives the median and flags any collection more than 1.5 times it (`slower_than_usual`, only when at least four have a duration), and the
finding says how the latest compares. The median mixes collections of different sizes, so read it beside `devices`. Only Forward-collected snapshots count: a reprocess, an import or a prediction is not a collection. A collection whose snapshot was replaced by a reprocess (the task survives) is listed under `collections_without_a_shown_snapshot` with the task's start, end and what its snapshot became, from Forward's recent collector tasks, so a collection older than that window can be missing (seen: 10-01's 1h46m, 09-30's 4h07m and 09-29's 3h01m beside 10-02's 3h20m). It does not wait for a newer snapshot that is still processing.
`longest_idle_seconds` (with where the gap is, and `had_idle_gap`) is measured for the newest 5 collections only, since the per-device record is large. Checked on a network whose collection went from 52 minutes to 1h47m and then 3h20m as its device count went from about 4,500 to 40,000.

**view logs.** A window of one device's collection log at or above a level (`failure`, default WARN), read up to 512 KiB. `device` must be
the name the device was collected under. An empty log is **unknown**, not clean; an import has no log. The text can quote commands and
device output: keep it out of issues and public places. The whole log is scanned (up to 64 MiB) and `log` gives its line count, first line, last line and time, the last 20 lines, and the first lines that mention cancel or timeout, so a device that finished, was cancelled or timed out can be told apart; give `failure` INFO, since at WARN the last line is only the last warning.

**Parser failures.** Forward marks a supported device PARSER_EXCEPTION whenever processing fails, even when it stored no exception, so an
empty exception list is expected and no message, class or line exists to read: the device list, its category and `collectionError`
(sometimes the root cause) and the raw files are what there is.

**view exceptions** (takes `device`, `limit`, `offset`). The exceptions the collectors logged while collecting the snapshot, deduplicated by Forward: the first line of each stack trace, how many times, and which devices or cloud accounts.
It is where an error a collector **ignored** shows up: a collection can finish, and a cloud account read as collected, with an exception in the log (a quota or permission API call that failed, say). Needs the permission to view
collector exceptions (a network administrator); without it the answer is **unknown** with what is needed. The text can quote what the collector was doing: keep it out of public places.

**view neighbors.** Lists the neighbours Forward sees but does not model, with discovery method,
This is ONE REST call with no paging; on a very large network (thousands of devices with many neighbours) it can run past the HTTP timeout (measured: timed out at 2 minutes on an
~11,000-device network). The error then says so and names `FORWARD_TIMEOUT` (for example `FORWARD_TIMEOUT=10m`) to raise it. `view summary` reads the same call but treats a slow or
failed read as a limit, not a failure to answer: it still reports what it could.
addresses and which devices see them, and marks each that a modelled device peers with over BGP
(`bgp_peer`, `bgp_sessions` with peer AS and state), BGP peers first: an unmodelled BGP peer is
usually the upstream or edge. `plan-synthetic-device` covers modelling one.

## Procedure

1. Resolve the snapshot. None at all: `unknown`.
2. If the snapshot is still processing, stop: `unknown`. Failure counts are incomplete until
   processing finishes, and an in-progress snapshot must not read as healthy or as failed.
3. Read the collector task if one was given. FAILED, TIMED_OUT or CANCELED is a failure;
   QUEUED or RUNNING means the collection has not finished (`unknown`).
4. Read the snapshot metrics. Group `deviceCollectionFailures` by category:

   | Category | Failure types |
   |---|---|
   | credentials | AUTHENTICATION_FAILED, AUTHORIZATION_FAILED, CONFIG_COLLECTION_UNAUTHORIZED, PRIV_PASSWORD_ERROR, KEY_EXCHANGE_FAILED, jump/proxy auth failures |
   | network_path | CONNECTION_TIMEOUT, CONNECTION_REFUSED, NETWORK_UNREACHABLE, jump/proxy connection failures |
   | device_session | IO_ERROR, SESSION_CLOSED, STATE_COLLECTION_FAILED, NO_SPACE_LEFT_ON_COLLECTED_DEVICE |
   | unclassified | anything else, including UNKNOWN |

   `deviceProcessingFailures` are parsing or modeling problems, not access problems.
5. List neighbours that are seen but not modeled (`missing devices`) and, when permitted,
   the processing exceptions. Exceptions need a permission the caller may lack; then they
   are reported as **not read**, never as "none".
6. Decide:
   - Any collection or processing failure, a failed snapshot or a failed task: **failed**.
   - Processed, devices collected, no failures: **ok**.
   - Nothing collected and no failure recorded, or still in progress: **unknown**. Zero
     devices with zero failures means nothing was measured.

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
