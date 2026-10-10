# edit-nqe-query: inputs

`path` (the library path, starting with `/`), and either `source` or `delete: true`. Optional: `message` (commit title) and `apply` (default
false).

**Several queries in one commit.** Instead of `path` and `source`, give `changes`: a list of `{path, source}` (up to 25; modules that import each other change together), `message` (the
commit title), optional `basis_commit_id` (the library head the edits were made against: the plan and the apply refuse if the head is another commit, and say so) and `typecheck: true`
(optionally with `snapshot_id`). `offline_check` picks the schema of the offline check: `embedded` (default, this build's), `org` (the organization's live schema, like `fwdctl nqe lint --org`: needed for a query that uses a field newer than this build; the result says which schema was used) or `skip` (only with `changes` and `typecheck: true`, so Forward's own typecheck is the gate). The dry run lints every source offline, compares each with what is committed, writes nothing and says so when dependents were **not** typechecked. With
`typecheck: true` it **stages the changes as drafts in your workspace, has Forward type every changed query and every query that imports one (and counts the checks and dashboards that
use them), then discards the drafts** (Forward's discard drops them: the skill refuses to stage when you already have an uncommitted draft at one of the paths, since cleaning up would drop
it, and says when a discard failed so you can look at the NQE editor). Any error Forward reports in the changed queries or their importers (it does not say which already existed at head), or a change this login may not commit, is **failed** and nothing is committed. `apply: true` re-reads the head, commits all
paths as ONE commit, reads each path back at the new head, and returns `previous_commit_id` and `commit_id` in the evidence (the commit call itself returns none; the head is read
afterwards). Forward has no optimistic-concurrency check, so a commit landing between the head check and the commit is not caught. This form edits and adds; it does not delete. A new
query needs its enclosing directories: without `create_directory: true` the plan is **failed** and names the missing ones (nothing is changed); with it the plan lists each directory as its own
change, the apply adds them parents first and commits them with the queries in them, and a failure while staging discards the directory drafts again (deepest first) along with the query drafts.
To load a whole folder tree, `fwdctl nqe pack DIR` builds this input from it (the counterpart of `fwdctl nqe export`); the skill itself never reads the filesystem.
