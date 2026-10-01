---
name: edit-device-tags
description: Puts existing tags on devices or takes them off, after showing the pairs that change. Dry run unless apply is true. Use when asked to tag, label or group devices, or remove a tag.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "inventory-topology"
  summary: "tag or untag devices"
  maturity: "2"
  class: write
  reversible: "true"
  tools: "device tags"
---

# edit-device-tags

## Intent

Tags group devices for checks, searches and access rules. This skill adds a tag to devices or removes it, and nothing else: it never
creates or deletes a tag definition (the API cannot remove one, so creating one could not be undone).

## Inputs

`network_id`, `action` (`add` or `remove`), `devices` (names, at most 200), `tags` (names of tags that exist). Optional: `apply` (default false). See `schema.json`.

## Dry run

Without `apply: true` nothing is changed. The skill reads which devices carry each tag, and returns `mode: dry_run` with one change per tag
listing only the devices that would actually change. Show it to the person who asked before applying.

## Procedure

1. Read the tags and their devices. An `add` of a tag that does not exist is **failed** (nothing changed).
2. Keep only the pairs that differ: a device that already has the tag is not touched by `add`; one that lacks it is not touched by `remove`.
3. Nothing differs: **ok**, nothing changed. Dry run: return the plan. With `apply: true`: send one change per tag, then read the tags back; a pair not in the requested state is **failed**, never ok.

## Undo

Each change carries `undo`: run this skill with the opposite action on exactly those devices and tag. A tag change takes effect in the
network's next snapshot, and the devices must be collection sources.

## Evidence

One `state` item with `action`, the per-tag `changes`, `mode` and, once applied, `held`.

## Output

The envelope in `schema/skill-result.schema.json`, with `mode` and `changes`.

## Next actions

`inspect-inventory` to see the devices and their tags.

## Running this skill

Give the inputs as one JSON object on stdin: `echo '{"network_id": "<id>", "action": "add", "devices": ["r1", "r2"], "tags": ["edge"]}' | fwdctl run edit-device-tags`.
It needs the `fwdctl` binary and `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD` in the environment. Read `status`
(`ok`, `failed`, `unknown`, `error`), `mode` and the `limits`; `unknown` is never a pass. Add `"apply": true` only after the dry run was accepted.
