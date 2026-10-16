# edit-change-set: inputs

`network_id`, `name`. Then `devices` (a list of `{device, commands}`, commands as one string of CLI text with newline-separated lines, or an array of lines) and/or `bgp_advertisements` (`device`,
`external_peer`, `prefix`, `next_hop`, optional `vrf`, `type`, `origin`, `local_pref`, `as_path`, `med`, `communities`). Optional:
`description`, `snapshot_id` (the base; default the newest processed one), `run` (predict it; needs `apply`), `apply` (default false).
`firewall_rules` stages security rule changes on a firewall (PAN-OS and similar): `{op: add, device, rule: {name, action, sourceZones, destinationZones, sourceAddresses, destinationAddresses, applications, services}, predecessor}` or `{op: remove, device, rule_id}`, in rulebase `scope_id` (default LOCAL) and `rulebase_id` (default PRIMARY); the result shows each firewall's staged rule diff, and `verify-change` predicts and judges it. Model new BGP originations with `bgp_advertisements`: Predict discards CLI `network` statements.

The dry run does the cheap checks the apply would fail on: an organization whose license lacks Predict (or a login without the permission) is reported as unknown with the licence limit, not as an ok dry run, and a device that is not in the base snapshot is refused by name.
