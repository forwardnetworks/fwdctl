---
name: edit-device-tags
description: "Puts existing tags on devices or takes them off, after showing the pairs that change. Dry run unless apply is true. Use when asked to tag, label or group devices, or remove a tag."
compatibility: "Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment."
license: Proprietary
metadata:
  cluster: "inventory-topology"
  summary: "tag or untag devices"
  maturity: "2"
  class: write
  effect: "network"
  secrets: "false"
  reversible: "true"
  tools: "device tags"
---

# edit-device-tags

## Intent

Tags group devices for checks, searches and access rules. This skill adds a tag to devices or removes it, and nothing else: it never
creates or deletes a tag definition: `edit-network` (object `tag`, action `create`, `update` or `delete`) does, and a tag must exist before this skill applies it.

## Inputs

`network_id`, `action` (`add` or `remove`), `devices` (names, at most 200), `tags` (names of tags that exist). Optional: `snapshot_id` and `apply` (default false). See `schema.json`.

`snapshot_id` is the snapshot the change takes effect from and the one membership is read at. Omitted, it is the **newest processed snapshot**: Forward writes the tag at that snapshot's instant (later snapshots inherit it, earlier ones are untouched), the devices need only be names in that snapshot (a firewall virtual system or an access point passes), and NQE `device.tagNames` shows the tag on that snapshot at once, so a tag can be written and read back by a sync without a new collection. `"staging"` writes no snapshot: the change starts after every existing snapshot, the devices must be collection sources, and NQE shows it only in a snapshot collected afterwards. With no processed snapshot the skill falls back to staging and says so. From Forward's source and API spec; the immediate NQE visibility has not been confirmed live.

## Dry run

Without `apply: true` nothing is changed. The skill reads which devices carry each tag, and returns `mode: dry_run` with one change per tag
listing only the devices that would actually change. Show it to the person who asked before applying.

## Procedure

1. Read the tags and their devices. An `add` of a tag that does not exist is **failed** (nothing changed).
2. Keep only the pairs that differ: a device that already has the tag is not touched by `add`; one that lacks it is not touched by `remove`.
3. Nothing differs: **ok**, nothing changed. Dry run: return the plan. With `apply: true`: send one change per tag, then read the tags back; a pair not in the requested state is **failed**, never ok.
4. Forward refuses a whole batch when one device is not accepted (with `"staging"`: not a collection source; at a snapshot: not a name in that snapshot). The skill then retries that tag one device at a time, so the rest is applied, and lists each refused device with Forward's reason in the evidence (`refused`); a device Forward accepted but that does not show the tag afterwards is listed in `not_in_requested_state`. Either makes the result **failed** (partly applied); `changes` lists only what was made, and in the evidence `changes` is what Forward accepted while `planned` is what was asked. A virtual context (a PAN-OS vsys, a virtual device) is not a collection source and is expected to take its tags from the physical device (Forward groups virtual contexts under the physical device's tags in its layout; the tag write API's own handling of one is not verified): tag the physical device. Each refusal carries `physical_name` (the raw `device.system.physicalName` on the newest processed snapshot, when it differs from the device's name) and `parent_device` (the Forward device that name resolves to, matched case-insensitively against device names, then against `system.hostNames` and `system.otherNames`; the virtual systems of a firewall carry the firewall's own host name, so an alias that several devices carry is narrowed by dropping the devices that are themselves part of another; it resolves only when exactly one is left, never guessed). The raw name is the parser's record, not always a modeled device's name: a live check found firewall virtual systems whose physical name matched no device name, so `parent_device` can be empty while `physical_name` is set. An access point listed under a controller has its own name as physical name: no parent is recorded, so there is none to offer. The tag is written at Forward's staging instant, after every existing snapshot (from Forward's source, not a live test), so with `"staging"` NQE `device.tagNames` shows it only in a snapshot collected afterwards.

## Undo

Each change carries `undo`: run this skill with the opposite action on exactly those devices and tag. A tag change takes effect from the
snapshot named in `snapshot_id` (default the newest processed one), or in the next snapshot with `"staging"`, where the devices must be collection sources.

## Evidence

One `state` item with `action`, the per-tag `changes`, `mode` and, once applied, `held`.

## Output

The envelope in `schema/skill-result.schema.json`, with `mode` and `changes`.

## Next actions

`inspect-inventory` to see the devices and their tags.

## Running this skill

Run `fwdctl run edit-device-tags --help` for the inputs and an example, then give them as one JSON object on stdin: `fwdctl run edit-device-tags < input.json`. It is a dry run unless the input has `"apply": true`; add that only after the dry run was accepted. Read `status` (`ok`, `failed`, `unknown`, `error`), `mode` and the `limits`; `unknown` is never a pass.

## Examples

```bash
echo '{"network_id": "<id>", "action": "add", "devices": ["r1", "r2"], "tags": ["edge"]}' | fwdctl run edit-device-tags
```

## Reference

Read the file you need, when you need it (where you cannot read files: `fwdctl describe edit-device-tags reference/<file>`).

- [reference/behaviours.md](reference/behaviours.md): when tags take effect, case-insensitive names, all-or-nothing batches (unverified, labelled). Read before applying tags.
