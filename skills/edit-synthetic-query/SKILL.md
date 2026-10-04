---
name: edit-synthetic-query
description: Attaches a saved NQE query to a synthetic node so Forward generates its connections from the rows, or detaches it. Dry run unless apply is true. Use when driving a node from a query.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "edge-synthetic"
  summary: "query-driven synthetic node"
  maturity: "1"
  class: write
  effect: "network"
  secrets: "false"
  reversible: "true"
  tools: "synthetic nodes, nqe"
---

# edit-synthetic-query

## Intent

Forward can build a synthetic node's connections from a saved NQE query instead of a hand-written list: the node holds a query id, and Forward runs the query's last
commit on the latest processed snapshot, one row per connection, merged with any manual connections. This skill points a node at such a query, swaps it, or removes it.
It never writes the connections themselves, and it creates no node (the node must exist). This is a **preview feature** of Forward (not in its published API spec).

## Inputs

`network_id`, `kind` (`internet`, `intranet`, `l3vpn`, `l2vpn` or `adjacent-network`) and, for every kind but `internet`, the node `name`. Then one of: `query_id` (`Q_...` from the
organization's library, or `FQ_...` from Forward's), `detach: true`, or neither (lists the queries that fit the kind). Optional: `backdate_snapshot_id` (apply it to an existing snapshot now, see below) and `apply` (default false). See `schema.json`.

## Dry run

Without `apply: true` nothing is changed. The skill reads the node, then asks Forward to **compute** the query as if it were attached (this changes nothing) and returns
`mode: dry_run` with one change (`before` and `after` query id) and the preview: the connection count, the first rows and any error. Show it to the person before applying.

## Procedure

1. Read the node. A missing node is **unknown** (nothing changed).
2. With no `query_id` and no `detach`: list the saved queries whose rows fit the kind, and stop (**ok**, nothing changed).
3. If the node already uses that query, say so (nothing to change).
4. Preview the query with Forward's own compute. An error (`COLUMN_DATATYPE_MISMATCH`, `MISSING_REQUIRED_COLUMNS`, `QUERY_RUN_ERROR`, `QUERY_MISSING`, `NO_LATEST_SNAPSHOT`, `INVALID_IDENTIFIER`) is **failed** and the query is not attached.
   Offline, `fwdctl nqe lint --synthetic <kind>` finds the row problems first (and `fwdctl nqe template <kind>` gives the starter query).
5. Dry run: return the plan. With `apply: true`: set the query (or detach), then read the node back. The new query id must be there and its result error-free; anything else is **failed**, never ok.

## Backdate (optional): apply it now

By default a change reaches the **next** processed snapshot. With `backdate_snapshot_id` the skill, after attaching, backdates the node kind's configuration to that snapshot: that snapshot and **every later one are
invalidated**, set to UNPROCESSED. Forward does **not** reprocess them by itself: run `edit-snapshot` (action reprocess) for each one, then `edit-advanced-reachability` if internet exposure is needed (it only runs after a
snapshot is PROCESSED). The dry run lists the snapshots affected. Ask for it only when the person wants the change applied now; get approval of the backdate itself, separately from the query change. The backdate cannot
be undone (reprocessing recomputes the same data from what was collected); the query change still can.

## Undo

The change's `undo` says what to run: this skill again with the previous `query_id` (or `detach: true` if there was none). A zero-row result is not an error and attaches fine, but it means the
node has no dynamic connections: check the query before relying on it.

## Limits to state

Forward recomputes when the node is saved or the query is committed or deleted (deleting the query detaches it from every node that used it). Whether it also recomputes on each new
snapshot is not verified. Connections apply to the next processed snapshot. WAN circuits and encryptors cannot be defined by a query through the API.

## Evidence

One `topology` item with `kind`, `node`, `query_before`, `mode`, `query_requested`, the `preview` (or, listing, `compatible_queries`) and, once applied, `result_after`.

## Output

The envelope in `schema/skill-result.schema.json`, with `mode` and `changes`.

## Next actions

`inspect-topology` (kind external) to see the node with its query and generated connections; `investigate-reachability` to test a path through it.

## Running this skill

Give the inputs as one JSON object on stdin: `echo '{"network_id": "<id>", "kind": "l3vpn", "name": "dir", "query_id": "Q_..."}' | fwdctl run edit-synthetic-query`.
It needs the `fwdctl` binary and `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD` in the environment. Read `status`
(`ok`, `failed`, `unknown`, `error`), `mode` and the `limits`; `unknown` is never a pass. Add `"apply": true` only after the dry run was accepted.
