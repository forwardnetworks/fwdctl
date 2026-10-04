---
name: investigate-collection-failure
description: Finds why Forward could not collect or model devices, grouping failures by credentials, network path, device session and processing. Use when a snapshot is incomplete or devices are missing.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
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

**view slow.** Each device's collection time and slowest command, slowest first, with total device time, the run's wall time, devices collected at once beside the collector's configured concurrency, the run over time (`stats.in_flight`, with `idle_gaps` and what started after the longest), Forward's queued and running series (`stats.queue`), the collector subtasks running in the gap (`stats.running_in_the_gap`), devices with no recorded duration, the per-device timeout, and `compare_to_snapshot_id` to set two collections side by side. The finding carries the headline numbers. Read the parallelism against the concurrency: well under it means the slots are not the limit; near it means they are. A gap that ends at the per-device timeout, or subtasks that TIMED_OUT, point at the collector, not slow devices. Field meanings, what each can and cannot prove, and the known causes: [reference/timing.md](reference/timing.md).

**view history** (takes `limit`, default 10, at most 30). How long each recent collection took, from the snapshots themselves, with devices and `devices_change`, the median, `slower_than_usual`, and for the newest 5 `longest_idle_seconds`, `had_idle_gap` and `subtasks_timed_out`. Details in [reference/timing.md](reference/timing.md).

**view logs.** One device's collection log at or above a level (`failure`, default WARN; give INFO to see the end), with a `log` block for the whole log: line count, first and last line and time, last 20 lines, lines mentioning cancel or timeout. An empty log is **unknown**, not clean; an import has no log. The text can quote commands and device output: keep it out of issues and public places.

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
