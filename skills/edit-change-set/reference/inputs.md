# edit-change-set: inputs

`network_id`, `name`. Then `devices` (a list of `{device, commands}`, commands as one string of CLI text with newline-separated lines, or an array of lines) and/or `bgp_advertisements` (`device`,
`external_peer`, `prefix`, `next_hop`, optional `vrf`, `type`, `origin`, `local_pref`, `as_path`, `med`, `communities`). Optional:
`description`, `snapshot_id` (the base; default the newest processed one), `run` (predict it; needs `apply`), `apply` (default false).
`firewall_rules` stages security rule changes on a firewall (PAN-OS and similar): `{op: add, device, rule: {name, action, sourceZones, destinationZones, sourceAddresses, destinationAddresses, applications, services}, predecessor}` or `{op: remove, device, rule_id}`, in rulebase `scope_id` (default LOCAL) and `rulebase_id` (default PRIMARY); the result shows each firewall's staged rule diff, and `verify-change` predicts and judges it. Model new BGP originations with `bgp_advertisements`: Predict discards CLI `network` statements.

The dry run does the cheap checks the apply would fail on: an organization whose license lacks Predict (or a login without the permission) is reported as unknown with the licence limit, not as an ok dry run, and a device that is not in the base snapshot is refused by name.

`bgp_advertisements` entries take an `action`: `inject` (the default) or `modify` stage an advertisement (`next_hop` required; `type` is the PEER type, EBGP or IBGP, not the action, and a staged advertisement overrides a received one with the same key only when the peer type matches); `withdraw` stages the loss of a received advertisement (only `device`, `vrf`, `external_peer`, `prefix`); `restore` drops what is staged for that key. Use `withdraw` to model losing a peer OUTSIDE the snapshot, such as an ISP: a CLI `neighbor ... shutdown` removes the session from the prediction but the routes learned from it stay (the dry run warns when a CLI change contains one). `modify` and `withdraw` need received-advertisement collection on the device (`collectBgpAdvertisements` on, BGP table type BOTH or ADJ_RIB_IN); without it the route is left unchanged in the predicted snapshot and no warning is shown. `verify-change view preflight` counts the BGP devices that collect it.

`devices[]` takes exactly one of `commands` (CLI applied on top of the collected configuration, validated by Forward before it is kept) or `config` (the device's WHOLE replacement configuration, at most 2 MB; Forward stores it unchecked, so it is not validated here, and whether Predict can model the result depends on the device type). `firewall_rules[].op` is `add`, `remove`, `edit` (`rule_id` plus the rule's whole new definition in `rule`), `move` (`rule_id`, after `predecessor`; empty = first) or `discard` (drops that rule's staged add, edit, move or removal); only the local rulebase is editable.

`change_set_id` with `lifecycle` acts on an EXISTING change set instead of creating one, and does not combine with devices, bgp_advertisements, firewall_rules, snapshot_id or run: `{op: rename, name}` (and/or `description`), `{op: duplicate, name}`, `{op: delete}` (needs `confirm` = the change set id; there is no undo, duplicate first to keep a copy), `{op: discard_device, device}` (throws away that device's uncommitted edits) and `{op: restore_commit, commit_id}` (makes an earlier version current; the earlier versions stay in the history). Each has a dry run. Predicted snapshots a change set already made are never touched.

A full firewall rule (the shapes matter: address, user, application and service fields are objects, not lists; `{}` means any; `sourceZones` and `destinationZones` are plain lists, empty meaning any; `action` is `DENY`, `DROP` or `PERMIT` in upper case; omit `predecessor` to put the rule at the top; a list where an object belongs fails with a message naming the shape):

```json
{"network_id": "123", "name": "block dns to 8.8.8.8", "apply": true, "run": true,
 "firewall_rules": [{"op": "add", "device": "fw1",
   "rule": {"name": "block-8-8-8-8", "description": "test", "enabled": true, "action": "DENY",
            "sourceZones": ["trust"], "destinationZones": ["untrust"],
            "sourceAddresses": {}, "destinationAddresses": {"subnets": ["8.8.8.8/32"]},
            "sourceUsers": {}, "applications": {}, "services": {}}}]}
```

Read the effect from the rule diff the result carries (`firewall_rules_diff`) and the checks (a firewall rule compliance check moves by the violations the rule creates), not only from a path search on the predicted snapshot: a path search shows a flow the rule blocks only if that flow crosses the firewall, and `verify-change` view `paths` needs advanced reachability on the base (`edit-advanced-reachability`).

