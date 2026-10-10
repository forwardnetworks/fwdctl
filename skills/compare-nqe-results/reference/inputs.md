# compare-nqe-results: inputs

`network_id`, `before_snapshot_id`, `after_snapshot_id`, and `query_id` of a query **committed to the NQE library**
(use `find-nqe-query` to get one). Forward cannot diff ad-hoc query text. Optional: `commit_id`, `limit` (default 25,
at most 100).

**Two networks.** Give `after_network_id` and `before_snapshot_id` is read in `network_id` while `after_snapshot_id` is read in `after_network_id` (a seed and a lab, production and a copy). Forward's own diff works inside one network, so the saved query is run on both and the rows are diffed here: `key` names the result columns that identify a row (default the whole row, so only rows on one side are found), `ignore` drops columns that are expected to differ (names, ids, addresses) from both sides first. The result lists rows only in A, only in B, and, with a `key`, rows whose other fields differ (field, A value, B value). Each side is read up to 200,000 rows; a longer result is said to be cut. `key` and `ignore` are refused without `after_network_id`.
