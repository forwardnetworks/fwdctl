# Collection timing: fields, readings and known causes

Reference for `view slow`, `view history` and `view logs` of `investigate-collection-failure`. Checked on a 40,000-device collection; the numbers quoted are from there.

## Contents
- view slow: the numbers
- Idle gaps, the queue and the per-device timeout
- Batches: why devices start late
- Devices with no recorded duration
- view history
- view logs
- Known cause: PAN-OS external lists

## view slow: the numbers
Forward's per-device collection metrics: collection duration, the slowest command and its duration, source and device type, jump server, and the merged collection-and-processing error (every error class, not only failures), slowest first, with the median, p95 and max. Forward keeps one slowest command per device, not every command, and saves nothing for an imported, forked or partially collected snapshot (then **unknown**).

`stats` gives `sum_ms` (total device time), `collection_wall_ms` and `implied_parallelism` (total device time over the wall time: the average number of devices collected at once; computed for the whole run, not for a `device` filter), the collector's configured concurrency beside it (`stats.collector`; 128 when unset, said so) with the share in use, `by_device_type` and `by_connection` (devices, total and median time, errors; the eight that took the most total time) and `errors_by_type`. The finding carries the headline numbers because a table or CSV rendering prints the finding, not the stats.

Read the parallelism against the concurrency: well under it means the collector's slots are not the limit (a long tail of slow devices, a per-second rate limit or the devices themselves are); near it means the collector is. Seen: 173 devices at once against a configured 1,024, firewalls (2.7% of devices) using 17% of the total device time (98 of 577 hours), median 201 s against 33 s for switches.

`errors_by_type` counts every error class, including devices Forward tags but did not collect (LICENSE_EXHAUSTED was seen on tens of thousands), while the snapshot's collection-failure count (view summary) was seen to leave those out: the two totals need not agree.

`compare_to_snapshot_id` sets a second collection beside this one: `stats.compare` has both sides and the change in total device time, median, p95 and max, devices, implied parallelism, and per device type and connection (the ten groups that changed most), and the finding carries the summary (seen: 575 and 577 hours of device time on two days, but 321 devices at once against 173, medians 31 and 33 s, so the per-device cost did not change and the concurrency did). A baseline with no metrics (a reprocess, an import) is said, not guessed.

There is no per-device command count or sum, so a device whose collection time far exceeds its slowest command (a PAN-OS firewall at 1,500 to 2,100 s with a slowest command of 20 to 80 s) has time Forward does not attribute; the log (view logs) is the only other evidence.

## Idle gaps, the queue and the per-device timeout

`stats.in_flight` is the run over time, from each device's own start time and duration: `in_flight_buckets` (`devices_in_flight` per bucket of 5 minutes, longer for a long run), the peak and when the profile first reached 90% of it, `finished_by_seconds` (when 50%, 90%, 99% and all devices had finished) and `idle_gaps`: stretches of at least three buckets with fewer than one device running, with how many devices started after the longest gap and which connection and type most were (`started_after_the_longest_gap`, with the first ten names). A run can go quiet in the middle and then start a second batch, which the average hides; seen: 31,378 devices started in the first 100 minutes, none in the next 70, then 7,322 mostly Cisco IOS-XE routers. A device's recorded start can be when it was handed to the collector rather than when it got a slot, so the count can exceed the configured concurrency (1,788 in the first five minutes against 1,024); read it as "started and not finished".

`stats.queue` is Forward's own series of devices queued and running over the run (`queue_buckets`), with the maximum queued, running and slots held during the longest idle gap against the concurrency limit. Nothing queued and nothing running is an idle collector; running far below the limit with devices queued means a jump-server or vCenter cap or a dispatch delay held work back. Queued subtasks are never listed by name, and what a queued task waits for is not recorded.

`stats.org_collection_settings` holds the organization's per-device collection timeout (Forward's default 180 minutes), retries, and `devices_that_ran_to_the_timeout`. A gap that ends at the timeout after the run began is said in the finding. `stats.running_in_the_gap` names the collector subtasks in progress in the middle of the longest gap (Forward's own description, status, operation and start). `stats.subtasks_not_succeeded` (read whenever there is an idle gap or a device with no recorded duration) counts the collector task's subtasks by Forward's status and lists the ones that TIMED_OUT, were CANCELED or FAILED with how long each ran (a timed-out subtask's run time should match the per-device timeout; capped at 3,000 subtasks).

A subtask can TIME_OUT although its device's own collection finished within a minute (seen: both subtasks running mid-gap, the devices' logs finished at once, the subtasks timed out 3 h later and held an SD-WAN batch back). The finding names such subtasks. That is a collector issue to raise with Forward, not a slow device.

## Batches: why devices start late

Forward collects interdependent devices as ONE batch subtask: an ACI fabric, F5 or ASA families, a Viptela vSmart with its edges, a controller with the devices it manages. Batches are enqueued longest-estimated first, and a batch starts when all its resources (concurrency slots, one per device, jump servers, vCenter) are free at once. The devices in a batch show start times inside that subtask, so devices that "start late" may be the tail of a batch task that began earlier, or a batch that waited for a resource. This is read from Forward's scheduler source; it is not something Forward records per run.

## Devices with no recorded duration

`stats.no_recorded_duration` lists devices that ended without a recorded collection duration: count, by type, up to 50 named, each with its `start_offset_seconds`. They were cancelled, finished early or never collected; most are not hung. Only the device's log (view logs, `failure` INFO) or `subtasks_not_succeeded` tells which.

## view history

How long each recent collection took, from the snapshots themselves, so it reaches further back than the few tasks `inspect-collection` lists: per collected snapshot its `collection_seconds` and `processing_seconds` (Forward's own figures), `devices` and `devices_change` against the next older collected snapshot (a jump is a change in what is collected, not a slower collector), the devices collected and failed, and, where the snapshot records its collector task, `task_seconds` (the task's start to finish) and `collection_end_to_processed_seconds`. It gives the median and flags any collection more than 1.5 times it (`slower_than_usual`, only when at least four have a duration), and the finding says how the latest compares. The median mixes collections of different sizes, so read it beside `devices`.

Only Forward-collected snapshots count: a reprocess, an import or a prediction is not a collection. A collection whose snapshot was replaced by a reprocess (the task survives) is listed under `collections_without_a_shown_snapshot` with the task's start, end and what its snapshot became, from Forward's recent collector tasks, so a collection older than that window can be missing. It does not wait for a newer snapshot that is still processing.

`longest_idle_seconds` (with where the gap is), `had_idle_gap` and `subtasks_timed_out` are measured for the newest 5 collections only, since the per-device record is large. Each of the 5 rows carries both fields; a value of null means that measurement failed (the row's `notes` say why: metrics missing, no collector task, a timeout reading a large record), never zero. The metrics of a very large collection can take longer than the default 120 s request limit: raise it with `FORWARD_TIMEOUT` (for example `FORWARD_TIMEOUT=10m`) when the notes say the metrics could not be read. `stats.collections_with_an_idle_gap` and `collections_with_timed_out_subtasks` count only measured rows and are null when none was measured; the headline says when the latest collection went idle or had subtasks that timed out, or that it could not be measured. Seen: a collection that went from 52 minutes to 1h47m and then 3h20m as its device count went from about 4,500 to 40,000.

## view logs

A window of one device's collection log at or above a level (`failure`, default WARN), read up to 512 KiB; `device` must be the name the device was collected under. The whole log is scanned (up to 64 MiB) and `log` gives its line count, first line, last line and time, the last 20 lines, and the first lines that mention cancel or timeout, so a device that finished, was cancelled or timed out can be told apart. Give `failure` INFO: at WARN the last line is only the last warning.

## Known cause: PAN-OS external lists

Forward's PAN-OS collector tries `request system external-list show` for each external list, and on an authorization failure downloads each list's URL from the collector instead, repeatedly. A PAN-OS device whose collection time far exceeds its slowest command is often this (log lines "Unable to get external list ... Trying to download from the list's URL directly"; seen one list every 2.5 to 4 minutes, about 16% of the total device time). Forward has no setting to skip it or bound the download; the fix is on the device (permission for that command, or no external lists).

