# edit-snapshot: cancel

`action: cancel` stops the running or queued backend jobs of one snapshot (a predicted snapshot's Protocol Modeling is one). Forward has no snapshot-level cancel route: the only server-side stop is cancelling jobs (`DELETE /api/jobs/{cancelLink}`), and both that and the list of active jobs it takes the links from need an **ADMINISTER_SYSTEM** credential (a system administrator, and on some deployments an organization administrator); an ordinary user cannot. Without it the result is `unknown` and says so.

Inputs: `network_id`, `snapshot_id` (numeric, so it can be matched to a job row), then `apply: true` and `confirm` equal to the snapshot id. The dry run lists the active job rows that carry the snapshot's id (type, organization, queued, running, seconds) and changes nothing.

What it does and does not do:
- It cancels EVERY top-level job row of that snapshot, including other users' queries, diffs and path searches (a grouped row's link cancels all of that type on the snapshot). Shared work is shared: cancelling a base snapshot's job can fail it for every other prediction waiting on it.
- It does NOT cover the per-device processing of an import, a reprocess or a collection (those rows carry no snapshot id): an import can keep parsing after a cancel. A collection is stopped with the collector task stop; a snapshot still waiting in the processing throttle queue is removed by `invalidate`.
- The stop is cooperative: Protocol Modeling checks for cancellation between path-selection prefixes, route-propagation seeds and DAG node creation, and some steps run to their end, so expect a delay, not a thread interrupt. The result reads the active jobs back; rows still listed are said, not hidden. Run `inspect-jobs` again before relying on it.
- No active row for the snapshot is `unknown`, never a success (it may have finished, be per-device work, or be queued in the throttle).
- Waits (`fwdctl wait`, `edit-change-set` with `run`, `verify-change`) never cancel by themselves; a timed-out wait names this action.
- There is no undo: cancelled jobs are not resumed. Reprocess the snapshot or run the prediction again.
