---
name: check-network-compliance
description: Decides whether the network satisfies a policy using Forward's checks and NQE violation queries; view read lists existing checks. Use when asked if the network is compliant or breaks a rule.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "compliance"
  summary: "policy checks and violations"
  maturity: "3"
  tools: "snapshots, checks, query"
---

# check-network-compliance

## Intent

Evaluate a policy against a snapshot and return **which things violate it**, with the rows.
A pass must mean "evaluated, and nothing violates", never "nothing was evaluated".

## Inputs

`network_id`. Optional `snapshot_id` (default: newest processed, non-predicted snapshot).
At least one policy source:

- `use_checks: true` (optionally `check_ids`): Forward's own checks. Forward evaluated them,
  so its PASS is Forward's own verdict for what the check tests. An isolation or reachability check can pass vacuously (no valid path
  ever existed); `fwdctl describe plan-segmentation-check reference/check-vacuity.md` says how to tell.
- `nqe`: a violations query (`query` or `query_id`, `parameters`) where **each row is a
  violation**, plus a **scope query** (`scope_query` or `scope_query_id`) whose rows are the
  things the policy applies to. The scope is what separates "none violate" from "nothing was
  in scope".

See `schema.json`.

## Procedure

1. Resolve the snapshot; it must be processed. Otherwise `unknown`.
2. Checks (if asked): read every enabled check on the snapshot. PASS is compliant; FAIL is a
   violation (with its violation count); NONE, PROCESSING, ERROR, TIMEOUT and
   REQUIRES_ADDITIONAL_SNAPSHOT_PROCESSING mean **not evaluated**, which is unknown. Disabled
   checks are listed as a limit, not counted as passing.
3. NQE (if asked): run the violations query and the scope query.
   - Any violation rows: violation.
   - Zero violations and a non-empty scope: compliant.
   - Zero violations and an empty or missing scope: **not evaluated**. Zero rows is not a pass.
   - On a predicted snapshot, zero violation rows is also not evaluated: some signals come back
     empty there whatever the network state.
4. Decide across all policies: any violation is **failed**; otherwise any not-evaluated policy
   is **unknown**; otherwise **ok**.

**view read.** Reports checks without evaluating a policy. Give `snapshot_id` (default newest processed), and optionally `check_id` (one check with Forward's diagnosis), `statuses` (only these), `catalogue` (list predefined checks instead), `limit`, `offset`. Failing checks come first, then checks with no verdict. **failed** when an enabled check is FAIL (the finding also says how many are not evaluated); **ERROR and TIMEOUT are not failures of policy**: they mean Forward produced no verdict, so they are counted apart as not evaluated, and when they are all there is, the result is **unknown**; **ok** when checks were read and none is failing or not evaluated; **unknown** also when the list is empty (an empty list is not health) or a check has no result. Reading one check with `check_id` that is ERROR or TIMEOUT says there is no verdict and gives Forward's recorded reason (the diagnosis summary; some ERROR paths store none, and the list read never carries one), and says when it looks like a pre/post diff that needs a reference snapshot. Giving `check_id`, `statuses` or `catalogue` without `view` selects read. Creating or deactivating a check is `edit-checks`.

## Evidence

`policy` items for checks (name, status, violations) and `nqe` items for queries (violation
count, scope size, a bounded sample of rows). Truncation is reported in `limits`.

## Output

The envelope in `schema/skill-result.schema.json`.

## Next actions

`verify-change` to test a fix, `investigate-reachability` for a violated reachability policy.

## Running this skill

Give the inputs as one JSON object on stdin: `echo '{"network_id": "<id>"}' | fwdctl run check-network-compliance`. It needs the
`fwdctl` binary and `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD` in the environment. Read `status`
(`ok`, `failed`, `unknown`, `error`) and the `limits`; `unknown` is never a pass. Inputs are listed above and in `schema.json`.

## Reference

Read the file you need, when you need it (where you cannot read files: `fwdctl describe check-network-compliance reference/<file>`).

- [reference/stig-results.md](reference/stig-results.md): reading STIG and other catalog-query results (rows that are not violations, zero rows, counting devices). Read when an `nqe` result says the rows are not all violations.
