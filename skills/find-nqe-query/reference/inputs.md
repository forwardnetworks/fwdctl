# find-nqe-query: inputs

`network_id` and one of `question` (plain words, to search), `query_id` (a `Q_` id: return that saved query's source) or `path` (the same, by library path), with optional `commit_id` to read it at a library commit instead of the head (the result names the commit; a path is looked up among the head's paths). Optional: `list: true` (browse instead of search: one level of the library as a tree, the directories under `directory`, default `/`, with how many queries are below each and the queries directly in it; use it to learn the top-level folders or whether a directory exists), `directory` (limit the library to a folder), `limit` (default 8,
at most 25).

**What a commit is.** `commit_id` on its own (no `question`, `query_id` or `path`; `head` is the head) says what that library commit last changed and who made it: the queries whose last change it was, plus its author, time
and title from the history of one of them. Forward has no list of library commits, so a commit that no query last changed in is **unknown**, not empty. The author is personal: keep it out of public places.
