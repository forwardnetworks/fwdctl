# edit-internet-exclusions: inputs

`network_id` and one of: `add` and/or `remove` (lists of public CIDR blocks, changes to the current list), or `set` (the whole list; `[]` clears it). Optional: `backdate_snapshot_id` and `apply` (default false). Each entry is a CIDR with the host bits
zero (a bare address is a /32); a block lying entirely inside private, shared (100.64.0.0/10), loopback, link-local, documentation, benchmarking (198.18.0.0/15), multicast or reserved space (Forward's own list) is refused in the dry run, with EVERY such entry named, before anything is sent. A list taken from a routing-table dump often holds some: filter them first.
