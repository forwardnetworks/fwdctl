# edit-nqe-query: inputs

`path` (the library path, starting with `/`), and either `source` or `delete: true`. Optional: `message` (commit title) and `apply` (default
false).

**Several queries in one commit.** Instead of `path` and `source`, give `changes`: a list of `{path, source}` (up to 25; modules that import each other change together), `message` (the
commit title), optional `basis_commit_id` (the library head the edits were made against: the plan and the apply refuse if the head is another commit, and say so) and `typecheck: true`
(optionally with `snapshot_id`). Both work only with `changes[]`: a single-query save refuses them, so check one query with validate-nqe-query first. `offline_check` picks the schema of the offline check: `embedded` (default, this build's), `features:FLAG[,FLAG]` (this build's schema with those NQE feature flags on, like `fwdctl nqe lint --features`: for fields behind a flag such as `device.securityPolicy`; `fwdctl nqe features` lists them), `org` (the organization's live schema, like `fwdctl nqe lint --org`: needed for a query that uses a field newer than this build; the result says which schema was used) or `skip` (only with `changes` and `typecheck: true`, so Forward's own typecheck is the gate). The dry run lints every source offline, compares each with what is committed, writes nothing and says so when dependents were **not** typechecked. With
`typecheck: true` (it first stages the same queries unchanged to learn which errors already exist at head: those are `existing_errors` and never stop a commit, only `new_errors` do) it **stages the changes as drafts in your workspace, has Forward type every changed query and every query that imports one (and counts the checks and dashboards that
use them), then discards the drafts** (Forward's discard drops them: the skill refuses to stage when you already have an uncommitted draft at one of the paths, since cleaning up would drop
it, and says when a discard failed so you can look at the NQE editor). Any NEW error in the changed queries or their importers (errors that already existed at head are listed apart as `existing_errors` and do not stop it), or a change this login may not commit, is **failed** and nothing is committed, unless the new errors are exactly the list in `acknowledge_new_errors` (see its section below; a login that may not commit is never overridden). `apply: true` re-reads the head, commits all
paths as ONE commit, reads each path back at the new head, and returns `previous_commit_id` and `commit_id` in the evidence (the commit call itself returns none; the head is read
afterwards). Forward has no optimistic-concurrency check, so a commit landing between the head check and the commit is not caught. This form edits and adds; it does not delete. A new
query needs its enclosing directories: without `create_directory: true` the plan is **failed** and names the missing ones (nothing is changed); with it the plan lists each directory as its own
change, the apply adds them parents first and commits them with the queries in them, and a failure while staging discards the directory drafts again (deepest first) along with the query drafts.
To load a whole folder tree, `fwdctl nqe pack DIR` builds this input from it (the counterpart of `fwdctl nqe export`); the skill itself never reads the filesystem.

## Read before applying

### Discarding a stray draft

`discard_draft: true` with `path` drops **your own uncommitted draft at exactly that path** (an add or an edit left in the NQE editor), nothing else, never a bulk discard and never anything committed.
Dry run by default; the draft's source is recorded in the change so the discard can be undone by saving it again; the apply reads your drafts back and reports failed if one is still listed. On a shared
login the draft may be a colleague's: check first. A path with no draft is a no-op.

### Directories

A query must sit in a directory that **exists**, and a library directory exists only **while a committed query is under it** (verified live: deleting the last query removes the directory, and a save into it is then refused with a 409). The library has Forward-owned roots
(for example `/Security`, `/L3`) and the organization's own folders; the root `/` always exists, so a query at the root is `/Name`. A save whose enclosing directory has no committed query under it is **failed** with the directory named (nothing written):
either save at the root, or run again with `create_directory: true`, which adds the missing directories (parents first) and commits the query with them (the dry run lists each directory as its own change). There is no separate directory delete: the directories
go with their last query, so the undo of a `create_directory` save is deleting the query.

## acknowledge_new_errors: committing code written for a newer release

Forward's typecheck refuses a commit that adds errors, and that includes code written for a release the organization has not upgraded to (a field that exists in the new data model but not yet in this organization's). `acknowledge_new_errors` is the one narrow way through, and only with `changes` and `typecheck: true` (use `offline_check: skip`, since the offline schema lacks the new field too):

1. Run the dry run, or an apply without it: the refusal says "pass acknowledge_new_errors with exactly the list in `typecheck.new_errors_to_acknowledge`", and that list is in the result, one `"path: message"` per new error.
2. Check each error is the one you expect (a field that does not exist yet on this release, not a typo).
3. Run again with `acknowledge_new_errors` set to exactly that list (the order does not matter; a repeated error is listed as often as it occurs).

The commit goes ahead only when Forward's new errors are exactly the acknowledged list. A different error, an extra one, one that no longer occurs, or the same message on another path refuses it and commits nothing, and the refusal lists `acknowledged_not_found` and `found_not_acknowledged`. It does not override anything else: a login that may not commit some of the changes (`unauthorized`), a moved library head, an existing draft of yours at the path, or the offline check (unless `offline_check` is `skip`) still stops the commit. The dry run says it too ("with N acknowledged new typecheck error(s)"), and names any acknowledged error on a query you did not edit (an importer: it fails on this organization until it upgrades, and `uses_count` checks or dashboards use it). Acknowledge only after the person confirms the errors are fields of a newer release, not typos. The result and its finding say how many errors were acknowledged ("with N acknowledged new typecheck error(s)"), and a limit repeats it. Until the organization upgrades, the committed queries fail on it; the commit does not make them work.

