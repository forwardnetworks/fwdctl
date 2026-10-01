---
name: plan-investigation
description: Maps a network question to the right skill and states what Forward cannot answer. Use at the start of any network question.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "environment"
  summary: "router: question to skill"
  maturity: "3"
  tools: "investigate-reachability, verify-change, investigate-collection-failure, check-network-compliance, validate-nqe-query, plan-change-review, plan-health-check, plan-what-changed, plan-snapshot-recovery, plan-link-overrides, plan-device-audit, plan-safe-write, plan-troubleshoot-connectivity, plan-incident-triage, plan-security-posture, plan-vulnerability-response, plan-segmentation-check, plan-compliance-audit, plan-maintenance-window, author-nqe-query, plan-report-skill-gap"
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
| Health | "Is the network healthy", a morning check, "is anything wrong" | `plan-health-check` |
| Authoring | "Write or fix an NQE query", a custom question, "save this query" | `author-nqe-query` |
| Feedback | A skill result was wrong or missing something, a skill gave a wrong answer, or I had to hand-write a query (DOGFOOD-TEMP) | `plan-report-skill-gap` |

## Which skill, by symptom

| Symptom or question | Reach for |
|---|---|
| "Which networks do I have", list the networks, "what is the id of network X" (no network id yet) | `inspect-networks` |
| "Can A reach B", "why is this dropped", a path question | `investigate-reachability`; when the endpoints are names or you do not have addresses, find them first with `inspect-inventory` (kind hosts or devices) |
| Internet-bound path | `investigate-reachability` with destination 8.8.8.8; source "internet" for inbound |
| "Did this change work", "is it safe to push" | `verify-change` |
| "What does this change touch", "how big is it", the extent of a change, which areas differ, which subnets lose connectivity | `verify-change` with `view: impact` |
| A device is missing, a snapshot looks incomplete, a collection failed; which devices failed collection (refused, timed out, authentication), a parser failure, a slow or longest collection, a collection log, unmodelled neighbours | `investigate-collection-failure` (view devices, slow, logs or neighbors) |
| "Are we compliant", does the network comply with a rule, "does anything violate X" | `check-network-compliance` |
| "What version is this", "which features does this Forward have", is a feature or preview flag enabled, what value a property has | `inspect-environment` |
| "Which snapshot should I use", "is this snapshot complete", "which are predictions" | `inspect-snapshots` |
| "Is anything unhealthy now", CPU, memory, utilization, errors, packet loss | `inspect-performance` |
| "Is collection running or healthy", "is the collector connected" | `inspect-collection` (view status) |
| "What is collected", "is device X switched off for collection" | `inspect-collection` (view config) |
| A path ends at the edge, "is the internet modelled", links added by hand, the connections of an L3 VPN or L2 VPN | `inspect-topology` (kind external) |
| "When did this check start failing", "is this new or old" | `inspect-history` |
| "Which checks exist or fail", "how many checks fail or error", "why is this check in ERROR", "what did this check find", "what checks does Forward offer" | `check-network-compliance` (view read) |
| "What does this change set do", "is it ready" | `verify-change` with `view: describe` |
| Label a snapshot | `edit-snapshot-note` (dry run first) |
| Add a policy check, turn an authored query into a check, or switch a check off | `edit-checks` (dry run first) |
| "What if we made this change": stage or build a change set from CLI lines or BGP advertisements the caller supplies, and predict it | `edit-change-set` (dry run first), then `verify-change` (`view: describe`, then the default view) |
| "Collect now", cancel a running collection | `edit-collection` (dry run first) |
| Reprocess a snapshot: it failed to process, or its answers are stale after an upgrade | `edit-snapshot-reprocess` (dry run first) |
| Internet exposure (internet_addressable) is unavailable, or a snapshot shows advanced reachability UNPROCESSED: start the analysis for that snapshot | `edit-advanced-reachability` (dry run first) |
| Save an authored query to the library, or remove a saved one | `edit-nqe-query` (dry run first) |
| Tag devices or take a tag off them | `edit-device-tags` (dry run first) |
| Try a collection change away from production (a temporary workspace network, endpoints added to it, delete it afterwards) | `edit-workspace` (dry run first) |
| What can this login do, why was I refused (403, permission denied), who has access, which users or groups exist | `inspect-access` |
| Create or disable a user, make someone org admin, give a user or group a role on a network, define an access group | `edit-access` (dry run first) |
| Change an organization-wide Forward setting (a property), or list what is configurable and how risky each is | `edit-org-property` (dry run first) |
| Try an extra OID, or a different profile, on an endpoint (create an SNMP profile as a copy plus OIDs, repoint endpoints, delete the copy) | `edit-endpoint-profile` (dry run first) |
| Model a leased line or provider L2 circuit between two edge ports (a WAN circuit) | `edit-wan-circuit` (dry run first) |
| Where should the internet attach or egress, why a trace dead-ends at the edge, which default route points at an unowned next hop or upstream | `inspect-edge` (`view: exits`, the default) |
| Show BGP neighbors or peers, the upstream's AS, session state, what a device advertises to a peer (advertised, received prefixes) | `inspect-bgp-neighbors` |
| Who owns this IP address: which device, interface or VRF has it, or whether it is inside a connected subnet | `inspect-inventory` (`kind: ip_owner`, `ips`) |
| Which devices carry public IPv4 addresses on their own interfaces (management, loopback, firewall sides), by role, device type or VRF; what to review before excluding prefixes from the internet node or when a device is flagged internet addressable | `inspect-edge` (`view: public_addresses`) |
| Is there already a saved NQE query for a question; browse the NQE library: list its top-level folders or directories and the queries in one | `find-nqe-query` (`list: true`) |
| Which internal devices are good sources to trace from; choose a source that is not blackholed before tracing to an external or synthetic destination | `inspect-edge` (`view: trace_sources`, `target_ip`) |
| Keep public space the network routes internally off the internet node (its excluded subnets) | `edit-internet-exclusions` (dry run first) |
| Drive a synthetic node (internet, intranet, L3 VPN, L2 VPN, adjacent network) from a saved NQE query, or take it off | `edit-synthetic-query` (dry run first) |
| Add a link Forward missed, or ignore a wrong one, in a snapshot's topology | `edit-link-overrides` (dry run first) |
| What a device connects to, which sites, tags or aliases exist | `inspect-topology` |
| Counting or listing devices, interfaces, VLANs, VRFs, hosts, cloud resources ("how many", "list all", "per vendor") | `inspect-inventory` |
| The literal config text or a "show" output of one device ("what does the config say", "grep the config") | `inspect-device-files` |
| "Which devices' config files changed between snapshots", what lines were added or removed | `compare-device-config` |
| "Which link overrides differ between two snapshots", which manual links one snapshot has and another lacks | `inspect-topology` (`kind: link_overrides`, `compare_to_snapshot_id`) |
| "Which CVEs affect us", "is device X vulnerable to CVE-Y" | `inspect-vulnerabilities` |
| Which devices are internet addressable, or why internet exposure is unavailable: disabled, blocked or never triggered | `inspect-vulnerabilities` (`internet_addressable`) |
| "What changed in X between snapshots" for a kind of data: which rows of a saved query changed, were added or removed | `find-nqe-query`, then `compare-nqe-results` |
| Anything not covered above (custom config questions) | write an NQE query (`author-nqe-query`) and check it with `validate-nqe-query` |
| NQE syntax or data-model questions | `author-nqe-query` |
| "Is this NQE query valid", checking a query without Forward | `fwdctl nqe lint` (offline syntax, deprecations, field names), then `validate-nqe-query` for the type check |

Prefer a dedicated skill to a hand-written query when one exists.

## Writing: always through `plan-safe-write`

The `edit-*` skills change Forward's own data, never a device. Read `plan-safe-write` before the first one: read first, run the plan (no `apply`), show the exact change and its undo, wait for the person's approval of that plan, apply once, and check that `status` is `ok`. A tool-calling harness sees each as a `<name>_plan` tool (cannot write) and a `<name>` tool (behind an approval gate).

DOGFOOD-TEMP: when a skill gap bites (a wrong or misleading result, something missing that you had to hand-write, a wrong route), finish the task, then offer to file a redacted GitHub issue: read `plan-report-skill-gap`.

## Trust what a result says about itself

Every skill answers `ok`, `failed`, `unknown` or `error`. `unknown` means the data could not decide: an
empty result, an unprocessed or predicted snapshot, an incomplete comparison. It is never a pass. Read the
`limits` before you rely on a result, and say what was not measured.

## Out of scope: say so, do not improvise

Forward as a data source cannot do these, and no skill should be bent to pretend it can:

1. Changes to devices, or any write to Forward beyond the sixteen skills that write (`edit-snapshot-note`, `edit-snapshot-reprocess`, `edit-checks`, `edit-change-set`, `edit-collection`, `edit-nqe-query`, `edit-device-tags`, `edit-link-overrides`, `edit-synthetic-query`, `edit-wan-circuit`, `edit-internet-exclusions`, `edit-advanced-reachability`, `edit-endpoint-profile`, `edit-workspace`, `edit-org-property`, `edit-access`): push, apply, configure, restart, roll back. Those sixteen show a dry run first and change only Forward's own data.
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
