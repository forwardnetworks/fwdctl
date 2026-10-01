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
`collector_task_id`; `devices` names the failed devices and takes `failure` (a category such as
`credentials`, `network_path`, `device_session`, `unclassified`, `processing`, or a type such as
`CONNECTION_REFUSED`), `device` (name contains) and `limit`/`offset`; `neighbors` lists unmodelled
neighbours and takes `limit`/`offset`; `slow` ranks devices by collection time and takes `device`, `limit`, `offset`; `logs` reads one
device's collection log and takes `device` (required), `failure` (here the minimum level TRACE, DEBUG, INFO, WARN (default) or ERROR),
`limit`, `offset`. See `schema.json`.

**view devices.** Reads each device's recorded result from the snapshot's device model (NQE
`device.snapshotInfo.result`) and returns one row per failed device: stage, category, error type,
vendor, OS, model, collection IP. For processing failures it adds Forward's exception for them: the
device names and the first line of the stack trace (the parser's class and message). That needs the
DEBUG_SNAPSHOTS permission and is reported as not read otherwise; when Forward's exception list holds no record for the failures (measured: a network with 12 parser failures returned none) the limits say so and the message is not available. Forward's API gives no line or
file region for a parser exception, so go on to `inspect-device-files` for the device's raw config.
The snapshot's metrics count every failed device, but the model lists only devices Forward built a
record for; when the two differ, the limits say so.

**view slow.** Forward's per-device collection metrics: collection duration, the slowest command and its duration, source and device type,
jump server, and the merged collection-and-processing error (so every error class, not only failures), slowest first, with the median, p95
and max. Forward keeps one slowest command per device, not every command, and saves nothing for an imported, forked or partially collected
snapshot (then **unknown**). Not yet run against a live full collection: the networks available at release time held only imports.

**view logs.** A window of one device's collection log at or above a level (`failure`, default WARN), read up to 512 KiB. `device` must be
the name the device was collected under. An empty log is **unknown**, not clean; an import has no log. The text can quote commands and
device output: keep it out of issues and public places. Not yet run against a live full collection.

**Parser failures.** Forward marks a supported device PARSER_EXCEPTION whenever processing fails, even when it stored no exception, so an
empty exception list is expected and no message, class or line exists to read: the device list, its category and `collectionError`
(sometimes the root cause) and the raw files are what there is.

**view neighbors.** Lists the neighbours Forward sees but does not model, with discovery method,
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
