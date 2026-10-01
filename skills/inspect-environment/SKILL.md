---
name: inspect-environment
description: Reports the Forward build, organization, login, which features are on (value, default, where set) and non-default properties. Use when asked what Forward version or features are enabled.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "environment"
  summary: "Forward build, org, features"
  maturity: "3"
  tools: "version, organization, users (current), cve index, properties, global properties"
---

# inspect-environment

## Intent

Account-level context for every other skill: what build and organization these results come from. It lists no users, tokens or roles and only names the login itself.

## Inputs

None are required. Optional: `include_features` (default true; false skips the five reads of the organization configuration that build the `features` table) and `include_properties` (true to add the organization properties that differ from the defaults, first 50 by name, each with an explanation of what it does and which skill behaviour it changes where this skill holds one: [reference/properties.md](reference/properties.md)). See `schema.json`.

## Features

`features` answers "is this feature on for this organization" for the properties the skills depend on or that gate preview behaviour, from Forward's own configuration API (`GET /api/config` with its filters, and `GET /api/global-config`), read-only. One row per property the build defines: `value` (effective), `state` (`on`, `off`, `automatic` or `on request only` for the advanced reachability mode, `value` for a number or enum), `default` (the deployment default), `overridden_at_org` (the organization overrides the deployment default), `set_at` (`organization override`, `deployment default (differs from the built-in default)`, `built-in default`, or `organization row equal to the deployment default`), `level`, `org_admin_configurability`, `reprocess_to_apply`, and for a feature that is not on `symptom_when_off` and `who_can_change_it`. `disable_flow_computation` reads inverted: `true` means the feature is off. A property the build does not return is listed in `not_defined_by_this_build`, never guessed, and a view that could not be read is named in `sources` and the limits (its fields are left out). The table of the features, how to probe each, the symptom and how to enable it is in [reference/features.md](reference/features.md), with the features no property gates (NQE-driven synthetic nodes, progress and process estimate, internet exposure).

What the API does not tell, and the result says in its limits: who set a value or when; network-level overrides of `disable_flow_computation` and `advanced_reachability_analysis`; per-user toggles; the license tier. A feature that reads on can still be off for one network.

## Procedure

1. Read the version, organization, current login, vulnerability-index metadata and network count. Each read that fails is a limit, not a stop.
2. Report the capabilities the SDK has seen. **unknown** there means not seen yet, never unsupported.
3. A vulnerability index that was never updated says so: it is the one bundled with the build.
4. Decide: **ok** when anything was read, **unknown** when nothing was.

## Evidence

One `state` item with `version`, `organization`, `login`, `cve_index`, `network_count`, `capabilities`, `features` and, on request, `non_default_properties` with `explanations`. The result has scope `account` and no network id.

## Output

The envelope in `schema/skill-result.schema.json`.

## Next actions

`inspect-networks` for the networks this login can see.

## Running this skill

Give the inputs as one JSON object on stdin: `echo '{}' | fwdctl run inspect-environment`. It needs the
`fwdctl` binary and `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD` in the environment. Read `status`
(`ok`, `failed`, `unknown`, `error`) and the `limits`; `unknown` is never a pass. Inputs are listed above and in `schema.json`.
