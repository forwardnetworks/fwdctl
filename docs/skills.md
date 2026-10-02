# The skills

The skills are grouped by what Forward does. A **playbook** (procedure) says which skills to call for a task and in what order; a **read skill** answers one question; an **edit skill** changes Forward's own data.

### Path analysis and troubleshooting

Why can't A reach B, what is down, what changed.

- **Playbook** `plan-troubleshoot-connectivity`: Why A cannot reach B: fresh data, path search, the failing hop, what changed.
- **Playbook** `plan-incident-triage`: Where to start when something is down and the cause is unknown.
- **Playbook** `plan-what-changed`: What changed and when: snapshots, history, config and state differences, what a change did.
- **Playbook** `plan-synthetic-device`: Model an uncollected segment (the internet, an MPLS or provider core, a partner network, a circuit) as a synthetic device: choose the type, build the connections from an NQE query, preview, attach, verify paths cross.
- **Playbook** `plan-link-overrides`: Link overrides (manual or suppressed links) missing, drifted or not appearing: compare snapshots, verify ports and topology, apply the smallest fix safely, say what to expect on the next snapshot.
- **Playbook** `plan-snapshot-recovery`: Why a snapshot or collection is bad and how to recover: reprocess or collect again.
- `investigate-reachability`: Is this flow delivered, and where does it first fail?
- `inspect-topology`: What is connected to what, which locations, tags and aliases exist, how is the edge modelled (kind external), and which link overrides does a snapshot have, or have gained and lost against an earlier one (kind link_overrides, `compare_to_snapshot_id`)?
- `inspect-history`: When did a check start failing: its status across the last collected snapshots and where it last changed?
- `compare-device-config`: Which devices' configuration files changed between two snapshots, and which lines were added or removed?
- `inspect-device-files`: What does a device's raw collected config or command output say? List the files, read a window, or search with a regular expression (secrets redacted).
- `investigate-collection-failure`: Why could Forward not collect or model some devices?

### Security

Posture, vulnerabilities, internet exposure, segmentation.

- **Playbook** `plan-security-posture`: Security review: failing checks, CVEs, internet exposure, risky configuration.
- **Playbook** `plan-vulnerability-response`: A CVE or advisory: who is affected, how exposed, what to patch first.
- **Playbook** `plan-segmentation-check`: Verify zone isolation: flows that must be blocked and that must work, before and after a change.
- `inspect-vulnerabilities`: Which CVEs expose the network, which devices are affected by one, and what is wrong with a device?
- `check-network-compliance`: Does the network satisfy a policy (Forward checks and/or an NQE query)? view read: which checks exist and fail, what one found, which predefined checks exist.
- `investigate-reachability`: Is this flow delivered, and where does it first fail?

### Change

Is it safe, did it work, bracket a maintenance window.

- **Playbook** `plan-change-review`: The order of skills that decide whether a change is safe
- **Playbook** `plan-maintenance-window`: Bracket a change with a labelled baseline, a collection after, and a verified comparison.
- `verify-change`: Did a change (a Predict change set, or two snapshots) do what was intended, and break nothing? view impact: how far did it reach, judged not at all. view describe: what does a Predict change set edit, has it been predicted, and which checks got worse?
- `edit-change-set` *(writes, dry run first)*: Stage a change set from commands you supply, validate it, and optionally predict it. Touches no device.

### Audit and compliance

Policy checks, evidence, a device or a site under review.

- **Playbook** `plan-compliance-audit`: Audit against policy: checks, missing rules as NQE, evidence, history.
- **Playbook** `plan-device-audit`: Everything known about one device or a group: connections, config, exposure, load, history, tags.
- `check-network-compliance`: Does the network satisfy a policy (Forward checks and/or an NQE query)? view read: which checks exist and fail, what one found, which predefined checks exist.
- `inspect-inventory`: What is in the network: counts and vendors, and the devices, interfaces (with SVI addresses and VRFs), VLANs, VRFs, hosts, routes (with a per-VRF default-route fact), OSPF neighbors, and cloud accounts, VPCs, subnets and instances, with filters and paging? kind ip_owner: which modelled interface, SVI or FHRP address owns an IPv4 address, or which connected subnet it falls in.
- `edit-checks` *(writes, dry run first)*: Create a check (local to its snapshot unless persistent) or deactivate one.

### Health and collection

Is the data current, is collection working, is anything stressed.

- **Playbook** `plan-health-check`: The order of skills that decide whether the network is healthy
- `inspect-snapshots`: Which snapshots exist, which is the newest one worth reading, which are predictions, and how complete is one?
- `inspect-collection`: Is collection running or healthy now (view status), and what is Forward configured to collect, with credentials never shown (view config)?
- `inspect-performance`: Which devices and interfaces are unhealthy right now (CPU, memory, utilization, errors, loss), and what is their history?
- `inspect-environment`: Which Forward build, organization, login and vulnerability-index age is this, which features has the client seen, and which features (advanced reachability, flow computation, Predict, NQE fields) are on, what is their default and where is each set (`features`)?
- `edit-collection` *(writes, dry run first)*: Start or stop a collection; refuses while one runs or the collector is down.
- `edit-snapshot-reprocess` *(writes, dry run first)*: Recompute a snapshot's derived data from what it collected (failed or stale after an upgrade).
- `edit-advanced-reachability` *(writes, dry run first)*: Start advanced reachability for one processed snapshot that never had it (the analysis internet exposure is read from); asynchronous and compute-heavy.
- `edit-snapshot-note` *(writes, dry run first)*: Put a note on a snapshot, for example "before change X".

### Inventory and topology

What is in the network and how it connects.

- `inspect-networks`: Which Forward networks can this login see, and what is the id of the one I mean?
- `inspect-inventory`: What is in the network: counts and vendors, and the devices, interfaces (with SVI addresses and VRFs), VLANs, VRFs, hosts, routes (with a per-VRF default-route fact), OSPF neighbors, and cloud accounts, VPCs, subnets and instances, with filters and paging? kind ip_owner: which modelled interface, SVI or FHRP address owns an IPv4 address, or which connected subnet it falls in.
- `inspect-topology`: What is connected to what, which locations, tags and aliases exist, how is the edge modelled (kind external), and which link overrides does a snapshot have, or have gained and lost against an earlier one (kind link_overrides, `compare_to_snapshot_id`)?
- `edit-endpoint-profile` *(writes, dry run first)*: Create an SNMP endpoint profile as a copy of another plus extra OIDs, repoint endpoints at a profile, or delete one; profiles are organization-wide, only assigned endpoints use them; undo is reassign then delete.
- `inspect-access`: What can this login do and why was an operation refused (a 403 explained from Forward's role definition, with who can resolve it), which users and access control groups exist and what they hold.
- `edit-access` *(writes, dry run first)*: Create or disable a user, grant or revoke organization admin, set a network role for a user or group, define access control groups; undo recorded; refuses what Forward would refuse, with the reason.
- `edit-org-property` *(writes, dry run first)*: List Forward's organization properties with value, who may change them and a risk (safe, caution, dangerous), or set or clear one; org-wide; dangerous ones need a second confirmation; undo restores the recorded value.
- `edit-workspace` *(writes, dry run first)*: Create a temporary workspace network under a production one, add endpoints to a workspace, or delete one; never touches a non-workspace network, never handles a secret.
- `edit-device-tags` *(writes, dry run first)*: Put existing tags on devices or take them off; only the pairs that differ.
- `inspect-edge`: Find where the network meets what it cannot collect: IPv4 default-route next hops no modelled device owns (exit candidates), with the egress interface and local address, by VRF, and which synthetic node (if any) claims each uplink (`claimed_by`, read from the nodes as configured now: for the newest snapshot only, unknown for an older one; an unclaimed likely internet edge is a Missing Peer). view public_addresses: which collected interfaces carry public IPv4 addresses, with device type, VRF, an inferred role (management, loopback, a firewall's customer-facing side) and a candidate exclude list per role. view trace_sources: pick good devices to start a trace from (each candidate is traced to a control address and the ones that arrive are ranked).
- `inspect-bgp-neighbors`: BGP neighbors with peer AS, state, prefix counts, whether the peer is a modelled device and the routes advertised to it.
- `edit-internet-exclusions` *(writes, dry run first)*: Add, remove or replace the internet node's excluded subnets (public space routed internally that must not be treated as the internet); shows the whole list before and after.
- `edit-wan-circuit` *(writes, dry run first)*: Create, replace or delete a WAN circuit (a point-to-point L2 link a provider carries between two edge ports); previews before and after.
- `edit-synthetic-query` *(writes, dry run first)*: Drive a synthetic node (internet, intranet, L3 VPN, L2 VPN, adjacent network) from a saved NQE query, or take it off; previews what the query generates first.
- `edit-link-overrides` *(writes, dry run first)*: Add a link Forward missed, or suppress a wrong one, in one snapshot's topology.

### NQE (custom questions)

Find, write, check, run, compare and keep a query.

- `find-nqe-query`: Does the saved NQE library (2,500+ queries on fwd.app) already have a query for this?
- `author-nqe-query`: (procedure) From a question to a saved, checked NQE query: find, write on real field names, lint, run, keep
- `validate-nqe-query`: Does this NQE query compile and run, and what did it return?
- `compare-nqe-results`: Which rows of a saved NQE query were added, removed or changed between two snapshots?
- `edit-nqe-query` *(writes, dry run first)*: Save an authored NQE query to the organization's library and commit it, or remove one.

### Router and protocol

How to choose, and how to write safely.

- **Playbook** `plan-investigation`: Which skill for which symptom, and what is out of scope
- **Playbook** `plan-safe-write`: The protocol every edit skill follows: plan, show, approval, apply once, verify, undo.
- **Playbook** `plan-report-skill-gap`: When a skill result was wrong or missing something, offer a redacted GitHub issue (DOGFOOD-TEMP).
  - `fwdctl redact-check [--file FILE | -] [--deny w]...` (DOGFOOD-TEMP): offline scan of the draft for IPs, hostnames, device-style names, URLs, secrets, emails, ids, home paths and the deny words (saved login, OS user, machine name, `redact_deny`, `FWDCTL_REDACT_DENY`, `--deny`); masked JSON findings (exit 1) and possible customer-name warnings (exit 2); exit 0 only when clean. `fwdctl dogfood-note --ref SLUG` (DOGFOOD-TEMP) saves full private details to a local 0600 file, never uploaded.

**Skills that write** are marked in the table. They change Forward's own data (a note, a check, a draft change set, a collection),
never a device. Each is a **dry run unless you pass `"apply": true`**: the dry run returns `mode: dry_run` and the exact `changes`
it would make, and an applied result lists what was done with the value it replaced and how to undo it. Check the result's `limits`;
a few changes (deactivating a check, a prediction, stopping a collection) cannot be undone through the API and say so.

Every skill returns the same envelope (`result/`): `status` is `ok`, `failed`, `unknown` or `error`, with
evidence, `limits` (what was not measured) and `next_actions`. **`unknown` is a first-class answer**: an empty
result, an unprocessed or predicted snapshot, or an incomplete comparison is never reported as a pass.

## How an AI gets the right context

Forward Skills applies Anthropic's [context engineering](https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents) guidance: give the model the smallest set of high-signal context, and let it fetch the rest when it needs it. By the article's headings:

| Technique | In Forward Skills |
|---|---|
| System prompts at the right altitude | five short agent rules (`fwdctl docs agents`) and the router `plan-investigation`; hard rules where they matter (unknown is never a pass, never improvise a write) |
| Tools with minimal overlap | one skill per question: 19 read skills and 10 edit skills (listed above), one verb rule for names, overlapping skills merged behind aliases |
| Examples | worked examples in the key playbooks; trigger evaluations for every skill |
| Just-in-time retrieval | a skill reads Forward when asked and returns a small, paged result with `limits`; the model never holds the network |
| Progressive disclosure | the description always; the skill or playbook body when it triggers; reference files when needed; 15 playbooks (troubleshooting, security, change, audit, health, authoring) that fix the order of a task; a small core tool set plus `find_skill` in the Copilot |
| Compaction, note-taking, sub-agents | each result is structured notes (`finding`, `context`, `limits`, `next_actions`); the rules ask for one line per result and sub-agents for independent branches (guidance: Forward Skills calls no model) |
| Evaluation | per-skill trigger evals, a 56-query routing eval, a write-protocol eval, structure tests |

```mermaid
flowchart LR
  Q([A question or task]) --> SP[System prompt at the right altitude<br/>agent rules, router]
  SP -->|a task| PB[Progressive disclosure<br/>playbook: the order and the stop]
  SP -->|one fact| T
  PB --> T[Tool: one skill<br/>just-in-time read of Forward]
  T --> R[Small, structured result<br/>status, evidence, limits, next_actions]
  R -->|next_actions| PB
  R --> A([Answer, with what was not measured])
  PB -.->|a change in Forward| W[plan-safe-write<br/>plan, show, approve, apply once, verify, undo]
  W --> E[edit-* skill<br/>dry run unless apply]
  E --> R
  LH[Long-horizon: notes, compaction, sub-agents] -.keeps the window small.-> PB
  EV[Evals: routing, protocol, per-skill] -.checks.-> SP
  EV -.checks.-> W
```

The architecture page, with the full list of skills and playbooks, how each consumer gets them (Claude Code, Codex, the Copilot, Slack, MCP), and a worked flow, is the **[reference architecture](architecture.md)**; the binary prints it with `fwdctl docs architecture`.

## Retired names (aliases)

These names still run (and `fwdctl describe` them), each as a view of the skill that absorbed it: `review-change-set` = `verify-change` view describe; `compare-link-overrides` = `inspect-topology` kind link_overrides with `compare_to_snapshot_id` (its `before_snapshot_id` and `after_snapshot_id` are renamed); `find-ip-owner` = `inspect-inventory` kind ip_owner; `find-public-addresses` and `find-trace-source` = `inspect-edge` views public_addresses and trace_sources; `plan-author-query` = `author-nqe-query`; and, earlier, `analyze-blast-radius` = `verify-change` view impact, `inspect-checks` = `check-network-compliance` view read, `inspect-collection-status` and `inspect-collection-config` = `inspect-collection` views status and config, `inspect-external-connectivity` = `inspect-topology` kind external, plus the renames `annotate-snapshot`, `manage-checks`, `draft-change-set`, `start-collection`, `investigate-vulnerabilities` and `list-networks`.
