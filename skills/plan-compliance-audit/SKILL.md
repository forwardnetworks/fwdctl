---
name: plan-compliance-audit
description: Sequences the skills that audit policy: checks, findings, new rules, history. Use when asked about compliance or audit evidence.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "compliance"
  summary: "policy audit evidence"
  maturity: "2"
  tools: "inspect-snapshots, check-network-compliance, inspect-history, find-nqe-query, author-nqe-query, validate-nqe-query, edit-checks"
---

# plan-compliance-audit

A procedure, not a skill that runs. A passing check proves only what the check tests.

## Steps

1. **Data.** `inspect-snapshots`: the snapshot the audit is about, and its age.
2. **Existing checks.** `check-network-compliance` with `view: read`: which checks exist, which fail, and what each failing one found. The catalogue (`catalogue`) lists the predefined checks Forward offers.
3. **Map the policy to checks.** For each rule the person names: is there a check for it? If not, look for a saved query (`find-nqe-query`), else write one (`author-nqe-query`, then `fwdctl nqe lint`, then `validate-nqe-query`) and evaluate it with `check-network-compliance` and `nqe`.
4. **Keep a rule.** To make a rule a standing check, `edit-checks` (follow `plan-safe-write`); a check is local to its snapshot unless persistent.
5. **How long?** `inspect-history` for each failing check that matters: when it started failing.

## Freedom

How much room each step gives (levels are defined in `plan-investigation`):

- **Fixed** (steps 4): making a rule a standing check is a write: `plan-safe-write`, dry run first.
- **Guided** (steps 1, 2, 5): keep the order; scope the checks and the history to the rules the person named.
- **Open** (steps 3): mapping a rule to a check, a saved query or a new one is judgment; an authored query is still linted and validated before it is trusted.

## The minimum

Before you answer, take at least: the existing checks and their status (step 2) and, for a rule with no check, a statement that it is unknown or an NQE check for it (step 3). Stop earlier only when the evidence already answers the question, and say so.

## Answering

A table of rule, check, status, snapshot. A rule with no check, or a check that returned no rows because nothing matched its scope, is **unknown**, not compliant. Do not claim compliance with a framework Forward does not test (a check set is not a certification).

## Worked example

[reference/example.md](reference/example.md) shows a good run end to end (illustrative, results shortened). Read it when you are unsure what a finished answer looks like; where you cannot read files, run `fwdctl describe plan-compliance-audit reference/example.md`.
