# edit-internet-exclusions: inputs

`network_id` and one of: `add` and/or `remove` (lists of public CIDR blocks, changes to the current list), or `set` (the whole list; `[]` clears it). Optional: `backdate_snapshot_id` and `apply` (default false). Each entry is a CIDR with the host bits
zero (a bare address is a /32); a block lying entirely inside private, shared (100.64.0.0/10), loopback, link-local, documentation, benchmarking (198.18.0.0/15), multicast or reserved space (Forward's own list) is refused in the dry run, with EVERY such entry named, before anything is sent. A list taken from a routing-table dump often holds some: filter them first.

## Read before applying

### Backdate (optional): apply it now

By default a change reaches the **next** processed snapshot. With `backdate_snapshot_id` the internet node's configuration is re-dated to that snapshot: that snapshot and **every later one are invalidated**, set to
UNPROCESSED. Forward does **not** reprocess them by itself: run `edit-snapshot` (action reprocess) for each one, then `edit-advanced-reachability` if internet exposure is needed (it only runs after a snapshot is PROCESSED). The
dry run lists them. Ask for it only when the person wants it applied now, and get approval of the backdate separately. It cannot be undone; the list change can.

### Limits worth stating

The skill cannot show how paths change: trace the flow before and after. Exclusions are **prefix-based** (no per-interface or per-device exclusion): an address that a collected interface carries is located on that interface before the internet node is considered, so excluding it does not hide it from an internet-addressable result (`plan-synthetic-device`, `reference/exposure.md`). An exclusion keeps a prefix off the internet node, it does not make it reachable. Clearing the list gives every kept-off prefix back to the internet node.
