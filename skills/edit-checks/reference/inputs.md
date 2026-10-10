# edit-checks: inputs

`network_id`, `action` (`create` or `deactivate`). For `create`: `definition` (Forward's check definition: a predefined check type, or an
NQE query), optional `name` (NQE checks only: Forward names a predefined check itself and answers 400 if a name is sent, so a name with a predefined definition is refused in the dry run), `note`, `tags`, `priority`, `persistent`. For `deactivate`: `check_id`. Optional for both: `snapshot_id`
(default the newest processed one), `apply` (default false). Find predefined types and existing checks with
`inspect-checks`; validate an NQE query with `validate-nqe-query` first.
