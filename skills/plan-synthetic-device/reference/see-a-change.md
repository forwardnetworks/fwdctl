# The fastest safe way to see a synthetic-device change in a trace

Source: Forward's own code for how a synthetic device's configuration is versioned (read in its source), plus what the skills do. **Inferred** or **unknown** marks what Forward does not state: test it.

## How a change takes effect (from Forward's code)

A synthetic device's configuration is a **timeline of versions**: each version starts at a snapshot's creation instant, and the version you are editing is a pending "staging" one. Saving a node, attaching a query or committing the query only changes the
**staging** version. A snapshot uses the version that was valid when it was created, so **only a snapshot created after the change reflects it** (the next collection or upload).
Forward also recomputes a node's query-generated connections when a new snapshot arrives, and when the node is saved or its query is committed or deleted; the result it stores on the node is what `inspect-topology` (kind external, device `<node>`) shows.
That is a preview of the **connections**, not of what traffic does.

## Options, fastest safe first

1. **Backdate to the LATEST snapshot only** (`backdate_snapshot_id` on `edit-synthetic-query`, `edit-wan-circuit` or `edit-internet-exclusions`). The current configuration is re-dated to that snapshot and that snapshot and every later one are
   invalidated and reprocess. Choosing the newest snapshot invalidates only one, so the cost is that snapshot's answers being unavailable until it finishes (minutes). Get approval of the backdate itself. It does not touch collected data.
2. **Start a collection** (`edit-collection`, or the Forward UI) and trace on the snapshot it produces: no invalidation, but as slow as a collection. The network's schedule decides when the next one would happen anyway (`inspect-snapshots` shows recent timing).
3. **`edit-snapshot` (action reprocess) on the latest snapshot alone is, inferred, not enough**: that snapshot already holds the version that was valid at its creation instant, so a reprocess recomputes the same synthetic configuration. Do not count on it; if you try it, confirm with the check below.

## Which snapshot reflects the change

A snapshot reflects it if it was **created after** the change was saved (compare `inspect-snapshots` creation times with the change's time) or if a backdate to it or an earlier one was applied after the change. To prove it, trace a flow
that depends on the change: before (on the old snapshot) and after (on the new one) must differ. Forward does not report which configuration version a snapshot used.

## A trace that proves something

1. Choose the source first. A source that is blackholed at its first hop proves nothing. `inspect-edge` (`view: trace_sources`) does the control trace for you: give it the edge device's own interface address as `target_ip` (and the VRF of the uplink you are testing)
   and it ranks the devices whose control trace is delivered. For a host rather than a device, run the control trace yourself with `investigate-reachability` `src_ip`.
2. Trace to the real destination (for the internet, a public address that is on no interface) on the **old** snapshot and keep the result.
3. Apply the change, get a snapshot that reflects it (above), trace the same flow again, and compare where the path ends (`last_hop`, and for a path dropped at a synthetic device the `vrf` and `entered_from` the skill now shows).
4. Check a flow that **worked before** still works: a change to claim an uplink can break paths that crossed it.
