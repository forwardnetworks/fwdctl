# investigate-collection-failure: inputs

`network_id`. Optional `snapshot_id` (default: the newest snapshot in **any** state, since a
failed collection is the point) and `view`: `summary` (default) counts failures by cause and takes
`collector_task_id`; `triage` is first contact with a network — one call merging `summary`, the slowest devices and the worst-failing platform group, takes no inputs of its own; `devices` names the failed devices and takes `failure` (a category such as
`credentials`, `network_path`, `device_session`, `unclassified`, `processing`, or a type such as
`CONNECTION_REFUSED`), `device` (name contains) and `limit`/`offset`; `exceptions` lists the collectors' logged exceptions and takes `device`, `limit`/`offset`; `neighbors` lists unmodelled
neighbours and takes `limit`/`offset`; `slow` ranks devices by collection time and takes `device`, `limit`, `offset`, `compare_to_snapshot_id`; `history` reads how long each recent collection took and takes `limit` (snapshots, default 10, at most 30); `logs` reads one
device's collection log and takes `device` (required), `failure` (here the minimum level TRACE, DEBUG, INFO, WARN (default) or ERROR),
`limit`, `offset`.

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

**view slow.** Each device's collection time and slowest command, slowest first, with total device time, the run's wall time, devices collected at once beside the collector's configured concurrency, the run over time (`stats.in_flight`, with `idle_gaps` and what started after the longest), Forward's queued and running series (`stats.queue`), the collector subtasks running in the gap (`stats.running_in_the_gap`), devices with no recorded duration, the per-device timeout, and `compare_to_snapshot_id` to set two collections side by side. The finding carries the headline numbers. Read the parallelism against the concurrency: well under it means the slots are not the limit; near it means they are. A gap that ends at the per-device timeout, or subtasks that TIMED_OUT, point at the collector, not slow devices. Field meanings, what each can and cannot prove, and the known causes: `reference/timing.md`.

**view history** (takes `limit`, default 10, at most 30). How long each recent collection took, from the snapshots themselves, with devices and `devices_change`, the median, `slower_than_usual`, and for the newest 5 `longest_idle_seconds`, `had_idle_gap` and `subtasks_timed_out`. Details in `reference/timing.md`.

**view logs.** One device's collection log at or above a level (`failure`, default WARN; give INFO to see the end), with a `log` block for the whole log: line count, first and last line and time, last 20 lines, lines mentioning cancel or timeout. An empty log is **unknown**, not clean; an import has no log. The text can quote commands and device output: keep it out of issues and public places.

**Parser failures, view exceptions, view neighbors:** an empty exception list is expected for a parser failure; exceptions are the errors a collector ignored (needs the collector-exceptions permission); neighbors lists unmodelled neighbours, BGP peers first (one unpaged call that can time out: raise `FORWARD_TIMEOUT`). Details: `reference/views.md`.
