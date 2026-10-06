---
name: edit-network
description: Manages networks, locations, device clusters and tag definitions: create, rename, delete. Dry run unless apply is true. Use when setting up or tidying a network.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "inventory-topology"
  summary: "network structure"
  maturity: "1"
  class: write
  effect: "network"
  secrets: "false"
  reversible: "false"
  tools: "networks, locations, clusters, device tags"
---

# edit-network

## Intent

Manage the objects around a network: the network itself, its locations and device clusters, and its device tag definitions. Putting tags on devices is `edit-device-tags`; this changes the definitions. It touches no device.

## Inputs

`object` and `action` (required), `network_id` (not for creating a network), `name`, `definition` (the body, one object), `confirm`, `apply` (default false). See `schema.json`.

| `object` | `action` | `name` | `definition` | Undo |
|---|---|---|---|---|
| `network` | `create` | the new network's name | | delete it |
| `network` | `update` | | `name`, `note`, `retention_days` | update back |
| `network` | `delete` | | | **none**; `confirm` = `network_id` |
| `location` | `create` | | `name`, `lat`, `lng`, `city`, `adminDivision`, `country` | delete it |
| `location` | `update` | the location name or id | any create field, `deviceGlobs` | update back |
| `location` | `delete` | the location name or id | | **none**; `confirm` = the location id |
| `location` | `assign` | | `{"<device name>": "<location id>"}` | assign back |
| `cluster` | `create` | | `location_id`, `name`, `devices` | delete it |
| `cluster` | `update` | the cluster | `location_id`, new `name`, `devices` | update back |
| `cluster` | `delete` | the cluster | `location_id` | **none**; `confirm` = the cluster name |
| `tag` | `update` | the tag | `name` (rename), `color` | update back |
| `tag` | `delete` | the tag | | **none**; `confirm` = the tag name |
| `collector` | `assign` | attach a registered collector to the network (needs `network_id`) | `definition` {`username`: the collector's username, from `inspect-platform` area `collectors`} | attach the previous collector again; **not reversible here** when the network had none (no detach in the SDK) |

Detail, including what each read and what the API cannot do: `reference/objects.md`.

## Dry run

Without `apply: true` nothing changes. The result shows before, after and the undo, and names the exact `confirm` an irreversible action needs.

## Procedure

1. Read the current object. A missing one is **unknown**, nothing changed; a name that already exists is refused.
2. Dry run returns the plan. With `apply: true`: one call, then read back; a read-back that does not show the change is **failed**.

## Limits

Deleting a network removes its snapshots, checks and settings. Deleting a tag definition removes it from every device across the whole timeline. There is no read of current device locations in the API.

## Evidence

One `state` item: `object`, `action`, `mode`, `before`, `after`.

## Next actions

`inspect-platform` (areas `locations`, `tag_definitions`) and `inspect-networks` to see the result.

## Running this skill

`echo '{"object":"network","action":"create","name":"lab"}' | fwdctl run edit-network` is the dry run; add `"apply": true` after it is accepted.
