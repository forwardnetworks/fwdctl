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

What each input means, and the fields per object and action: `fwdctl run check-network-compliance --help`, or `reference/inputs.md`.

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

**view read** reports checks without evaluating a policy (`check_id`, `statuses`, `catalogue`, `limit`, `offset`; the fields and status rules are in `reference/inputs.md`, printed by `fwdctl run check-network-compliance --help`). **ERROR and TIMEOUT are not policy failures: they mean Forward produced no verdict, so they count as not evaluated, and when they are all there is the result is unknown.**

## Evidence

`policy` items for checks (name, status, violations) and `nqe` items for queries (violation
count, scope size, a bounded sample of rows). Truncation is reported in `limits`.

## Output

The envelope in `schema/skill-result.schema.json`.

## Next actions

`verify-change` to test a fix, `investigate-reachability` for a violated reachability policy.

## Running this skill

Run `fwdctl run check-network-compliance --help` for the inputs and an example, then give them as one JSON object on stdin: `fwdctl run check-network-compliance < input.json`. Read `status` (`ok`, `failed`, `unknown`, `error`) and the `limits`; `unknown` is never a pass.

## Reference

Read the file you need, when you need it (where you cannot read files: `fwdctl describe check-network-compliance reference/<file>`).

- [reference/stig-results.md](reference/stig-results.md): reading STIG and other catalog-query results (rows that are not violations, zero rows, counting devices). Read when an `nqe` result says the rows are not all violations.
- [reference/baseline.md](reference/baseline.md): what to look at in a general configuration review (management, control and data plane), as prompts, not findings. Read when asked for a review with no policy to check against.
- [reference/citations.md](reference/citations.md): the IETF RFCs behind common routing and security findings. Read when a finding should cite a standard.
