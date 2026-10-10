---
name: find-nqe-query
description: Searches the saved NQE query library for queries relevant to a question and returns ids, paths and intent. Use before writing a query from scratch, or to get a query id for a check.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "nqe"
  summary: "search saved queries"
  maturity: "3"
  tools: "query"
---

# find-nqe-query

## Intent

Reuse before writing. Forward ships and organizations save NQE queries; a saved query is already reviewed and
gives an id that `check-network-compliance` and `compare-nqe-results` accept.

## Inputs

What each input means, and the fields per object and action: `fwdctl run find-nqe-query --help`, or `reference/inputs.md`.

## Procedure

1. List the committed library.
2. Rank each query by how many of the question's words appear in its path and stated intent.
3. Decide:
   - Matches: **ok**, confidence `inferred` (a word match, not a read of the query's source), best first.
   - No match: **unknown**. It does not mean no such query exists; the wording may differ. Fall back to
     `author-nqe-query`.

With `query_id` or `path` the skill instead reads that one saved query of the organization's library and returns its source in a `nqe`
evidence item (**ok**, deterministic); a query that is not there is **unknown**. The built-in library (`FQ_` ids) is searched by
`question` but its source is not read.

## Evidence

One `nqe` item listing the matches: id, path, intent, repository, words matched.

## Output

The envelope in `schema/skill-result.schema.json`.

## Next actions

`validate-nqe-query` to run one, `check-network-compliance` to use one as a policy, `compare-nqe-results` to diff one.

## Running this skill

Give the inputs as one JSON object on stdin: `echo '{"network_id": "<id>"}' | fwdctl run find-nqe-query`. It needs the
`fwdctl` binary and `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD` in the environment. Read `status`
(`ok`, `failed`, `unknown`, `error`) and the `limits`; `unknown` is never a pass. Inputs are listed above and in `schema.json`.
