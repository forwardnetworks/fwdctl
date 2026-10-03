---
name: edit-alias
description: Manages a named alias of hosts, devices, interfaces or headers that checks use: put, replace or end. Dry run unless apply is true. Use when a check needs a named group.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "inventory-topology"
  summary: "aliases"
  maturity: "1"
  class: write
  effect: "snapshot"
  secrets: "false"
  reversible: "true"
  tools: "aliases"
---

# edit-alias

## Intent

An alias is a named set that checks and path searches refer to by name: hosts (names, addresses, subnets, MACs), devices (names and globs), interfaces
(`device port`, globs, VLAN ranges), traffic headers, or a logical network. This puts one, replaces one's definition, or ends one. It is not a second name for a device.
`inspect-topology` kind `aliases` lists what exists.

## Inputs

`network_id`, `action` (`put` or `deactivate`), `name` (the name checks use). `put` also takes `definition`: `{"type": ..., ...}` with the fields of that type, in
Forward's names: **HOSTS** `values` and/or `locations`; **DEVICES** `values`; **INTERFACES** `values` and/or `vlanIds` (ranges such as `20-29`), optional
`vlanIntfTypes` (ACCESS, TRUNK), `isExposurePoint`; **HEADERS** `headerValues` keyed by `mac_addr`, `eth_type`, `vlan_vid`, `ip_addr`, `ip_proto` or `tp_port`;
**LOGICAL_NETWORK** `devices` and/or `edgeNodes`. A field another type owns is refused. Optional: `snapshot_id` (default the newest processed one), `apply`
(default false). See `schema.json`.

## When it takes effect

An alias applies to the snapshot it is put on and to every LATER snapshot of the network; earlier snapshots do not see it. Put it on the newest snapshot and it carries
on to the ones collected afterwards. Deactivating ends it at that snapshot's creation time, for it and every later one; the name can then only be reused as a new alias.

## Dry run

Without `apply: true` nothing changes. The result shows what the alias is now (`before`), what it would be (`after`), and the undo. Show it before applying.

## Apply and read back

Applies once, then reads the alias back and says if it does not show the saved definition. Forward checks the values itself (a 400 for one it cannot parse); a snapshot
forked for Predict needs the organization property PREDICT_UNSANDBOXED. Putting the same definition again is a no-op.

## Undo

A replaced alias: put the `before` definition again. A new alias: deactivate it. A deactivated alias: put its `before` definition, which makes a new alias with that name
from this snapshot on.

## Limits

This cannot tell which checks use an alias, so deactivating one that a check refers to breaks that check from this snapshot on (`inspect-checks` shows an ERROR).

## Example

`echo '{"network_id":"N","action":"put","name":"web-servers","definition":{"type":"HOSTS","values":["10.1.0.0/24"]}}' | fwdctl run edit-alias` is the dry run; add `"apply": true` after it is accepted.
