# inspect-access: inputs

| `view` | Inputs | Answers |
|---|---|---|
| `me` (default) | optional `network_id` | The roles in effect for this login: organization roles, the role on each network, access control groups, how the login signs in. With `network_id`, what the role there allows (the write operations it holds). |
| `explain` | `operation` (`EDIT_CHECKS`, `NetworkOperation.EDIT_CHECKS`) or `error` (Forward's 403 text), optional `network_id` | The operation, what it means, the lowest role that holds it, the role you hold, and the resolution: who grants what. A licence refusal is reported as a licence problem. |
| `users` | optional `user`, `match`, `limit`, `offset` | Users with enabled state, sign-in source, organization admin, networks and roles, groups, last activity, API token use, two-factor state. With `user` it also reads that user's Forward-internal `system_roles` and the organizations they support (`supported_org_ids`; support access is an unexpired supported-org grant, there is no SUPPORT system role), so a written support list can be read back and compared with a desired one. Needs VIEW_USER_ACCOUNTS. |
| `groups` | optional `match`, `limit`, `offset` | Access control groups: the identity-provider group names that put a user in it, the role per network, whether it makes organization administrators, device access labels. |
| `activity` | optional `since` and `until` (7d, 48h or RFC 3339; default 7d to now), `method`, `status` (ok or failed), `user`, `network_id`, `match` (route prefix), `limit`, `offset` | Forward's audit log (a request made while impersonating is marked `impersonated`, and names the `impersonator` when Forward shows it; `match` as a route prefix finds impersonation calls once Forward audits them: it records the later requests with the impersonator's id, not the impersonate call itself): who called which route, when, from which address, with what response code; totals by user, method, outcome, day and route over up to 30,000 records (said when more match). Needs VIEW_AUDIT_LOGS (organization administrator). It records requests, not bodies, so it says who touched the device list, not which devices. No record is not proof nothing happened. |

## Read before relying on a result

### The model in one page

- **Network roles**, lowest first: `LIMITED_READ_ONLY`, `READ_ONLY`, `OPERATOR`, `WORKSPACE_OPERATOR`, `ADMIN`. Each includes everything below it. `WORKSPACE_OPERATOR` applies to workspace networks only.
- **Organization administrator** holds every operation on every network and manages users and groups. A network `ADMIN` does not: it cannot manage users, view audit logs or add networks.
- **Access control groups** have no member list. A user belongs to a group when one of their identity-provider group names matches the group's names. Only SAML and LDAP users use groups; local users have directly assigned roles. Roles combine to the highest per network.
- **Workspaces** inherit the parent network's role when `WORKSPACE_NETWORK_ROLE_AUTO_PROPAGATION` is on.
- A refusal names the operation (`Missing permission: OrgOperation.MANAGE_USER_ACCOUNTS`), never the role or the network. `explain` closes that gap from Forward's own role definition. A role can still be refused by a licence facet, a device access label or an org property that gates a route: `explain` says so when the role is already enough.

### Refusals of this skill

Reading `users` and `groups` needs VIEW_USER_ACCOUNTS (organization administrator or read-only org role). Without it the answer is **unknown** and says exactly that, with the resolution.
The same explanation is added to **any** skill that Forward refuses for missing permission: the result is `unknown`, names the operation, the role it needs, and points here.

### Refusal wordings (any skill)

Forward sends no reason code on a 403 (it is null), so a skill that is refused tells the kinds apart by the wording and names who can resolve each: `Missing permission: <operation>` (a role: `view explain` names the lowest role that holds it), `Unlicensed operation: <operation>` (the licence), `... license has expired` (the licence lapsed: a read-only grace period, then only an organization administrator can sign in), `<PROPERTY> is on/off for your organization` (an organization property: `edit-org-property`, and on a hosted Forward most Predict properties need a Forward-support login), `<PROPERTY> is on/off for your deployment` (not changeable by an organization), and `<X> authority required` (a Forward-support or Forward administrator login). The wordings are Forward's own format strings as the Python SDK read them, so they are as stable as undocumented prose is: wording not recognised stays a plain refusal, which does not mean it has no cause. An empty roles answer from Forward is unknown, not a refusal.
