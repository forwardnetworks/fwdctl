# edit-link-overrides: inputs

`network_id`, `snapshot_id`, and any of `add_present`, `remove_present`, `add_absent`, `remove_absent`: lists of links, each `{port1, port2}`
(the port names as `inspect-topology` shows them; a link has no direction). Optional: `apply` (default false), `force` (default false). Network-level mode (see Staged mode below): `staged`, `backdate_to_snapshot_id`, `confirm`, and no `snapshot_id`. See `schema.json`.


## Read before applying

### Undo

The change's `undo` is the exact inverse edit (additions become removals and the other way round). Run this skill with it and `apply: true`.
Overrides are written per snapshot, and **writing them invalidates the snapshot** (and later snapshots up to the end of its override range), setting them UNPROCESSED; Forward does not reprocess them by itself, so apply
only with the person's approval and when that is acceptable, then, **only after asking the person, one snapshot at a time (never chained onto the apply; a 1,400-device snapshot takes about 40 minutes)**, run `edit-snapshot` (action reprocess) for each one (and `edit-advanced-reachability` after, if internet exposure is needed). Whether a later snapshot carries
them is not stated by Forward's API: compare snapshots with `inspect-topology` with `compare_to_snapshot_id`. The playbook `plan-link-overrides` covers missing or drifted overrides end to end.

### Staged mode (network level, Forward 26.8+)

`staged: true` edits the network's STAGED overrides (same add/remove inputs, no `snapshot_id`): they leave every existing snapshot alone and apply to the network's next snapshot. To push the staged set onto an existing snapshot, run alone `backdate_to_snapshot_id` (dry run first): it invalidates that snapshot and every non-draft snapshot created at or after it, per Forward's rule (Forward has no route that lists them; the dry run computes the list from the snapshot list), and apply needs `confirm` equal to that snapshot id. Edits and a backdate are separate runs so the person is asked before anything is invalidated, and a reprocess after a backdate is asked for too, one snapshot at a time. The snapshot-scoped mode above is deprecated by Forward for removal in 26.11.
