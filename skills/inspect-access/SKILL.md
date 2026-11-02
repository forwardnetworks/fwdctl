---
name: inspect-access
description: Shows this login's Forward roles and what they allow, explains a refused operation, lists users and groups, reads the audit log. Read-only. Use when access or a 403 is the question.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "access"
  summary: "roles, users, groups, audit"
  maturity: "1"
  class: read
  tools: "current user session, users with roles, access control groups"
---

# inspect-access

## Intent

Forward decides every request by role. This skill says what the login you are using can do, **why something was refused and what resolves it** (even when you cannot resolve it yourself), and who has access.
It reads and changes nothing. To change access use `edit-access`.

## Views

Five views: `me` (default: the roles in effect for this login), `explain` (why a refusal happened and what resolves it), `users`, `groups` and `activity` (the audit log). Every input and the model of roles, groups and refusals: `fwdctl run inspect-access --help` (from `reference/inputs.md`).

**A refusal names the operation, never the role or the network; `explain` closes that gap. An organization administrator holds every operation; a network ADMIN does not manage users.**

## Procedure

1. `me` first when the question is "what can I do"; `explain` with the 403 text when something was refused.
2. Read the finding, the evidence and the limits; `unknown` is never a pass.
3. If the resolution needs another person (an organization administrator), say who and what to ask for; `edit-access` is how they do it.

## Evidence

One `state` item per view: the session, the explanation, the users or the groups (windowed).

## Next actions

`edit-access` (dry run) to change roles, users or groups.

## Running this skill

Run `fwdctl run inspect-access --help` for the inputs and an example, then give them as one JSON object on stdin: `fwdctl run inspect-access < input.json`. Read `status` (`ok`, `failed`, `unknown`, `error`) and the `limits`; `unknown` is never a pass.

## Examples

```bash
echo '{}' | fwdctl run inspect-access
echo '{"view":"explain","error":"Missing permission: NetworkOperation.EDIT_CHECKS","network_id":"N"}' | fwdctl run inspect-access
```

