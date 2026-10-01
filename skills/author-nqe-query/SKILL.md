---
name: author-nqe-query
description: Guides an NQE question from words to a saved query: find, write, lint, run, keep. Use when writing, fixing, checking or saving an NQE query, or when one fails to compile.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "nqe"
  summary: "write NQE queries"
  maturity: "3"
  tools: "find-nqe-query, validate-nqe-query, compare-nqe-results, edit-nqe-query"
---

# author-nqe-query

## Procedure

0. Check for a saved query first: run `find-nqe-query`. A saved query needs no authoring.
1. Get the closest worked examples for the question: `fwdctl context nqe "<question>"` on the CLI, or the
   `nqe_examples` tool. They are real queries for real questions; imitate their shape, not their names.
2. Look up the real names you are about to use: `fwdctl context schema "<term>"` (or the `nqe_schema` tool) searches Forward's
   data model and lists enum values. Never guess a field or an enum value.
3. Write the query against [the cheat sheet](reference/cheatsheet.md) and [the rules for choosing what to query](reference/rules.md). Do not invent schema names.
3a. Before running anything, check it offline: `fwdctl nqe lint query.nqe` (no Forward connection, milliseconds). It reports syntax errors with line and column, unknown names and wrong argument counts, fields or enum values the data model does not have (with the closest real ones), type errors, and deprecated constructs with Forward's own advice. Its type check is gradual: where it cannot tell a type it says nothing, so a clean result is not proof, but an error it reports is one Forward reports.
4. Run `validate-nqe-query`. A compile error is a deterministic finding with a position: fix exactly what
   it names and run it again. Do not guess a second change.
5. Zero rows is **unknown**, not "compliant". Decide whether the filter or the network explains it before
   you report anything.

## From a question to a saved, checked query (the former `plan-author-query` playbook)

Prefer a saved query to a new one, and never save one that has not run. The Procedure above is steps 1 to 4 of the sequence; the whole sequence is:

1. **Reuse.** `find-nqe-query` with the question; read a promising one with `path` or `query_id`. A saved query that answers the question is the answer.
2. **Learn the data and write.** Procedure steps 1 to 3: worked examples, real field names, the cheat sheet. Do not invent names.
3. **Check offline.** `fwdctl nqe lint` (and `fwdctl nqe fmt`) before any server round trip. Fix every error; read every warning.
4. **Run it.** `validate-nqe-query` against a processed snapshot: the authoritative type check and the rows. Zero rows is **unknown**, not "none".
5. **Compare.** To see what a query's answer did between snapshots, `compare-nqe-results`.
6. **Keep it.** `edit-nqe-query` (follow `plan-safe-write`): the commit is visible to the whole organization.

The minimum before you answer: the existing-query search (step 1), the offline lint (step 3) and a run against a snapshot (step 4) before saving anything. Stop earlier only when the evidence already answers the question, and say so. Answer with the query, the snapshot it ran on, the row count and what it does not cover; if it came from the library, say so with its path.

## Reference

The authoring references are not in the public source tree; the official release carries them.


