# edit-alias: inputs

`network_id`, `action` (`put` or `deactivate`), `name` (the name checks use). `put` also takes `definition`: `{"type": ..., ...}` with the fields of that type, in
Forward's names: **HOSTS** `values` and/or `locations`; **DEVICES** `values`; **INTERFACES** `values` and/or `vlanIds` (ranges such as `20-29`), optional
`vlanIntfTypes` (ACCESS, TRUNK), `isExposurePoint`; **HEADERS** `headerValues` keyed by `mac_addr`, `eth_type`, `vlan_vid`, `ip_addr`, `ip_proto` or `tp_port`;
**LOGICAL_NETWORK** `devices` and/or `edgeNodes`. A field another type owns is refused. Optional: `snapshot_id` (default the newest processed one), `apply`
(default false).
