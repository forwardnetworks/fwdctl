# Reading STIG and other catalog-query results

Read when an `nqe` policy is a catalog query (for example the saved STIG queries under `/Security/STIGs`) and the result is `unknown` with "the rows are not all violations", or when a row count looks too large.

**Measured** on a demo network with a read-only run of the saved query "STIG Findings": the query returned one row for every device and control, not only failures, and each row carries a `violation` column (true, false or empty) and an `Outcome` (for example "Not a Finding", "Not Reviewed"). 166 of the first 200 of 16,779 rows were not violations. Other catalog queries may return failures only, so check the columns of the rows you got.

- **A row is not a violation.** When rows carry a `violation` column, only `true` is a violation. The skill reports `unknown`, not `failed`, when some rows are not, because it reads only the first page (`unknown` is never a pass, and never a failure count either).
- **To get a count,** write or find a query filtered to `violation == true` (`author-nqe-query`), then run it again. Do not quote the row count.
- **Zero rows can mean nothing applies.** A control with no matching device returns no row: say how many devices were in scope.
- **Every control failing is suspicious.** All controls failing on the same device set usually means a wrong filter, an unprocessed snapshot, or one baseline drift, not that many independent findings.
- **Count devices, not rows.** A row is a device and control pair; say how many distinct devices and how many distinct controls.
