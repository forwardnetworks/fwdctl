---
name: find-nqe-query
description: Searches the saved NQE query library for queries relevant to a question and returns ids, paths and intent. Use before writing a query from scratch, or to get a query id for a check.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
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

`network_id` and one of `question` (plain words, to search), `query_id` (a `Q_` id: return that saved query's source) or `path` (the same, by library path), with optional `commit_id` to read it at a library commit instead of the head (the result names the commit; a path is looked up among the head's paths). Optional: `list: true` (browse instead of search: one level of the library as a tree, the directories under `directory`, default `/`, with how many queries are below each and the queries directly in it; use it to learn the top-level folders or whether a directory exists), `directory` (limit the library to a folder), `limit` (default 8,
at most 25). See `schema.json`.

**What a commit is.** `commit_id` on its own (no `question`, `query_id` or `path`; `head` is the head) says what that library commit last changed and who made it: the queries whose last change it was, plus its author, time
and title from the history of one of them. Forward has no list of library commits, so a commit that no query last changed in is **unknown**, not empty. The author is personal: keep it out of public places.

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
