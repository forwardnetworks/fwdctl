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
3. Write the query against the cheat sheet and the rules for choosing what to query (`fwdctl describe author-nqe-query reference/cheatsheet.md` and `reference/rules.md`). Do not invent schema names.
3a. Before running anything, check it offline: `fwdctl nqe lint query.nqe` (no Forward connection, milliseconds). It reports syntax errors with line and column, unknown names and wrong argument counts, fields or enum values the data model does not have (with the closest real ones), type errors, and deprecated constructs with Forward's own advice. Its type check is gradual: where it cannot tell a type it says nothing, so a clean result is not proof, but an error it reports is one Forward reports. The data model, standard library and Forward's own built-in query library (`@fwd/...` imports) are fully checked offline. **An import of your own organization's saved query is not**: lint warns ("not in Forward's built-in library... fwdctl nqe bundle resolves it") instead of guessing, because that library is per-organization and changes with every commit, unlike the sealed `@fwd/...` signatures. If the warning appears, run `fwdctl nqe bundle --path <entry>` first (it fetches the real source of every module you import, live, and inlines it) and lint the bundle instead for the same full coverage you get on Forward's own library.
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

- `reference/cheatsheet.md` (served by `fwdctl describe author-nqe-query reference/cheatsheet.md`): syntax forms and the roots of the data model. Read before writing.
- `reference/rules.md` (served by `fwdctl describe`): what to query: exact schema names, filters, matching the question's intent, `configured` fields, config-compliance patterns, and why not to filter on `DeviceType.ROUTER` or `SWITCH`. Read before writing.
- `reference/syntax-and-types.md` (served by `fwdctl describe`): type keywords, single-line versus block patterns, parenthesising a comprehension used as a value, `order by` and `limit`, and typed time arithmetic. Read when the query sorts, limits, does time arithmetic or matches patterns, or when a diagnostic mentions a type.
- `reference/std-lib.md` (served by `fwdctl describe`): every built-in function and constant, one entry each, generated from Forward's own documentation. Read when `fwdctl context schema` or a diagnostic names a function you have not used before.

### Language guides

Forward's own long-form language documentation, one topic per file, generated from Forward's docs and served by `fwdctl describe author-nqe-query reference/<name>.md`. The cheat sheet and rules above are the short version; read a guide when a diagnostic, a type or a construct needs more than a line of explanation.

Values and types: `reference/guide-booleans.md`, `reference/guide-numbers.md`, `reference/guide-strings.md`, `reference/guide-nqe-types.md`, `reference/guide-records.md`, `reference/guide-sets.md`, `reference/guide-collections.md` (Bag vs List, and the 25.11 typing change), `reference/guide-oneofs-enumerations.md`, `reference/guide-missing-values.md`, `reference/guide-expressions-types.md`, `reference/guide-top-level-expression.md`.

Writing queries: `reference/guide-comprehensions.md` (the full walkthrough: qualifiers, `order by`, `limit`, nested comprehensions), `reference/guide-if-expressions.md`, `reference/guide-comparisons.md`, `reference/guide-parameterized-queries.md`, `reference/guide-user-defined-constant-functions.md`, `reference/guide-imports.md`, `reference/guide-primary-keys.md`.

Data and matching: `reference/guide-block-patterns.md`, `reference/guide-regexes.md`, `reference/guide-json.md`, `reference/guide-csv.md`, `reference/guide-data-extraction.md` (pattern matching against collected configuration).

Network-specific types: `reference/guide-ip-addresses.md`, `reference/guide-ip-subnets.md`, `reference/guide-mac-addresses.md`, `reference/guide-device-groups.md`, `reference/guide-snapshot-data.md`, `reference/guide-time.md`.

