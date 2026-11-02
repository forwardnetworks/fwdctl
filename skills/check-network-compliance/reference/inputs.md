# check-network-compliance: inputs

`network_id`. Optional `snapshot_id` (default: newest processed, non-predicted snapshot).
At least one policy source:

- `use_checks: true` (optionally `check_ids`): Forward's own checks. Forward evaluated them,
  so its PASS is Forward's own verdict for what the check tests. An isolation or reachability check can pass vacuously (no valid path
  ever existed); `fwdctl describe plan-segmentation-check reference/check-vacuity.md` says how to tell.
- `nqe`: a violations query (`query` or `query_id`, `parameters`) where **each row is a
  violation**, plus a **scope query** (`scope_query` or `scope_query_id`) whose rows are the
  things the policy applies to. The scope is what separates "none violate" from "nothing was
  in scope".

## view read

Reports checks without evaluating a policy. Give `snapshot_id` (default newest processed), and optionally `check_id` (one check with Forward's diagnosis), `statuses` (only these), `catalogue` (list predefined checks instead), `limit`, `offset`. Failing checks come first, then checks with no verdict. **failed** when an enabled check is FAIL (the finding also says how many are not evaluated); **ERROR and TIMEOUT are not failures of policy**: they mean Forward produced no verdict, so they are counted apart as not evaluated, and when they are all there is, the result is **unknown**; **ok** when checks were read and none is failing or not evaluated; **unknown** also when the list is empty (an empty list is not health) or a check has no result. Reading one check with `check_id` that is ERROR or TIMEOUT says there is no verdict and gives Forward's recorded reason (the diagnosis summary; some ERROR paths store none, and the list read never carries one), and says when it looks like a pre/post diff that needs a reference snapshot. Giving `check_id`, `statuses` or `catalogue` without `view` selects read. Creating or deactivating a check is `edit-checks`.
