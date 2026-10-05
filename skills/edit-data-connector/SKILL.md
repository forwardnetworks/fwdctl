---
name: edit-data-connector
description: Manages a network's HTTP data connector (add, update, delete, test), polled each collection. Dry run unless apply is true. Use when an API, not SNMP/CLI, is the source.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "nqe"
  summary: "data connector"
  maturity: "1"
  class: write
  effect: "network"
  secrets: "true"
  reversible: "true"
  tools: "data connectors"
---

# edit-data-connector

## Intent

A data connector is a **per-network** HTTP source: Forward's Collector polls it every collection and stores each endpoint's response under
`network.dataConnectors`, so a query can read it. Unlike a data file (`edit-data-file`, organization-wide, uploaded once), a connector belongs
to one network and is fetched live on a schedule. This skill adds, updates, deletes and test-runs one. `inspect-collection` (view config) lists
what exists and previews one's endpoints, status and last test result; this skill only writes (`test` is the one exception: it calls out live).

## Actions

| `action` | Inputs | Undo |
|---|---|---|
| `add` | `network_id`, `name`, `base_url`, `endpoints` (non-empty), optional `refs`, `extra_headers`, `disable_ssl_validation`, `collect` | `delete` the same connector |
| `update` | `network_id`, `name`, at least one of `base_url`, `endpoints`, `refs`, `extra_headers`, `disable_ssl_validation`, `collect` | `update` again with the previous values (shown as `before`) |
| `delete` | `network_id`, `name`, `confirm` (must equal `name`) | add it back with the same `base_url`/`endpoints`; the stored test result and collection history are not restored |
| `test` | `network_id`, `name` | none: a read-only connectivity check, not a write (refuses `apply`) |

`refs` holds `credential_id`, `proxy_server_id`, `collector_id`. On `update` each is **tri-state**: leave the key out to leave it alone, give
it as `""` to clear it (no credential, no proxy, or Forward picks a Collector), give it a value to set it. `endpoints` is `[{name, path}]`,
**static only**: this skill does not set a paginated endpoint's pagination model. `extra_headers` must not carry a secret; authenticate with
`refs.credential_id`.

## What is refused, with the reason

`add` on a name that already exists (refused, not overwritten: use `update`); `update`/`delete`/`test` on a name that does not exist (`delete`
on an already-gone name is reported **ok**, nothing to change); `update` with no field to change; `delete` without `confirm` matching `name`
exactly; `apply` with `test` (it never writes).

## Procedure

1. Read the connector first (`inspect-collection` has already shown it, or this skill re-reads before `update`/`delete`/`test` to confirm it
   exists and to show the real `before`).
2. Dry run: show the plan, before/after, and the undo.
3. Apply: send the one call, then read back (`add`/`update`/`delete`) and report **failed** if the state does not match what was asked. `test`
   has nothing to read back: it reports the connectivity result Forward just returned (and also stored).

## The `test` action is different

It calls the connector's real HTTP endpoint through Forward's Collector right now and blocks until it answers, up to Forward's own 60-second
ceiling, unlike every other action here, which only edits Forward's stored configuration. A failed test (`error`, `error_desc`) is a normal
**failed** result, not a tool error: a connectivity problem on the target side, not a problem with this skill's own call. `test` is still not a
pure read: Forward replaces the connector's stored test result with this outcome (no config or collection changes), so `inspect-collection`
view config shows this run afterward, not the previous one; the finding says so.

## Evidence

One `state` item per action, named `dataConnectors`.

## Next actions

`inspect-collection` to see the connector's endpoints, status and stored test result once a collection runs.

## Running this skill

`echo '{"action":"add","network_id":"<id>","name":"weather-feed","base_url":"https://api.example.com","endpoints":[{"name":"current","path":"/v1/current"}]}' | fwdctl run edit-data-connector`
is the dry run; add `"apply": true` after it is accepted. `echo '{"action":"test","network_id":"<id>","name":"weather-feed"}' | fwdctl run edit-data-connector`
runs a connectivity test.
