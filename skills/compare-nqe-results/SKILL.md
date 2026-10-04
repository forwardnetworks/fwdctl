---
name: compare-nqe-results
description: Shows which rows a saved NQE query gains, loses or changes between two snapshots. Use when asked what changed between two snapshots for one kind of data, or to diff a query before and after.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "nqe"
  summary: "query rows changed"
  maturity: "3"
  tools: "snapshots, query"
---

# compare-nqe-results

## Intent

Answer "what changed in X between these two snapshots" as exact added, deleted and modified rows, for any data an
NQE query can express. `analyze-blast-radius` counts differences by area; this skill shows the rows.

## Inputs

`network_id`, `before_snapshot_id`, `after_snapshot_id`, and `query_id` of a query **committed to the NQE library**
(use `find-nqe-query` to get one). Forward cannot diff ad-hoc query text. Optional: `commit_id`, `limit` (default 25,
at most 100). See `schema.json`.

**Two networks.** Give `after_network_id` and `before_snapshot_id` is read in `network_id` while `after_snapshot_id` is read in `after_network_id` (a seed and a lab, production and a copy). Forward's own diff works inside one network, so the saved query is run on both and the rows are diffed here: `key` names the result columns that identify a row (default the whole row, so only rows on one side are found), `ignore` drops columns that are expected to differ (names, ids, addresses) from both sides first. The result lists rows only in A, only in B, and, with a `key`, rows whose other fields differ (field, A value, B value). Each side is read up to 200,000 rows; a longer result is said to be cut. `key` and `ignore` are refused without `after_network_id`.

## Procedure

1. Both snapshots must be processed. Otherwise `unknown`.
2. Diff the query's results across the snapshots.
3. Decide:
   - Rows differ: **ok**, with the total, the counts by type, and the first `limit` rows (before and after).
   - No differences: the skill runs the query once on the after snapshot. If it returns rows, **ok** with zero
     differences; if it returns nothing, **unknown**, because an empty result on both sides says nothing about the
     network.
   - Unknown query id: **unknown**.
4. A prediction on either side is stated as a limit.

## Evidence

One `nqe` item: the query id, both snapshot ids, the total, counts by type, and the rows.

## Output

The envelope in `schema/skill-result.schema.json`.

## Next actions

`analyze-blast-radius`, `verify-change`, `validate-nqe-query`.

## Running this skill

Give the inputs as one JSON object on stdin: `echo '{"network_id": "<id>"}' | fwdctl run compare-nqe-results`. It needs the
`fwdctl` binary and `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD` in the environment. Read `status`
(`ok`, `failed`, `unknown`, `error`) and the `limits`; `unknown` is never a pass. Inputs are listed above and in `schema.json`.
