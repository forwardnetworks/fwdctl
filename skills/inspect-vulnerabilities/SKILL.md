---
name: inspect-vulnerabilities
description: Finds which CVEs expose the network, which devices a CVE affects, or which CVEs affect a device, from Forward's detection. Use when asked about vulnerabilities, a named CVE or patching.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "security"
  summary: "CVEs and affected devices"
  maturity: "3"
  tools: "snapshots, vulnerabilities, query"
---

# inspect-vulnerabilities

## Intent

Say whether the network is exposed, and to what, from Forward's CVE analysis: which CVEs, how severe, whether they
are known to be exploited, and which devices are affected on what grounds. Forward, not a model, decides whether a
device is vulnerable; this skill reports its verdict and how firm it is.

## Inputs

`network_id`, and one of four views:

- nothing else: the **network** view, the CVEs that may affect devices, worst first;
- `cve_id`: one CVE with a verdict per affected device;
- `device`: one device's CVE findings;
- `view: devices`: **one row per device** with its `internet_addressable` flag, its CVE count, worst severity, exposed and unsettled CVEs, from one call to Forward. Use it to list which devices are internet addressable (with `internet_addressable: true`) instead of reading CVE after CVE. It rejects `cve_id` and `device`. The finding says how many DEVICES are addressable, not only how many CVEs; `addressable_device_names` holds every one. Only devices with at least one CVE matching the filters are listed (a flagged device with none is not), and `min_severity` is applied to each device's per-severity counts.

Optional: `snapshot_id` (default: newest processed, collected), `min_severity`, `known_exploited_only`,
`internet_addressable` (network, cve_id and devices views) and `limit` (default 25 rows shown). See `schema.json`.

**`internet_addressable` is not "open".** It is one yes or no per device, from one arbitrary qualifying interface, so a flagged load balancer can still deny a given VIP. Read it as "can receive some traffic from the internet" and trace the exact flow with `investigate-reachability` before saying a service is reachable (`plan-synthetic-device` reference/exposure.md). With the filter on, every count in the CVE views is already addressable-only; a CVE row with `exposed_devices: 0` has addressable devices whose verdict is unsettled (`unsettled_devices`). The unfiltered single-CVE view shows the first `limit` devices ordered by verdict, not by exposure; its limits give the addressable count across all of them.

## Detection results, and what they let you say

| Forward's result | Meaning | Counts as |
|---|---|---|
| VULNERABLE | confirmed from the device configuration | **exposed** |
| OS_VULNERABLE | the OS version is affected and the CVE does not depend on configuration | **exposed** |
| UNCONFIRMED | affected OS version, depends on configuration Forward could not confirm | **not settled** |
| UNIMPLEMENTED | affected OS version, no configuration analysis for this CVE yet | **not settled** |
| NOT_VULNERABLE | the configuration rules it out | not exposed |

## Procedure

1. Resolve the snapshot; it must be processed. Otherwise `unknown`.
2. Read the CVEs for the chosen view. Severity is the NVD rating of the best CVSS score; a CVE with no score is NONE.
3. Decide:
   - Any device with an **exposed** result (after the filters): **failed**. The finding names the worst CVEs and how many
     devices are exposed.
   - Only **not settled** results: **unknown**. The CVE may or may not apply; the limit says which devices and why.
   - Analysed devices, every result NOT_VULNERABLE: **ok**.
   - Nothing returned at all: **unknown**. An empty list can mean a clean network or a missing CVE index; the skill cannot
     tell them apart. For `device`, a device with no findings at all is unknown too (not found, or no CVE analysis for its
     platform).
4. Order by severity, then known-exploited, then exposed device count, and report how many CVEs the filters left.

## When `internet_addressable` is unavailable

Forward refuses the filter with a reason; the skill answers **unknown** with a distinct reason and next step, never zero and never an error. It reads the organization's effective `DISABLE_FLOW_COMPUTATION` and `ADVANCED_REACHABILITY_ANALYSIS` to tell the cases apart:

| Reason | What it means | Next step |
|---|---|---|
| `INTERNET_NODE_NOT_DEFINED` | the snapshot has no internet node | `plan-synthetic-device` |
| `REACHABILITY_COMPUTATION_DISABLED` | flow computation is disabled: the organization property is true, or (UNKNOWN which, no API returns them) a network-level override or a license without path analysis | `inspect-environment` (features) |
| advanced reachability `UNPROCESSED`, property `DISABLE_FLOW_COMPUTATION` true | **blocked, not never triggered**: Forward does not run the stages after reachability, so starting it gives nothing | `inspect-environment`; an administrator changes the property |
| `UNPROCESSED`, `ON_DEMAND` | never asked for, by design | `edit-advanced-reachability` |
| `UNPROCESSED`, `ASYNC` | Forward should have started it; the cause (a predicted snapshot, a network-level disable, no license) is **UNKNOWN** | `inspect-environment`, then `edit-advanced-reachability` |
| `PROCESSING`, or `FAILED`, `CANCELED`, `TIMED_OUT` | still computing; or a final state, which a reprocess clears | `inspect-snapshots`; `edit-snapshot` (action reprocess) |

`PENDING_ADVANCED_REACHABILITY` is what Forward says whenever the DAG stage has not finished, so by itself it does not rule disabled flow computation out. If the organization properties cannot be read the reason says it is UNKNOWN. See `inspect-environment` reference/features.md.

## Evidence

`state` items: the worst CVEs (id, severity, top score, known exploit, results by count), or one CVE's devices with their
verdicts and whether Forward cites configuration lines, or one device's findings. Each names the operation and snapshot.

## Output

The envelope in `schema/skill-result.schema.json`. `next_actions`: `check-network-compliance` to gate on a policy,
`verify-change` after remediating.

## Next actions

`check-network-compliance`, `verify-change`.

## Running this skill

Give the inputs as one JSON object on stdin: `echo '{"network_id": "<id>"}' | fwdctl run inspect-vulnerabilities`. It needs the
`fwdctl` binary and `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD` in the environment. Read `status`
(`ok`, `failed`, `unknown`, `error`) and the `limits`; `unknown` is never a pass. Inputs are listed above and in `schema.json`.
