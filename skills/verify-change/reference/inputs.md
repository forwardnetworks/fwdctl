# verify-change: inputs

`network_id` is required. `view` picks the question (default `verify`; `verdict` is the same view): an input that belongs to another view is rejected by name.

| `view` | Takes |
|---|---|
| `verify` | a selector (`change_set_id`, or both `before_snapshot_id` and `after_snapshot_id`), `run_predict`, `connectivity_timeout_seconds`, `expectations` |
| `impact` | the same selector, `run_predict`, `connectivity_timeout_seconds`, and `device` |
| `describe` | `change_set_id` and `device` |

- `change_set_id`: a Predict change set. `run_predict: true` lets the skill run Predict when no processed prediction exists (without it the skill never runs one).
- `before_snapshot_id` and `after_snapshot_id`: compare two snapshots instead of a change set. Give both.
- `expectations`: flows that must end `delivered` or `blocked` after the change. Each has `dst_ip` and `expect`, and optionally `src_ip`, `from`, `protocol`, `src_port`, `dst_port`.
- `connectivity_timeout_seconds` (default 300): how long to wait for the connectivity diff to settle.
- `device`: with `describe`, a firewall whose rule diff (from the change set) to include. With `impact`, a device whose changed ACL rules between the two snapshots to list (`acl_changes`); without it, `impact` names the devices whose ACLs changed (`acl_devices`).
- `view: impact` returns the extent of a change and never judges it; `verify` judges against the expectations and the checks.

```bash
echo '{"network_id": "<network id>", "view": "impact", "before_snapshot_id": "<id>", "after_snapshot_id": "<id>", "device": "<device name>"}' | fwdctl run verify-change
```

`commit_id` belongs to views `history` and `config`; `device` to `describe`, `impact` and `config`; `after_snapshot_id` to `affected`.

`object_kind` belongs to view `objects`; `os` and `protocol` to view `commands`; `commit_id` also to `bgp_diffs`; `device` also to `bgp_diffs` and `objects`.
