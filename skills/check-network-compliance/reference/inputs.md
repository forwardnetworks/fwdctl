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


