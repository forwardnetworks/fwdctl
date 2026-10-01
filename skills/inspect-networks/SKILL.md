---
name: inspect-networks
description: Lists the Forward networks the login can see, with ids, names and which are workspaces. Use when the network id is not known, or to find a network's id by name before any other question.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "environment"
  summary: "list networks and ids"
  maturity: "3"
  tools: "networks"
---

# inspect-networks

## Intent

Every other skill takes a `network_id`. This finds it. It is the only skill about the login itself rather than one network,
so its result has `context.scope` `account` and no `network_id`.

## Inputs

None are required. Optional: `name` (case-insensitive substring of the network name, or an exact id), `limit` (default 25, at
most 200) and `offset` for paging. See `schema.json`.

## Procedure

1. List every network the login can see.
2. Filter by `name` if given, sort by name, and page with `limit` and `offset`.
3. Decide:
   - Networks returned: **ok**, with id, name, whether it is a **workspace** (a copy of another network made for a change; its
     `parent_id` is the real network), creator, creation time and a one-line note. If more match, the limits say how to page.
   - None: **unknown**. A login with no access, a wrong name and an empty account look the same, so the skill never says
     "you have no networks".

## Evidence

One `state` item: the total, the window and the rows.

## Output

The envelope in `schema/skill-result.schema.json`.

## Next actions

`inspect-inventory` to see what a network holds, `investigate-collection-failure` to see how healthy it is.

## Running this skill

Give the inputs as one JSON object on stdin: `echo '{}' | fwdctl run inspect-networks`. It needs the
`fwdctl` binary and `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD` in the environment. Read `status`
(`ok`, `failed`, `unknown`, `error`) and the `limits`; `unknown` is never a pass. Inputs are listed above and in `schema.json`.
