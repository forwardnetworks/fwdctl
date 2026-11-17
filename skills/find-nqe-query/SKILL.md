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

Three more forms read the whole library (the details are in `reference/inputs.md`): `list` shows one level of the tree; `recursive: true` (or `list` with `intent`) lists every query under `directory` at any depth with its intent; `intent` keeps only queries whose intent text matches a regular expression (`^\s*$` finds the ones with no intent), and with `question` narrows the matches; `repository` (ORG or FWD) keeps one repository's queries, and `offset` with `limit` pages a listing or moves a source search's window over a library of more than 300 queries; `source_matches` searches the SOURCE of the organization's queries under `directory` for a pattern (at most 300 sources per call, and a partial search says so: no match in a partial search is **unknown**).

With `query_id` or `path` the skill instead reads that one saved query of the organization's library and returns its source in a `nqe`
evidence item (**ok**, deterministic); a query that is not there is **unknown**. The built-in library (`FQ_` ids) is searched by
`question` but its source is not read.

## Evidence

One `nqe` item listing the matches: id, path, intent, repository, words matched. A recursive listing's item lists every query under the directory (path, id, intent, repository) with the count in all and the count that matched `intent`; a source search's item lists each matching query with its matching lines and how many sources were read of how many qualified.

## Output

The envelope in `schema/skill-result.schema.json`.

## Next actions

`validate-nqe-query` to run one, `check-network-compliance` to use one as a policy, `compare-nqe-results` to diff one.

## Running this skill

Run `fwdctl run find-nqe-query --help` for the inputs and an example, then give them as one JSON object on stdin: `fwdctl run find-nqe-query < input.json`. Read `status` (`ok`, `failed`, `unknown`, `error`) and the `limits`; `unknown` is never a pass.
