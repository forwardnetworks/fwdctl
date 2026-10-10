# edit-workspace: inputs

`network_id` and exactly one of:
- `create_workspace` (`network_id` is the parent): `name` (unique in the organization), `note`, `devices` (1 or more classic device names of the parent: Forward has no endpoint-only workspace, so name the smallest, most harmless one), `retention_days` (1 to 365, default 7) and `omissions` (any of `CUSTOM_COMMANDS`, `NQE_CHECKS`, `PREDEFINED_CHECKS`, `INTENT_CHECKS`: what is not copied from the parent).
- `add_endpoints` (`network_id` is the workspace, never a production network): `endpoints` of `{name, credential_id, profile_id}` (host, port, protocol and type are read from the parent endpoint of that name; `profile_id` and `credential_id` default to the parent's) and optional `from_network`.
- `delete_workspace: true` (`network_id` is the workspace) with `confirm_name` equal to its exact name.

Optional `apply` (default false).
