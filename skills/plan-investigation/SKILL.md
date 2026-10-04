---
name: plan-investigation
description: Maps a network question to the right skill and states what Forward cannot answer. Use at the start of any network question.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "environment"
  summary: "router: question to skill"
  maturity: "3"
  tools: "investigate-reachability, verify-change, investigate-collection-failure, check-network-compliance, validate-nqe-query, plan-change-review, plan-what-changed, plan-snapshot-recovery, plan-link-overrides, plan-device-audit, plan-safe-write, plan-troubleshoot-connectivity, plan-incident-triage, plan-security-posture, plan-vulnerability-response, plan-segmentation-check, plan-compliance-audit, plan-maintenance-window, author-nqe-query"
---

# plan-investigation

Forward collects a network through read-only APIs and CLI commands and builds a vendor-neutral digital
twin: switches, routers, firewalls, load balancers, on premises and in the cloud. Every skill reads that
twin. It is a **data source**: it answers what the network looks like at a collected moment, and nothing
more.

## Investigate like an engineer

- Be systematic. Form a hypothesis from the context, then gather targeted evidence with a skill.
- Verify what matters. For a security or connectivity claim, corroborate with a second source.
- Know when to stop. When the evidence you hold answers the question, answer it. Do not call skills you do
  not need.
- Return conclusions and the evidence behind them, not raw data dumps.
- Keep the context small. After each result, note in one line what it showed, the snapshot, and what is still unknown, and carry that forward instead of the raw result. Ask for the page or filter you need (`limit`, `view`, one device) rather than everything. In a long investigation keep a short running findings list; where you can delegate, give each independent branch (one device, one flow, one CVE) to a sub-agent and have it return only its conclusion with the snapshot and the evidence it rests on.

## Playbooks: when the question is a task, not a single fact

A playbook is a procedure (it does not run): it names the skills to call, their order, and when to stop. Read it with `describe` and follow it. They are grouped by what Forward does.

| Area | The question looks like | Read |
|---|---|---|
| Change | "Is it safe to make this change", "will it break anything" | `plan-change-review` |
| Change | A maintenance window, a cutover, a migration: "prove the change worked" | `plan-maintenance-window` |
| Change | **Any** request to change something in Forward (an `edit-*` skill) | `plan-safe-write`, before the first call |
| Troubleshooting | "Why can't A reach B", a dropped flow, an unreachable site, "is there a path" | `plan-troubleshoot-connectivity` |
| Troubleshooting | "Something is down", "users are complaining", an incident with an unknown cause | `plan-incident-triage` |
| Troubleshooting | A path dead-ends at the edge: model the internet, a provider or MPLS core, a partner network or a circuit as a synthetic device | `plan-synthetic-device` |
| Troubleshooting | Link overrides (manually added or suppressed links) are missing, drifted between snapshots, or did not appear; how to apply or keep them | `plan-link-overrides` |
| Troubleshooting | A snapshot failed, devices are missing, answers look stale, "collect again" | `plan-snapshot-recovery` |
| Troubleshooting | "What changed", "since when", "did that change cause this" | `plan-what-changed` |
| Security | "Is the network secure or exposed", a security review, "what can an attacker reach" | `plan-security-posture` |
| Security | A CVE or advisory, "is device X vulnerable", "what do we patch first" | `plan-vulnerability-response` |
| Security | "Are zones isolated", "does this rule set enforce segmentation" | `plan-segmentation-check` |
| Audit | "Do we comply with policy or standard X", evidence for an audit, "add a standing rule" | `plan-compliance-audit` |
| Audit | "Tell me about this device", everything you know about a device, "audit this site or tag group" | `plan-device-audit` |
| Health | "Is the network healthy", a morning check, "is anything wrong" | `plan-incident-triage` |
| Authoring | "Write or fix an NQE query", a custom question, "save this query" | `author-nqe-query` |

## Which skill, by symptom

Do not read the whole symptom table first. Run `fwdctl which "<the question in words>"`: it ranks the playbook table above and the symptom table offline and names the skill or playbook with the row that matched. If the best match is a playbook, `describe` it and follow it. If the guess is weak, or the question spans several areas, read the table: `reference/router.md` (`fwdctl describe plan-investigation reference/router.md`). Every runnable skill is named in it.

## Writing: always through `plan-safe-write`

The `edit-*` skills change Forward's own data, never a device. Read `plan-safe-write` before the first one: read first, run the plan (no `apply`), show the exact change and its undo, wait for the person's approval of that plan, apply once, and check that `status` is `ok`. A tool-calling harness sees each as a `<name>_plan` tool (cannot write) and a `<name>` tool (behind an approval gate).


## Trust what a result says about itself

Every skill answers `ok`, `failed`, `unknown` or `error`. `unknown` means the data could not decide: an
empty result, an unprocessed or predicted snapshot, an incomplete comparison. It is never a pass. Read the
`limits` before you rely on a result, and say what was not measured.

## Out of scope: say so, do not improvise

Forward as a data source cannot do these, and no skill should be bent to pretend it can:

1. Changes to devices, or any write to Forward beyond the twenty-two skills that write (`edit-snapshot` (action note), `edit-snapshot` (action reprocess), `edit-checks`, `edit-alias`, `edit-network`, `edit-source`, `edit-platform`, `edit-change-set`, `edit-collection`, `edit-nqe-query`, `edit-device-tags`, `edit-link-overrides`, `edit-synthetic-query`, `edit-wan-circuit`, `edit-internet-exclusions`, `edit-advanced-reachability`, `edit-endpoint-profile`, `edit-workspace`, `edit-org-property`, `edit-access`, `edit-data-file`, `edit-data-connector`): push, apply, configure, restart, roll back. Those eighteen show a dry run first and change only Forward's own data.
2. A timeline finer than the collected snapshots. History is read across the last snapshots (`inspect-history`, `plan-what-changed`) and is only as fine as the collection interval; Forward holds no events between two collections.
3. Platform management (dashboards, licensing, users, permissions, credentials). Collector and collection state can be read and a collection started or stopped; nothing else.
4. Verifying a configuration against an external policy or framework that is not expressed as a Forward check
   or an NQE query.
5. Forecasts and capacity projections.
6. Application-level performance beyond the network.
7. Live monitoring and alerting ("notify when").

Not in the model, only in configuration text: the route-map or prefix-list applied to a BGP neighbor, and configured static routes (only installed ones are modelled). Read them with `inspect-device-files` or NQE over `device.files.config` (`author-nqe-query`, reference/config-patterns.md) and say which config forms were covered.

In scope: current network state and configuration, paths and reachability, vulnerabilities and CVEs on
devices, cloud inventory, network complexity and inventory, and anything NQE can query in the twin. A
request with an in-scope part and an out-of-scope part is answered for the part that is in scope, with the
limit stated.

Ambiguous words: "fix" or "troubleshoot" means find the cause, never apply a change. "Verify" or "check"
means against current state across devices. "Write" or "generate" applied to an NQE query means produce a
query the user runs. "Utilization" and "counters" mean the collected snapshot, not live streams.
