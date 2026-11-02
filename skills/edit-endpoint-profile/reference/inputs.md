# edit-endpoint-profile: inputs

`network_id` and at least one of:
- `create_profile`: `name` (letters, digits, `_`, `-`; unique ignoring case), `copy_from` (an SNMP profile id such as `SNMP-10`) and `add_oids` (`name`, numeric dotted `oid`; a subtree root such as `1.3.6.1.4.1.10418.26` is accepted).
- `assign`: `endpoints` (1 to 20 endpoint names of this network, matched exactly) and `profile` (an existing profile id, or `new` for the one `create_profile` makes in the same run). The profile type must match the endpoint's.
- `delete_profile`: a profile id, **on its own** (reassign first; Forward refuses to delete a profile any endpoint of the organization still uses).

Optional `apply` (default false).

## Read before applying

### What it does not do

It does not collect: the new OIDs appear in NQE `network.endpoints[*].snmpOutputs[*].rawOidEntries` only after a **new collection snapshot** (`edit-collection` starts one, with approval). Whether Forward
walks a custom OID as a subtree is shown by the collected rows, not asserted. CLI profiles are not created here: a profile is collected only if ALL its commands (custom, detector, name detector) match the organization's approved-command patterns; if any one fails, the whole endpoint is excluded from collection. The approved list can be replaced only by a Forward-signed file, so a custom list cannot be self-authored (`inspect-collection` config shows the approved-command state).
