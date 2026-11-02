# edit-synthetic-query: inputs

`network_id`, `kind` (`internet`, `intranet`, `l3vpn`, `l2vpn` or `adjacent-network`) and, for every kind but `internet`, the node `name`. Then one of: `query_id` (`Q_...` from the
organization's library, or `FQ_...` from Forward's), `detach: true`, or neither (lists the queries that fit the kind). Optional: `backdate_snapshot_id` (apply it to an existing snapshot now, see below) and `apply` (default false). See `schema.json`.


## Read before applying

### Backdate (optional): apply it now

By default a change reaches the **next** processed snapshot. With `backdate_snapshot_id` the skill, after attaching, backdates the node kind's configuration to that snapshot: that snapshot and **every later one are
invalidated**, set to UNPROCESSED. Forward does **not** reprocess them by itself: run `edit-snapshot` (action reprocess) for each one, then `edit-advanced-reachability` if internet exposure is needed (it only runs after a
snapshot is PROCESSED). The dry run lists the snapshots affected. Ask for it only when the person wants the change applied now; get approval of the backdate itself, separately from the query change. The backdate cannot
be undone (reprocessing recomputes the same data from what was collected); the query change still can.

### Limits to state

Forward recomputes when the node is saved or the query is committed or deleted (deleting the query detaches it from every node that used it). Whether it also recomputes on each new
snapshot is not verified. Connections apply to the next processed snapshot. WAN circuits and encryptors cannot be defined by a query through the API.
