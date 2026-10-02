---
name: inspect-access
description: Shows this login's Forward roles and what they allow, explains a refused operation, lists users and access groups. Read-only. Use when access or a 403 is the question.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "access"
  summary: "roles, a refusal explained, users, groups"
  maturity: "1"
  class: read
  tools: "current user session, users with roles, access control groups"
---

# inspect-access

## Intent

Forward decides every request by role. This skill says what the login you are using can do, **why something was refused and what resolves it** (even when you cannot resolve it yourself), and who has access.
It reads and changes nothing. To change access use `edit-access`.

## Views

| `view` | Inputs | Answers |
|---|---|---|
| `me` (default) | optional `network_id` | The roles in effect for this login: organization roles, the role on each network, access control groups, how the login signs in. With `network_id`, what the role there allows (the write operations it holds). |
| `explain` | `operation` (`EDIT_CHECKS`, `NetworkOperation.EDIT_CHECKS`) or `error` (Forward's 403 text), optional `network_id` | The operation, what it means, the lowest role that holds it, the role you hold, and the resolution: who grants what. A licence refusal is reported as a licence problem. |
| `users` | optional `user`, `match`, `limit`, `offset` | Users with enabled state, sign-in source, organization admin, networks and roles, groups, last activity, API token use, two-factor state. Needs VIEW_USER_ACCOUNTS. |
| `groups` | optional `match`, `limit`, `offset` | Access control groups: the identity-provider group names that put a user in it, the role per network, whether it makes organization administrators, device access labels. |

## The model in one page

- **Network roles**, lowest first: `LIMITED_READ_ONLY`, `READ_ONLY`, `OPERATOR`, `WORKSPACE_OPERATOR`, `ADMIN`. Each includes everything below it. `WORKSPACE_OPERATOR` applies to workspace networks only.
- **Organization administrator** holds every operation on every network and manages users and groups. A network `ADMIN` does not: it cannot manage users, view audit logs or add networks.
- **Access control groups** have no member list. A user belongs to a group when one of their identity-provider group names matches the group's names. Only SAML and LDAP users use groups; local users have directly assigned roles. Roles combine to the highest per network.
- **Workspaces** inherit the parent network's role when `WORKSPACE_NETWORK_ROLE_AUTO_PROPAGATION` is on.
- A refusal names the operation (`Missing permission: OrgOperation.MANAGE_USER_ACCOUNTS`), never the role or the network. `explain` closes that gap from Forward's own role definition. A role can still be refused by a licence facet, a device access label or an org property that gates a route: `explain` says so when the role is already enough.

## Refusals of this skill

Reading `users` and `groups` needs VIEW_USER_ACCOUNTS (organization administrator or read-only org role). Without it the answer is **unknown** and says exactly that, with the resolution.
The same explanation is added to **any** skill that Forward refuses for missing permission: the result is `unknown`, names the operation, the role it needs, and points here.

## Procedure

1. `me` first when the question is "what can I do"; `explain` with the 403 text when something was refused.
2. Read the finding, the evidence and the limits; `unknown` is never a pass.
3. If the resolution needs another person (an organization administrator), say who and what to ask for; `edit-access` is how they do it.

## Evidence

One `state` item per view: the session, the explanation, the users or the groups (windowed).

## Next actions

`edit-access` (dry run) to change roles, users or groups.

## Running this skill

`echo '{}' | fwdctl run inspect-access` is `me`. `echo '{"view":"explain","error":"Missing permission: NetworkOperation.EDIT_CHECKS","network_id":"N"}' | fwdctl run inspect-access`.
