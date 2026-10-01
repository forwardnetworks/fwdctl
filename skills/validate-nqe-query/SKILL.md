---
name: validate-nqe-query
description: Checks that an NQE query compiles and runs against a snapshot and reports diagnostics or rows. Use after writing a query, before trusting results, or when one fails.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "nqe"
  summary: "compile and run a query"
  maturity: "3"
  tools: "snapshots, query"
---

# validate-nqe-query

## Intent

Authoring an NQE query is the harness model's job; **knowing whether it works is Forward's**. This
skill submits a candidate query and returns Forward's own verdict: compile errors with their position,
or the rows it produced. It exists so an agent loops on real diagnostics instead of guessing.

## Inputs

`network_id`, `query` (NQE source). Optional `snapshot_id` (default: newest processed, collected
snapshot), `parameters`, `sample_rows` (default 5, at most 200) and `offset`, so any query can be read in pages. See `schema.json`.

## Writing the query (what the harness should do first)

Run `fwdctl context nqe "<the question>"` on the CLI ; `fwdctl context schema "<term>"` looks up real field and enum names. It returns Forward's NQE
rules and the closest worked examples. Follow the rules strictly: use only schema names that exist,
apply named vendors/OS as explicit `where` filters, filter before you emit a `violation` flag, prefer
`configured*` fields for configuration questions, and never filter on `DeviceType.ROUTER/SWITCH`.

## Procedure

1. Resolve the snapshot; it must be processed. Otherwise `unknown`.
2. Run the query with a small row limit. Our own offline reader (`fwdctl nqe lint`) also reads it: its deprecation warnings go into `limits`, and when Forward rejects the query its reading is attached as `offline_lint`. Before calling this skill, run `fwdctl nqe lint` yourself: it needs no connection and catches syntax errors in milliseconds.
3. Decide:
   - Forward rejects the query (syntax, type or schema error): **failed**, with each diagnostic and
     its line and column, plus schema hints: an enum value that does not exist comes with the closest
     real values (`Vendor.PALO_ALTO` -> `PALO_ALTO_NETWORKS`). This is a deterministic finding about the query, not about the network.
   - It runs and returns rows: **ok**, with the total row count and a bounded sample.
   - It runs and returns **zero rows**: **unknown**. Zero rows may mean the condition is absent or
     that the query filtered on the wrong thing; the skill cannot tell them apart. On a predicted
     snapshot the limit says some signals are empty there regardless.

## Previewing a synthetic device query without saving it

Give `synthetic_kind` (internet, intranet, l3vpn, adjacent-network or l2vpn) to run an **unsaved** query on a snapshot and also check its rows as that kind's connections: the rows shown are the connections it would generate, and a row type
that does not fit (a missing or mistyped column, `vlan: 0`) makes the result **failed**. This costs no commit. Forward's own compute (`edit-synthetic-query`'s dry run) takes only a **committed** library query, so that last check
(a bad site name, empty subnets with discovery `none`) needs the query saved first, which is visible to the whole organization: use a clearly named scratch path (for example `/Scratch/<name>`) with `edit-nqe-query` and delete it
afterwards (`edit-nqe-query` with `delete: true`), or save it at its real path once it is right.

## Evidence

`nqe` items: the diagnostics, or the row count with a sample.

## Output

The envelope in `schema/skill-result.schema.json`. `next_actions`: `check-network-compliance` to use a
validated violations query as a policy.

## Next actions

`check-network-compliance`.

## Running this skill

Give the inputs as one JSON object on stdin: `echo '{"network_id": "<id>"}' | fwdctl run validate-nqe-query`. It needs the
`fwdctl` binary and `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD` in the environment. Read `status`
(`ok`, `failed`, `unknown`, `error`) and the `limits`; `unknown` is never a pass. Inputs are listed above and in `schema.json`.
