---
name: edit-access
description: Manages Forward users and access groups: create or disable users, grant org admin, set a network role for a user or group. Dry run unless apply is true. Use for access changes.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "environment"
  summary: "users, roles, access groups"
  maturity: "1"
  class: write
  reversible: "true"
  tools: "users, roles, access control groups"
---

# edit-access

## Intent

Change who can do what in Forward: users, organization administrators, the role a user or group holds on a network, and access control groups. Use `inspect-access` first to see the current state and, when someone was refused,
what role the operation needs.

## Who can run it

An **organization administrator** (MANAGE_USER_ACCOUNTS). A network `ADMIN` cannot manage users. For anyone else the answer is **unknown**, naming what is needed and who to ask. The one exception is granting a **group** a role on a
**workspace** network, which needs ASSIGN_ROLES there (WORKSPACE_OPERATOR or higher) and a role no higher than your own.

## Actions

| `action` | Inputs | Undo |
|---|---|---|
| `create_user` | `user` (the new email, also the username), `password` (temporary), optional `enabled` | `delete_user` |
| `delete_user` | `user`, `confirm` | cannot be undone (recreate with `create_user`, re-grant from before) |
| `set_user` | `user`, `enabled` (enable or disable) | set the previous values |
| `reset_2fa` | `user`, `confirm` | cannot be undone: the user enrols again |
| `set_org_admin` | `user`, `admin` true or false, `confirm` | the opposite |
| `set_network_role` | `network_id`, `role` (LIMITED_READ_ONLY, READ_ONLY, OPERATOR, WORKSPACE_OPERATOR, ADMIN, or `none`), and one of `user` or `group` | set the previous role |
| `create_group` | `group` (the new name), `identity_provider_groups`, `network_roles` (network id to role) or `admin: true` for an organization administrator group (with `confirm`) | delete the group |
| `update_group` | `group`, any of `identity_provider_groups`, `network_roles` (replaces the whole map) | update with the values in before |
| `delete_group` | `group`, `confirm` | create it again from before |

A user is matched exactly by id, username or email, a group by id or name. `confirm` is the username or group name, required for what widens access to everything or cannot be undone. `apply` defaults to false.

## What Forward refuses, refused here first with the reason

Disabling, deleting or revoking admin from yourself; a role for an organization administrator (they hold everything); WORKSPACE_OPERATOR on a non-workspace network; a group role on an organization
administrator group; a role above your own when granting through ASSIGN_ROLES. A group has no member list: a user is in a group when one of their identity-provider group names matches, and only SAML and LDAP users use groups.
Roles from several groups and direct assignment combine to the highest per network, so removing one grant may not remove access.

## Procedure

1. `inspect-access` (`users` or `groups`) to see the target and its current roles.
2. Run this skill without `apply`: read the change, the before, the undo and the limits.
3. Show it and wait for approval. Then `apply: true` (and `confirm` when asked). The result reads the user or group back and reports failed if it does not match.

## Evidence

One `state` item: the action, the target, before and after, whether confirm is needed. Passwords are never read back or shown.

## Next actions

`inspect-access` to see the result.

## Running this skill

`echo '{"action":"set_network_role","user":"alice","network_id":"N","role":"OPERATOR"}' | fwdctl run edit-access` is the dry run; add `"apply": true` after it is accepted.
