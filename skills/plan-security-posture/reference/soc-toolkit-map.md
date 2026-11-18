# The SOC toolkit map: what each SOC task can use, and what is missing

Read when asked what forward-skills can do for a security operations team, which skill answers a SOC question, or what it cannot do. It is a map from the questions a SOC asks to the skills and views that answer them, with the gaps stated. It is ours (a judgment about coverage, written from what the skills read), not a compliance statement.

**Status.** *covered*: a skill reads data that answers the question. *partial*: it answers part, or only by reading configuration text, or needs judgment. *gap*: nothing answers it today, and it could be built. *outside*: Forward does not hold the data (event logs, packets, threat intelligence), or the toolkit deliberately does not do it (it never changes a device, and the agent writes the report).

Forward models the network from collected configuration and state. Everything below is evidence about the network as of a snapshot: name the snapshot, and read each skill's limits. A SOC question about events, users or traffic is outside what this toolkit holds.

## Start here

| Workflow | Question | Skills and views | Status | What is missing or limited |
|---|---|---|---|---|
| Orient | What evidence can this organization give, and what is switched off? | `inspect-environment` (the feature rows with `topic: security`), `edit-org-property` | covered | turning a feature on is a write to the organization (dry run first); an EXPERIMENTAL feature changes between builds. |

## Monitor and detect

| Workflow | Question | Skills and views | Status | What is missing or limited |
|---|---|---|---|---|
| Monitor | Is collection healthy, and is the evidence current? | `inspect-collection`, `inspect-snapshots`, `investigate-collection-failure` | covered | a snapshot is a point in time: nothing is observed between collections. |
| Detect change | What changed in the network, and when did it start? | `plan-what-changed`, `inspect-history`, `compare-device-config`, `compare-nqe-results` | covered | the change between snapshots, not who made it on a device; changes made through Forward (users, API tokens) are in its audit log, `inspect-access` view `activity` (see Evidence). |
| Detect change | Which devices were added or removed since an earlier snapshot (the asset inventory)? | `inspect-inventory` (kind `devices` with `compare_to_snapshot_id`), `plan-device-audit` | covered | the devices Forward collects: hardware it has no source for is absent. |
| Detect | Unknown or new hosts and MAC addresses | `inspect-inventory` (kind `hosts`), `compare-nqe-results` | partial | the hosts Forward has seen; you set up the comparison across snapshots; no NAC or 802.1X view. |
| Detect | An address list (indicators of compromise) matched against configuration or hosts | `edit-data-file`, `author-nqe-query` | partial | you upload the list and write the NQE join; matching it against traffic or events is outside. |
| Monitor | Which devices or interfaces are unhealthy or busy? | `inspect-performance` | partial | needs performance data from SNMP collection; polled, not streamed. |
| Detect | Incidents raised from device SNMP traps | (none) | outside | Forward can raise incidents from traps (org property `SNMP_TRAP_MONITORING`, changed by Forward support only), but in Forward's source (release 26.11.0-10) the property is for the GUI only: neither the NQE model nor an API route carries the incidents, so no skill can read them. The configured trap servers are in `device.services` (`NQE_DEVICE_SERVICES`); the ServiceNow incidents linked to checks are read (`check-network-compliance` view `incidents`). |
| Detect | Security events, logs, packets, indicators of compromise, threat-intelligence matches | (none) | outside | Forward holds configuration and state, not event logs or traffic. |

## Investigate and triage

| Workflow | Question | Skills and views | Status | What is missing or limited |
|---|---|---|---|---|
| Investigate | Can A reach B, and where does the traffic first fail? | `investigate-reachability`, `plan-troubleshoot-connectivity` | covered | modelled from configuration: unmodelled devices and an undefined internet limit it. |
| Investigate | What is exposed to the internet? | `investigate-reachability` (from the internet), `inspect-edge` (view `public_addresses`), `inspect-topology` (kind `external`), `inspect-vulnerabilities` (input `internet_addressable`), `plan-security-posture` | partial | reachability is checked one flow at a time; `internet_addressable` is a per-device flag decided from one qualifying interface (not "open"), and the vulnerability view `devices` lists only devices with a CVE; needs an internet node, flow computation and advanced reachability (`inspect-environment`). |
| Investigate | Who peers with whom, and which neighbors advertise or hold a prefix? | `inspect-bgp-neighbors` | partial | the sessions and prefix counts as modelled; a route hijack elsewhere on the internet is not visible. |
| Investigate | A profile of a suspect device: identity, links, configuration, CVEs, performance | `plan-device-audit` | covered | the device as collected. |
| Investigate | Which device, interface or host owns this IP? | `inspect-inventory` (kinds `ip_owner`, `hosts`, `devices`) | covered | hosts are the ones Forward has seen. |
| Triage | Which devices does this CVE affect, and are they reachable? | `inspect-vulnerabilities`, `plan-vulnerability-response` | covered | a match by OS version and some configuration; reachable is not exploitable; a patch not yet collected is invisible. |
| Triage | What could a compromised device reach (blast radius)? | `investigate-reachability` (from the device's addresses), `inspect-topology` (kind `zones`) | partial | no single view from a device: `investigate-reachability` `matrix` checks many pairs in one request, but you choose the sources and destinations. |
| Triage | A health problem of unknown cause | `plan-incident-triage` | covered | the network's health, not an application incident. |

## Contain, respond and recover

| Workflow | Question | Skills and views | Status | What is missing or limited |
|---|---|---|---|---|
| Contain | Is this containment change (an ACL or route) safe, and does it block what I intend? | `plan-change-review`, `verify-change`, `edit-change-set` | covered | a prediction is a model; Predict needs the org property `PREDICT_MODELING` and a license with the Predict facet. |
| Contain | Apply the containment on the devices | (none) | outside | fwdctl never changes a device; your own tooling makes the change. |
| Respond | Who has access to Forward (roles, two-factor state, API token use), and disable a compromised Forward account | `inspect-access` (view `users`), `edit-access` | covered | Forward's own accounts, not the accounts on network devices; disabling is a write (dry run first). |
| Respond | Raise, link or close a ticket for a failing check | `edit-checks` (the ServiceNow incident) | partial | needs a configured ServiceNow integration; a dry run first. |
| Recover | Why is a snapshot bad, and how do I recover it? | `plan-snapshot-recovery`, `investigate-collection-failure`, `edit-collection`, `edit-snapshot` (action `reprocess`) | covered | collecting again or reprocessing is a write (dry run first). |
| Recover | Did a restored device come back as intended? | `compare-device-config`, `inspect-history` | partial | compares with an earlier or intended snapshot; service restoration beyond the network is not read. |

## Vulnerability and exposure management

| Workflow | Question | Skills and views | Status | What is missing or limited |
|---|---|---|---|---|
| Vulnerability | Which CVEs matter most (severity, reachable, unpatched), and what do I patch first? | `inspect-vulnerabilities`, `plan-vulnerability-response` | covered | version and configuration matching; UNCONFIRMED verdicts are unsettled. |
| Vulnerability | Which devices are past or near the end of OS support? | `inspect-inventory` (kind `lifecycle`) | covered | OS support dates only; a device with no record is unknown. |
| Vulnerability | Scanner findings next to Forward's own | `inspect-vulnerabilities` (view `scanners`), `edit-source` | partial | Tenable only, and needs the scanner integration property. |
| Exposure | Where are the business-critical assets, and what paths reach them? | `inspect-vulnerabilities` (input `threat_exposure_experimental`), `edit-network` | partial | EXPERIMENTAL: needs `THREAT_EXPOSURE`, a newer build and the security license facet. |

## Posture and compliance

| Workflow | Question | Skills and views | Status | What is missing or limited |
|---|---|---|---|---|
| Compliance | Which devices fail STIG Cat I, what CIS findings exist, which firewall rules are unused? | `check-network-compliance` (reference `library-queries.md`), `compare-nqe-results` | covered | the queries Forward ships, for the vendors they cover; an empty result or a firewall with no rule-usage data is unknown. |
| Compliance | Where do we stand against NIST CSF 2.0 or DORA? | `plan-compliance-audit` (references `nist-csf-2.md`, `dora.md`) | partial | our mapping, not an auditor's; answered by subcategory or article, never as compliant. |
| Posture | Is segmentation intact between zones or subnets? | `investigate-reachability`, `check-network-compliance` (isolation checks), `inspect-topology` (kind `zones`) | covered | what the model covers; an isolation check can pass vacuously. |
| Posture | Which cloud security groups and rules are open? | `inspect-inventory` (kind `cloud_security`) | covered | the cloud accounts Forward collects. |
| Posture | Which ACL rules are permissive (any to any)? | `plan-security-posture` (reference `permissive-rules.md`) | partial | matches ACL text in the Cisco style; PAN-OS, FortiOS and Junos policies are not matched. |
| Posture | Which firewall rules are shadowed or redundant? | `check-network-compliance` (view `shadowed_rules`) | partial | PAN-OS zone-based rules only so far (other vendors' list order and zone semantics are not verified); finds a rule whose every entry is covered by earlier rules (one rule at a time); the ACL entries carry no application, user or schedule condition, so a finding is a candidate to confirm; an any-to-any rule above others, coverage by several earlier rules and ACLs applied to interfaces are not found. |
| Posture | Which device certificates are about to expire? | (none) | outside | the NQE model has no certificate or expiry field (checked in the schema files of release 26.11.0-10), so the toolkit cannot list device certificates or their expiry; the trusted certificates of Forward itself are read by `inspect-platform`, and a certificate configured in device text can be searched with `inspect-device-files`. |
| Posture | Wireless networks (SSIDs) and network access control (802.1X) posture | (none) | outside | the NQE model carries wireless access points and WLAN interfaces only as device and interface types: no SSID, authentication or 802.1X field (checked in the schema files of release 26.11.0-10). |
| Posture | Is management access safe (telnet or http enabled, weak SNMP communities, default credentials)? | `inspect-device-files`, `check-network-compliance` (an NQE check you write) | partial | configuration text only; credentials are never readable by design. |
| Posture | Are logging, DNS, NTP and TACACS configured? | `check-network-compliance` and `author-nqe-query` over `device.services` | partial | needs `NQE_DEVICE_SERVICES` (Forward 26.11); the configuration, not the log records. |

## Evidence and reporting

| Workflow | Question | Skills and views | Status | What is missing or limited |
|---|---|---|---|---|
| Evidence | Evidence an auditor can follow: the snapshot, the query, the limits | every skill's result envelope, `plan-compliance-audit`, `fwdctl device-file export` | covered | export redacts line-based secrets only; it does not see a secret inside an XML or JSON element. |
| Evidence | Who changed what in Forward itself (its audit log), and what was touched | `inspect-access` (view `activity`) | covered | it records requests, not their bodies (who touched the device list, not which devices); it needs the audit-log permission (an organization administrator); Forward keeps the records for a short time by default (`AUDIT_LOGS_RETENTION_DAYS`, 30 days); no record is not proof that nothing happened. |
| Reporting | A written report with a summary, risk and recommendations | (none) | outside | the agent writes it from the results; there is no report generator. |

## Using the map

Pick the row for the question, run the skill in it, and carry its limits into the answer. For a *gap*, say so and offer the nearest *partial* route; for *outside*, say the toolkit does not hold that data. Where a row depends on an org property, check `inspect-environment` first: a missing feature explains missing evidence.
