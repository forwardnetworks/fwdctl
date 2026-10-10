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

What each input means, and the fields per object and action: `fwdctl run edit-network --help`, or `reference/inputs.md`, `reference/objects.md`.

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
