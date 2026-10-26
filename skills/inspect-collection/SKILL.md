---
name: inspect-collection
description: Reads collection state without diagnosing: running, what is collected, devices to migrate, discovery, connectivity tests. Use when asked if collection is healthy.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "snapshots"
  summary: "collection state, config, discovery"
  maturity: "3"
  tools: "collector tasks, collections, collectors, classic devices, endpoints, jump servers, proxies"
---

# inspect-collection

## Intent

The read-only side of collection, by the question you are asking: *is it running?* `status`; *what is Forward asked to collect, and what could be added?* `config` (devices, sources, schedules, controller-managed setups) and `discovery`; *do the devices answer?* `connectivity` (stored test results by error); *what is set up the wrong way?* `migration` (classic devices that belong in a controller-managed setup). Why a snapshot is missing devices is `investigate-collection-failure`, not here.

The views in detail. `view: status` is the present: a running collection, the latest tasks, the collector and each device's status. `view: discovery` reads Forward's device discovery without starting anything: the jobs (running or finished, with queued/discovered/inaccessible counts), the discovered devices that are not yet collection devices (by type and vendor, each with why it is not added: type unknown, no credential, no host; `limit` caps the list, default 20) and the scheduled discoveries. `view: connectivity` counts the last connectivity-test result of every classic device by error and phase (the largest group first, each with its device types, up to `limit` device names, the group's total and the range of times its results were saved, `tested_from` and `tested_to`; an authorization group also carries `device_details` for its first 20 devices: the commands the account may not run, the login the test found and the addresses reached. The stored result does not carry the credential that was tried or the privilege level): a failed test is not a failed collection, so errors that never reach a snapshot (a custom-command authorization failure, say) show here. `view: migration` counts the classic devices Forward's UI says "need to be migrated" to a controller-managed setup (ACI, SD-WAN): by category and effective type (the configured type, else the one the last connectivity test discovered) with the controllers and devices per controller; a standalone IOS-XE router is not counted, and a device with no test result yet cannot be classified (it is counted in `devices_without_test_result`). `view: config` is what Forward is told to collect, so an absence of data can be explained. `investigate-collection-failure` stays separate: it classifies why a finished snapshot is missing devices.

## Inputs

What each input means, and the fields per object and action: `fwdctl run inspect-collection --help`, or `reference/inputs.md`.

## Procedure

**status**
1. Read the running collection, the newest collector tasks, the collector attachment and the per-device statuses. A read that fails is a limit, not a stop.
2. **failed** when the last finished task ended failed or cancelled, the attached collector is not connected, any device failed collection, or the network has cloud setups and the newest collected snapshot did not collect all of their cloud accounts (whether or not a recent task is in the window: tasks can all end well while many accounts were skipped; `cloud_coverage` in the evidence rolls it up by setup, and `inspect-inventory` kind `cloud_coverage` gives each account and a reason). A network with no cloud setup pays nothing for that check. **ok** when something was read and none of that holds. **unknown** when nothing could be read. First the newest snapshot's origin is read (`processingTrigger`, the same kind `inspect-snapshots` shows): for an IMPORT or REPROCESS snapshot with nothing running and nothing failed the answer is **unknown** with the finding "The newest snapshot was imported/reprocessed; no collection ran in Forward for it" and next actions `inspect-snapshots` (and the original source or import named in the limits), not the "cannot say collection is healthy" wording, which is kept for collected snapshots and for one whose origin is not reported (a limit says so). A failed task or a running collection is still reported as such.
3. Forward lists the organisation's newest tasks and filters by network afterwards, so an older task can be outside the window; the limits say so.

**config**
1. Read the collection devices, endpoints, jump servers, proxies, cloud setups and the organization's data files. A read that fails is a limit.
2. List devices with host, type, port, whether CLI and HTTP credentials are set (never the credential or a login name; the `device` input adds the login ids, never a value), and whether collection is on.
3. List each controller-managed setup (`controller_setups`: its collector, controllers and managed devices with the collect flag and each device's last saved connectivity test: failed with its error and phase, never tested, or no error recorded with the time; a result outlives the test that wrote it, so compare `tested_at` with when a test was queued; also `started_at`, `host_ips`, `unauthorized_commands`, `discovered_cli_credential` (the login the test found working, which answers which login worked for a tested device) and, for a controller, `managed_devices_reported`; `HOST_NOT_REPORTING_ANY_GUEST` means the controller's test found no guests, not a login failure, and Forward's API does not return the guest list that phase used), each cloud setup (name, type, whether it collects, its configured regions and each region's last connectivity test result), each organization data file (name, its NQE field name, type, whether it is attached to this network), and each network data connector (name, base URL, endpoint count, whether it collects, its status in the latest snapshot); `data_file` additionally reads one stored file's inferred NQE schema (its content only with `include_content`), and `data_connector` additionally reads one connector's endpoints, proxy/collector and last connectivity test result (all reads: no credential, credential id or connector header value is shown; a data file's content is shown only on request).
4. **ok** when something is configured; **unknown** when nothing is (devices may arrive by upload or a cloud setup) or nothing could be read.

## Evidence

One `collection` item whose fields depend on the view; the fields of every view (migration's `controllers`, `suggested_migrate`, `migration_plan`, `edge_coverage`, `devices`, `credential_candidates`, and the rest) are in `reference/evidence.md` (`fwdctl describe inspect-collection reference/evidence.md`).

## Output

The envelope in `schema/skill-result.schema.json`.

## Next actions

`investigate-collection-failure` for why devices are missing, `inspect-snapshots` for what was collected.

## Running this skill

Give the inputs as one JSON object on stdin: `echo '{"network_id": "<id>", "view": "status"}' | fwdctl run inspect-collection`. It needs the `fwdctl` binary and `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD` in the environment. Read `status` (`ok`, `failed`, `unknown`, `error`) and the `limits`; `unknown` is never a pass. Inputs are listed above and in `schema.json`.
