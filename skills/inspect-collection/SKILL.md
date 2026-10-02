---
name: inspect-collection
description: Reports on collection without diagnosis: view status (running, task outcomes, collector, failed devices) or view config (what is collected). Use when asked if collection runs or is healthy.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "snapshots"
  summary: "collection state and config"
  maturity: "3"
  tools: "collector tasks, collections, collectors, classic devices, endpoints, jump servers, proxies"
---

# inspect-collection

## Intent

The read-only side of collection. `view: status` is the present: a running collection, the latest tasks, the collector and each device's status. `view: config` is what Forward is told to collect, so an absence of data can be explained. `investigate-collection-failure` stays separate: it classifies why a finished snapshot is missing devices.

## Inputs

`network_id`. Optional: `view` (`status`, the default, or `config`), `limit`, `offset`, `data_file` (view config: an exact data-file name, to also read its inferred schema), `include_content` (with `data_file`: also the first 2000 characters of the file, unredacted; off by default because a file can hold secrets), `data_connector` (view config: an exact data-connector name, to also read its endpoints and last test result). See `schema.json`.

**Waiting.** `wait_seconds` (view status, at most 120) polls every 5 seconds while a collection runs and returns when it finishes or the time is up, so one call after `edit-collection` apply reads the outcome instead of polling by hand. A running collection's finding names the task, how long it has run, how many sources finished and a rough estimate of the rest (an estimate: sources are not equally slow).

## Procedure

**status**
1. Read the running collection, the newest collector tasks, the collector attachment and the per-device statuses. A read that fails is a limit, not a stop.
2. **failed** when the last finished task ended failed or cancelled, the attached collector is not connected, or any device failed collection. **ok** when something was read and none of that holds. **unknown** when nothing could be read. First the newest snapshot's origin is read (`processingTrigger`, the same kind `inspect-snapshots` shows): for an IMPORT or REPROCESS snapshot with nothing running and nothing failed the answer is **unknown** with the finding "The newest snapshot was imported/reprocessed; no collection ran in Forward for it" and next actions `inspect-snapshots` (and the original source or import named in the limits), not the "cannot say collection is healthy" wording, which is kept for collected snapshots and for one whose origin is not reported (a limit says so). A failed task or a running collection is still reported as such.
3. Forward lists the organisation's newest tasks and filters by network afterwards, so an older task can be outside the window; the limits say so.

**config**
1. Read the collection devices, endpoints, jump servers, proxies, cloud setups and the organization's data files. A read that fails is a limit.
2. List devices with host, type, port, whether CLI and HTTP credentials are set (never the credential, its id or a login name), and whether collection is on.
3. List each cloud setup (name, type, whether it collects, its configured regions and each region's last connectivity test result), each organization data file (name, its NQE field name, type, whether it is attached to this network), and each network data connector (name, base URL, endpoint count, whether it collects, its status in the latest snapshot); `data_file` additionally reads one stored file's inferred NQE schema (its content only with `include_content`), and `data_connector` additionally reads one connector's endpoints, proxy/collector and last connectivity test result (all reads: no credential, credential id or connector header value is shown; a data file's content is shown only on request).
4. **ok** when something is configured; **unknown** when nothing is (devices may arrive by upload or a cloud setup) or nothing could be read.

## Evidence

One `collection` item. status: `newest_snapshot` (`id`, `kind`, `origin`), `running`, `progress`, `recent_tasks`, `collector`, `devices`, `failed_devices`. config: `devices` (paged), `device_count`, `devices_not_collected`, `endpoint_count`, `endpoints_by_type_and_profile`, `endpoints` (paged by limit and offset, each with its `profile_id`), `endpoint_profiles` (the profiles those endpoints use: an SNMP profile's OID sets and custom OIDs, a CLI profile's commands, an HTTP profile's requests and header names, never header values), `collection_schedules` (each: enabled, time zone, and either the times and days or the period; no next run is returned by Forward), `approved_cli_commands` (count, and whether the organization uploaded its own list: a CLI command runs only if approved), `jump_servers`, `proxies`, `cloud_setups` (name, type, whether it collects, its regions and each region's last connectivity test), `summary` (counts over the WHOLE device list, whatever page is shown: by type, without a CLI credential, SNMP collection on; Forward's device records carry no vendor, jump server or creation time, so those cannot be counted, and the per-collection device counts are in `investigate-collection-failure` view `history`), the collector's configured `concurrency` in the status view's `collector` block (128 when unset, said so), `data_files` (name, `nqe_name`, type, description, whether attached to this network, how many networks in all), and with `data_file` given, `data_file_schema` (Forward's inferred NQE type, data format, warnings, errors and a content preview only with `include_content`, truncated past 2000 characters); `data_connectors` (name, base URL, endpoint count, whether it collects, status in the latest processed snapshot), and with `data_connector` given, `data_connector_detail` (its endpoints, proxy/collector and last stored connectivity test). Profiles are organization-wide and Forward's API can edit them (SNMP custom OIDs, CLI commands, HTTP requests); no skill does, since a change applies to every network and collector. A data file attached to a network is read in NQE as `network.extensions.<nqe_name>`, a record `{status: OK | MISSING | INVALID_DATA, value}`; MISSING means no snapshot since attachment has carried it yet. A data connector is per-network (not organization-wide like a data file) and is read in NQE as `network.dataConnectors`; `edit-data-connector` manages it.

## Output

The envelope in `schema/skill-result.schema.json`.

## Next actions

`investigate-collection-failure` for why devices are missing, `inspect-snapshots` for what was collected.

## Running this skill

Give the inputs as one JSON object on stdin: `echo '{"network_id": "<id>", "view": "status"}' | fwdctl run inspect-collection`. It needs the `fwdctl` binary and `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD` in the environment. Read `status` (`ok`, `failed`, `unknown`, `error`) and the `limits`; `unknown` is never a pass. Inputs are listed above and in `schema.json`.
