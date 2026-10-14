# Reading vendor config text: why a missing line is not an answer

Read before saying a feature is off, a rule is absent, or a setting is missing from a device's text. General vendor behaviour, not checked against this repository's code: confirm on the device's own text and say which file you read.

- **A feature gate hides the config.** NX-OS configuration for a feature does not exist unless `feature <name>` is enabled, so absent lines may mean the feature is off. Check the gate first.
- **Group-supplied config is not inline.** Junos `apply-groups` supplies configuration that is not in the stanza you are reading. Search the groups before saying a setting is missing.
- **Dormant objects.** An ASA ACL does nothing without an `access-group` applying it. A disabled FortiOS policy is still in the file. Say "present but not applied" when that is the case.
- **Partial views.** Managed firewalls (a management server pushing a rulebase) can leave the device text without the rulebase, so "no rule allows X" cannot be claimed from it. A firewall with several contexts may show only one view. Say which part the file covers.
- **Spelling differs.** The same setting has different keywords per vendor. Search with the alternatives, and say which spellings you tried.

When nothing matches, the answer is **unknown** with the files read and the patterns tried, not "not configured".

## Reading a rule or policy: order, references, context

A rule line alone rarely says what the device does. Before describing a policy, read these. General vendor behaviour, not checked against this repository's code.

- **Order decides.** First match wins, so a broad rule above a narrow one shadows it. Cisco IOS and ASA ACLs, the PAN-OS rulebase and FortiOS policies read top-down; on FortiOS the `edit <n>` number is an id, not the position, so use the order in the file. Azure NSG rules run by priority number, lowest first; AWS network ACLs by rule number, lowest first. AWS security groups have no order and no deny: allow rules plus an implicit deny.
- **A rule points elsewhere.** Fetch what it names before saying what it does: a Junos `export`/`import` policy names a `policy-statement` (its `from` terms are ANDed; `then accept` and `then reject` end evaluation, `next policy` continues), an ASA ACL names `object` and `object-group` entries, a FortiOS policy names its antivirus, IPS, web filter and SSL inspection profiles, a PAN-OS rule names address, service and application objects (some may come from Panorama and not be in the device text). On IOS-XR a `route-policy` `pass` continues and `done` stops.
- **Application and service together.** A PAN-OS rule matches on application and on service; read both. Security rules match the addresses as they were before NAT and the destination zone as it is after NAT, so read the NAT rules with them.
- **Defined is not applied.** An ACL, a policy map or a route policy counts only where something uses it (an interface binding, a neighbor statement, a class map). Say "defined, not applied" when you cannot find the use.
- **Read both peers.** NX-OS vPC and Arista MLAG are configured on a pair; a mismatch or a missing line on one needs the other's text.
- **Say which context.** A Nexus VDC, a FortiOS VDOM, a PAN-OS vsys or an ASA context holds its own config; confirm the file is the one that applies.
- **Cloud is data, not CLI.** Read cloud security groups, route tables and ACLs as the structured records they are. GCP firewall rules apply to targets chosen by network tag or service account, not by address range.

Use this to confirm one device. To find every device with an open management service, a permissive rule or a missing setting, use `check-network-compliance` or an NQE query (`author-nqe-query`), not a text search over files.

