# Skill contract

A Forward Skill describes **how to accomplish a network-engineering objective using
Forward**. It is not an API wrapper. The harness owns orchestration; a skill owns
expertise and returns evidence.

## Layout

```text
skills/<name>/
  SKILL.md         frontmatter + the sections below
  schema.json      input schema (JSON Schema subset)
```

The Go implementation of a skill sits beside these files in package `skills` (`reachability.go` for
`investigate-reachability`, and so on) and is registered with `skills.Register`. A skill with no
implementation is a procedure-only skill: the harness loads its body and follows it.

`<name>` is lowercase kebab-case and names an objective (`investigate-reachability`),
never an endpoint (`get-paths`).

## SKILL.md

Frontmatter (the same shape Claude Code skills use, so a skill ports as-is):

```yaml
---
name: investigate-reachability
description: Triggers, not a summary. When an agent should reach for this skill.
maturity: 3
tools: [paths, query, snapshots]   # Forward capabilities it may use; not a grant
---
```

Sections, in order: **Intent**, **Inputs**, **Procedure**, **Tools**, **Evidence**,
**Output**, **Next actions**.

## Result

Every skill returns the envelope in `schema/skill-result.schema.json`, built with
`result.Build`:

| Field | Meaning |
|---|---|
| `status` | `ok` (objective holds), `failed` (a finding), `unknown` (data cannot decide), `error` (skill could not run) |
| `confidence` | `deterministic` (read from the twin), `inferred`, `unknown` |
| `evidence[]` | typed items, each with the operation and snapshot it came from |
| `limits[]` | what was not measured, truncated or unavailable |
| `next_actions[]` | skills that may follow; a recommendation, never a command |
| `context` | network, snapshot id/time, current/historical/predicted |

Rules the schema enforces mechanically:

1. `ok` and `failed` need at least one evidence item and a non-`unknown` confidence.
2. `unknown` needs `confidence: unknown` and at least one entry in `limits`.
3. **Absence is not evidence.** An empty NQE result, a zero-row check or a missing
   device is `unknown` with a limit that says so. It is never `ok`.

## Procedure, not facts

A skill states the order of operations and what a result means. It does not embed
facts that Forward can report (supported platforms, device counts). Read them at run
time and cite the snapshot.

## Boundaries

- No orchestration inside a skill. It may suggest `next_actions`; it does not call
  other skills.
- No harness assumptions: nothing here depends on a particular agent framework or on
  MCP. The CLI and the Go library expose the same contract; a harness that wants MCP wraps them.
- Bounded output: filter and project close to the data, report truncation in `limits`.

## Reaching Forward

A skill reaches Forward only through the vendor SDK (`forward-sdk`), via the wrappers in
package `fwd` (`SearchPaths`, `RunNQE`, `LatestProcessed`, ...). The wrappers add what the contract
needs: retries off, an operation log, refusal to query an unprocessed snapshot, and bounded
results. A skill never builds an HTTP request itself.
