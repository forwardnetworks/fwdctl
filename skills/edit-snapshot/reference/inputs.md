# edit-snapshot: inputs

`network_id`, `action`, and `snapshot_id` (not for `retention_policy`). Optional: `apply` (default false), and by action:

| `action` | Also takes | What it does | Undo |
|---|---|---|---|
| `note` | `note` (empty clears; at most 1000 bytes) | sets the snapshot's note, showing what it replaces | run it again with the earlier note |
| `reprocess` | | recomputes the model (paths, checks, NQE answers) from what was collected; the fix for a FAILED, stale or UNPROCESSED snapshot; does not wait | none needed: same data, same result |
| `invalidate` | | empties the derived model until reprocessed | `reprocess` |
| `favorite` | | marks it a favorite, which retention never thins | `unfavorite` |
| `unfavorite` | | clears the favorite flag, so retention may thin it | `favorite` |
| `delete` | `confirm` = `snapshot_id` | removes the snapshot and its model | **none** |
| `export` | `snapshot_id`, `definition` {`path`, `only_config`, `include_devices`, `exclude_devices`, `obfuscate_names`}, `secret_file` or `secret_env` (obfuscation key) | writes the snapshot (or some devices) to a new ZIP file here; never overwrites; nothing changes in Forward | delete the file |
| `import` | `definition` {`files`, `exclude_failed_devices`, `skip_processing`}, `note` (no `snapshot_id`) | uploads ZIP files as a new snapshot of the network | `delete` the new snapshot |
| `retention_policy` | `definition`, `confirm` = `network_id` | sets how Forward thins the network's snapshots (on-premises only) | set the old values back; snapshots already cleaned are gone |

`definition` for `retention_policy`: any of `enabled`, `lastWeek`, `lastMonth`, `lastQuarter`, `lastYear`, `older` (granularity `ALL`, `ONE_PER_DAY`, `ONE_PER_TWO_DAYS`, `ONE_PER_WEEK`, `ONE_PER_TWO_WEEKS`, `ONE_PER_MONTH`, `ONE_PER_QUARTER`, `NONE`); fields left out keep their current value. Forward's allowed combinations are in `reference/retention.md`.

`export` and `import` (files on this machine, the obfuscation key as a secret): `reference/transfer.md`. The obfuscation key is never put in the input: `secret_file` (mode 600) or `secret_env`; this skill is otherwise secret-free.
