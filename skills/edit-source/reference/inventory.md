# A source inventory file (`fwdctl source apply`)

One JSON file describes everything a network collects; `fwdctl source apply FILE --network ID` makes Forward match it. It is a dry run until `--apply`. Each item is compared by name with what Forward holds and is **create**, **update** or **unchanged**; each change goes through the same `edit-platform` / `edit-source` skill a person would call, so their checks apply. It stops at the first failure and marks the rest skipped; run it again after the fix, because unchanged items are left alone.

```json
{
  "network_id": "252606",
  "endpoint_profiles": [{"name": "linux", "command_sets": ["UNIX"]}],
  "credentials": [
    {"name": "lab-login",  "type": "CLI", "username": "admin", "secret_env": "LAB_PW"},
    {"name": "lab-enable", "type": "CLI", "kind": "PRIVILEGED_MODE", "secret_env": "LAB_ENABLE"}
  ],
  "devices": [
    {"name": "r1", "host": "10.0.0.1", "credential": "lab-login", "enable_credential": "lab-enable",
     "enableSnmpCollection": true, "collectBgpAdvertisements": true, "bgpTableType": "BOTH", "bgpPeerType": "BOTH", "bgpAndBmpFailureHandling": "SWITCH_TO_CLI"}
  ],
  "endpoints": [{"name": "h1", "host": "10.0.1.1", "credential": "lab-login", "profile": "linux"}]
}
```

- Order is profiles, credentials, devices, endpoints. Credentials and profiles are referred to by **name** (`credential`, `enable_credential`, `expert_credential`, `http_credential`, `snmp_credential`, `profile`) and resolved to ids; `jump_server` and `collector_id` are ids.
- A secret is never in the file: a credential names `secret_env` (an environment variable) or `secret_file` (a mode-600 file). A file with a field such as `password` is refused. Secrets are never compared, so a changed password is not detected; rotate it with `edit-source` object `credential` action `update`.
- A device's credentials and collection options are compared with what Forward reports (Forward leaves `bgpTableType` and `bgpPeerType` out when they are BOTH). The commands of an existing endpoint profile are not compared.
- The result's last line is JSON: `status`, `mode`, `counts` (create, update, unchanged, failed, skipped) and `items`. Exit 0 done, 1 an item failed, 64 a bad file, 3 Forward unreadable.
- Not covered: removing items that are in Forward and not in the file (nothing is deleted by an inventory), jump servers, proxies and cloud accounts (use `edit-source` for those).
