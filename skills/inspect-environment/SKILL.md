---
name: inspect-environment
description: Reports the Forward build, organization, login, license tier and which features are on (value, default, where set). Use when asked what version, license or features are enabled.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "environment"
  summary: "Forward build, license, features"
  maturity: "3"
  tools: "version, organization, users (current), cve index, properties, global properties"
---

# inspect-environment

## Intent

Account-level context for every other skill: what build and organization these results come from. It lists no users, tokens or roles and only names the login itself.

## Inputs

Every input: `fwdctl run inspect-environment --help` (from `reference/inputs.md`). See also: `reference/properties.md`. Read before relying on a result (printed by `fwdctl run inspect-environment --help`): Features. See also: `reference/features.md`, `reference/official-mcp.md`.

**A feature that reads on can still be off for one network, and the API does not say who set a value or when.**

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

Run `fwdctl run inspect-environment --help` for the inputs and an example, then give them as one JSON object on stdin: `fwdctl run inspect-environment < input.json`. Read `status` (`ok`, `failed`, `unknown`, `error`) and the `limits`; `unknown` is never a pass.

## Examples

```bash
echo '{}' | fwdctl run inspect-environment
```
