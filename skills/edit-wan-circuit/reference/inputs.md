# edit-wan-circuit: inputs

`network_id`, `name`, and either `connection1` and `connection2` (each `device`, `port`, optional `vlan` 1 to 4095 and `name`) to create or replace the circuit, or `delete: true`. Optional: `backdate_snapshot_id` and `apply` (default false). See `schema.json`.


## Read before applying

### Backdate (optional): apply it now

By default a change reaches the **next** processed snapshot. With `backdate_snapshot_id`, after the change the circuits are backdated to that snapshot: that snapshot and **every later one are invalidated**, set to
UNPROCESSED. Forward does **not** reprocess them by itself: run `edit-snapshot` (action reprocess) for each one, then `edit-advanced-reachability` if internet exposure is needed (it only runs after a snapshot is PROCESSED). The
dry run lists them. Ask for it only when the person wants it applied now, and get approval of the backdate separately. It cannot be undone (reprocessing recomputes the same data); the circuit change can.
