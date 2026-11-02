---
name: edit-nqe-query
description: Saves an authored NQE query to the organization's library, or removes one, showing the effect. Dry run unless apply is true. Use when a checked query should be kept for the team or dropped.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "nqe"
  summary: "save or remove a query"
  maturity: "2"
  class: write
  effect: "org"
  secrets: "false"
  reversible: "true"
  tools: "nqe"
---

# edit-nqe-query

## Intent

Keep a good query. The organization's NQE library is shared: a committed query is visible to everyone in the organization and can be run
by path or by its query id. This skill adds, replaces or removes one entry and commits it. It changes no network data.

## Inputs

What each input means, and the fields per object and action: `fwdctl run edit-nqe-query --help`, or `reference/inputs.md`.

Read before applying (printed by `fwdctl run edit-nqe-query --help`, from `reference/inputs.md`): Discarding a stray draft, Directories.

**`discard_draft` drops the draft at that path, which on a shared login may be a colleague's: check first. A save needs an existing directory (one exists only while a committed query is under it).**

## Dry run

Without `apply: true` nothing is changed. The skill reads the library, and returns `mode: dry_run` with one change whose `before` is the
current source (empty when the path is new) and `after` the new one. Show it to the person who asked before applying.

`network_id` is accepted and unused: the NQE library is organization-wide, so the same inputs work as for `find-nqe-query`.

## Procedure

1. Check the source offline (`fwdctl nqe lint`). A query with errors is **failed** and is not saved; use `author-nqe-query` to fix it.
2. Read what the library holds at the path. Identical source means nothing to do; deleting a path that is not there means nothing to do.
3. Dry run: return the plan. With `apply: true`: add or delete in the workspace, then commit that path. If the commit fails the draft is removed again.
4. Read the library back and compare. A library that does not hold the requested state is **failed**, never ok.

## Undo

The change carries `before` and `undo`. A new query is undone by running this skill with `delete: true`; a replaced or deleted query by
running it again with the prior source (`before`) and `apply: true`. Each undo is itself a commit.

## Evidence

One `state` item with `path`, whether it `existed`, `mode` and, once applied, `held` and the `query_id`.

## Output

The envelope in `schema/skill-result.schema.json`, with `mode` and `changes`.

## Next actions

`validate-nqe-query` to run the saved query against a snapshot; `find-nqe-query` to find it later.

## Running this skill

Run `fwdctl run edit-nqe-query --help` for the inputs and an example, then give them as one JSON object on stdin: `fwdctl run edit-nqe-query < input.json`. It is a dry run unless the input has `"apply": true`; add that only after the dry run was accepted. Read `status` (`ok`, `failed`, `unknown`, `error`), `mode` and the `limits`; `unknown` is never a pass.

## Examples

```bash
echo '{"path": "/Team/Devices without NTP", "source": "foreach d in network.devices select {name: d.name}"}' | fwdctl run edit-nqe-query
```
