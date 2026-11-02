# edit-workspace: inputs

`network_id` and exactly one of:
- `create_workspace` (`network_id` is the parent): `name` (unique in the organization), `note`, `devices` (1 or more classic device names of the parent: Forward has no endpoint-only workspace, so name the smallest, most harmless one; a workspace that only runs a CLI endpoint needs an enabled classic device for the collection to run (confirmed once; the device may fail): `edit-source` reference/objects.md, "Running one command on a host"), `retention_days` (1 to 365, default 7) and `omissions` (any of `CUSTOM_COMMANDS`, `NQE_CHECKS`, `PREDEFINED_CHECKS`, `INTENT_CHECKS`: what is not copied from the parent).
- `add_endpoints` (`network_id` is the workspace, never a production network): `endpoints` of `{name, credential_id, profile_id}` (host, port, protocol and type are read from the parent endpoint of that name; `profile_id` and `credential_id` default to the parent's) and optional `from_network`.
- `delete_workspace: true` (`network_id` is the workspace) with `confirm_name` equal to its exact name.

Optional `apply` (default false).

## Read before applying

### Credentials

Endpoints are **not** copied into a workspace. Credentials come across only when the organization property `COPY_CREDENTIALS` is `ENABLED` (or `ENABLED_FOR_ADMINS` and the creator is an admin); it
is an org-wide setting, not an option of the create call, and this skill only **reads** it (`inspect-environment` shows it too) and says in the dry run what it will do. When it is `ENABLED`, **all** of the
parent's secrets are copied with the same ids, so an added endpoint can keep the parent endpoint's credential id (the default when `credential_id` is omitted; a missing one shows as a connectivity
error on the first collection). When it is `DISABLED`, a person creates the credential **inside the workspace in the Forward UI** and its id is the `credential_id`. No secret is ever an input
here or shown in a result.

### What it does not do

It does not collect (`edit-collection` with the workspace as `network_id` does, collecting only what the workspace holds), create credentials, or set the `COPY_CREDENTIALS` property (read it with `inspect-environment`; changing it is an organization admin's setting). Jump servers and proxies are
not carried over for added endpoints.
