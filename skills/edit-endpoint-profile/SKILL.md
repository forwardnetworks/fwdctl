---
name: edit-endpoint-profile
description: Copies an SNMP endpoint profile with extra OIDs, repoints endpoints or deletes one, with before and after. Dry run unless apply is true. Use when changing what endpoints collect.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "snapshots"
  summary: "what endpoints collect"
  maturity: "1"
  class: write
  effect: "org"
  secrets: "false"
  reversible: "true"
  tools: "endpoint profiles, endpoints"
---

# edit-endpoint-profile

## Intent

An endpoint profile says what Forward collects from a kind of endpoint (an SNMP profile lists OIDs, a CLI profile commands, an HTTP profile requests). A profile is an
**organization-wide** object: every network sees it and Forward pushes it to the collectors, but only the endpoints assigned to it use it. This skill makes the narrow,
reversible change that lets you try an extra OID on **one** endpoint without touching the profile the others use: create a new SNMP profile as a copy of an existing one plus
the extra OIDs, point one endpoint at it, and later point it back and delete the copy. It does not edit an existing profile in place.

## Inputs

`network_id` and at least one of:
- `create_profile`: `name` (letters, digits, `_`, `-`; unique ignoring case), `copy_from` (an SNMP profile id such as `SNMP-10`) and `add_oids` (`name`, numeric dotted `oid`; a subtree root such as `1.3.6.1.4.1.10418.26` is accepted).
- `assign`: `endpoints` (1 to 20 endpoint names of this network, matched exactly) and `profile` (an existing profile id, or `new` for the one `create_profile` makes in the same run). The profile type must match the endpoint's.
- `delete_profile`: a profile id, **on its own** (reassign first; Forward refuses to delete a profile any endpoint of the organization still uses).

Optional `apply` (default false). See `schema.json`.

## Reading the profiles first

`inspect-platform` with `area: endpoint_profiles` lists every profile (SNMP, CLI, HTTP); `inspect-collection` view `config` shows the ones endpoints use. No dry run is needed to read.

## Dry run

Without `apply: true` nothing is changed. The skill reads the network's endpoints and the organization's profiles, refuses what cannot work (an unknown profile or endpoint, a type mismatch, a
name that exists, an endpoint with no profile to restore, a profile still used by this network) and returns `mode: dry_run` with one change per step: the profile to create (its name, the
number of OIDs, the ones added), each endpoint's current and new profile, and the delete. Show it to the person who asked, naming the organization-wide effect, before applying.

## Procedure

1. Read the endpoints of the network and every profile. Validate as above; a refusal is **failed** with nothing sent.
2. Dry run: return the plan and the undo.
3. `apply: true`: create the profile, repoint each endpoint, then delete (when asked). It stops at the first failure and says what was done and how to undo it. It then reads the profile and the endpoints back; a state that is not the requested one is **failed**, never ok.

## Undo

Reassign each endpoint to the profile it had (the change's `undo` and the finding name them), then delete the new profile: run this skill with `assign` back, and then `delete_profile` alone.
A delete cannot be undone (its stored connectivity test results go with it): recreate the profile with `create_profile`. Deleting a profile an endpoint still uses is refused by Forward.

## What it does not do

It does not collect: the new OIDs appear in NQE `network.endpoints[*].snmpOutputs[*].rawOidEntries` only after a **new collection snapshot** (`edit-collection` starts one, with approval). Whether Forward
walks a custom OID as a subtree is shown by the collected rows, not asserted. CLI profiles are not created here: a profile is collected only if ALL its commands (custom, detector, name detector) match the organization's approved-command patterns; if any one fails, the whole endpoint is excluded from collection. The approved list can be replaced only by a Forward-signed file, so a custom list cannot be self-authored (`inspect-collection` config shows the approved-command state).

## Evidence

The envelope with `mode` and `changes`. Read the profiles and endpoints back with `inspect-collection` (view config).

## Next actions

`edit-collection` to collect, then `inspect-inventory` or an NQE query over `snmpOutputs` to see what came back; `inspect-collection` to read the profiles.

## Running this skill

`echo '{"network_id": "<id>", "create_profile": {"name": "try_vendor", "copy_from": "SNMP-10", "add_oids": [{"name": "vendor_root", "oid": "1.3.6.1.4.1.10418.26"}]}, "assign": {"endpoints": ["<endpoint>"], "profile": "new"}}' | fwdctl run edit-endpoint-profile`.
Read `status`, `mode`, the `changes` and the `limits`; `unknown` is never a pass. Add `"apply": true` only after the dry run was accepted.
