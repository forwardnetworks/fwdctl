---
name: edit-nqe-query
description: Saves an authored NQE query to the organization's library, or removes one, showing the effect. Dry run unless apply is true. Use when a checked query should be kept for the team or dropped.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "nqe"
  summary: "save or remove a query"
  maturity: "2"
  class: write
  reversible: "true"
  tools: "nqe"
---

# edit-nqe-query

## Intent

Keep a good query. The organization's NQE library is shared: a committed query is visible to everyone in the organization and can be run
by path or by its query id. This skill adds, replaces or removes one entry and commits it. It changes no network data.

## Inputs

`path` (the library path, starting with `/`), and either `source` or `delete: true`. Optional: `message` (commit title) and `apply` (default
false). See `schema.json`.

**Several queries in one commit.** Instead of `path` and `source`, give `changes`: a list of `{path, source}` (up to 25; modules that import each other change together), `message` (the
commit title), optional `basis_commit_id` (the library head the edits were made against: the plan and the apply refuse if the head is another commit, and say so) and `typecheck: true`
(optionally with `snapshot_id`). The dry run lints every source offline, compares each with what is committed, writes nothing and says so when dependents were **not** typechecked. With
`typecheck: true` it **stages the changes as drafts in your workspace, has Forward type every changed query and every query that imports one (and counts the checks and dashboards that
use them), then restores the drafts** (Forward has no discard; restoring stages the committed source again, which is not proven to leave no pending-change marker, so look at the NQE
editor if the limits say a restore failed). Any new error, or a change this login may not commit, is **failed** and nothing is committed. `apply: true` re-reads the head, commits all
paths as ONE commit, reads each path back at the new head, and returns `previous_commit_id` and `commit_id` in the evidence (the commit call itself returns none; the head is read
afterwards). Forward has no optimistic-concurrency check, so a commit landing between the head check and the commit is not caught. This form edits and adds; it does not delete or
create directories.

## Directories

A query must sit in a directory that **exists**, and a library directory exists only **while a committed query is under it** (verified live: deleting the last query removes the directory, and a save into it is then refused with a 409). The library has Forward-owned roots
(for example `/Security`, `/L3`) and the organization's own folders; the root `/` always exists, so a query at the root is `/Name`. A save whose enclosing directory has no committed query under it is **failed** with the directory named (nothing written):
either save at the root, or run again with `create_directory: true`, which adds the missing directories (parents first) and commits the query with them (the dry run lists each directory as its own change). There is no separate directory delete: the directories
go with their last query, so the undo of a `create_directory` save is deleting the query.

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

Give the inputs as one JSON object on stdin: `echo '{"path": "/Team/Devices without NTP", "source": "foreach d in network.devices select {name: d.name}"}' | fwdctl run edit-nqe-query`.
It needs the `fwdctl` binary and `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD` in the environment. Read `status`
(`ok`, `failed`, `unknown`, `error`), `mode` and the `limits`; `unknown` is never a pass. Add `"apply": true` only after the dry run was accepted.
