# NIST Cybersecurity Framework 2.0: what Forward can evidence

Read when an audit or question names the NIST CSF (also written "NIST2"). The subcategory text is NIST's, from the CSF 2.0 Core download (<https://csrc.nist.gov/projects/cybersecurity-framework/filters>, <https://www.nist.gov/cyberframework>), regenerated with `scripts/gen-csf-reference.py`. **The mapping to skills is ours, not NIST's and not an auditor's:** judgment about what a network model can show.

## How to use it

- CSF has no pass or fail and no control that a tool can certify. A subcategory is met by the organization's practice, of which the network is one part. Never answer "compliant with the CSF"; answer which subcategories Forward evidences, with what, and what the evidence does not show.
- **evidence** means Forward reads data that answers the network-device slice of the subcategory. **partial** means part of it, or only configuration text, or judgment. A subcategory not in the tables is **not evidenced by Forward**: say so, and do not invent coverage.
- Evidence from a check, a query or a CVE match is only as good as its scope: a check that returned no rows because nothing matched its scope is **unknown**, not met (see `check-network-compliance`).
- An "evidence" level holds only when the feature or check behind it exists in that organization and build (an org property that is off, a check nobody defined, a Preview route an older build lacks); say which was missing instead of reporting the subcategory as met or not met.
- Name the snapshot the evidence is from. The CSF informative references (NIST SP 800-53, ISO/IEC 27001 and others) are in NIST's Core download; map from a subcategory to them there, not from memory.

## GOVERN (GV)

Not evidenced by Forward: GV.OC Organizational Context (01, 02, 03, 04, 05); GV.RM Risk Management Strategy (01, 02, 03, 04, 05, 06, 07); GV.RR Roles, Responsibilities, and Authorities (01, 02, 03, 04); GV.PO Policy (01, 02); GV.OV Oversight (01, 02, 03); GV.SC Cybersecurity Supply Chain Risk Management (01, 02, 03, 04, 05, 06, 07, 08, 09, 10).

## IDENTIFY (ID)

| Subcategory | NIST text | Level | Skills and views | What the evidence does not show |
|---|---|---|---|---|
| ID.AM-01 | Inventories of hardware managed by the organization are maintained | partial | `inspect-inventory` kind `devices`, `summary` | devices Forward collects only; unmanaged hardware, hosts it has not seen and anything off the network are not in it. |
| ID.AM-02 | Inventories of software, services, and systems managed by the organization are maintained | partial | `inspect-inventory` kind `devices` (OS and version), `cloud_instances` | network device OS and cloud instances; applications and services on servers are not read. |
| ID.AM-03 | Representations of the organization's authorized network communication and internal and external network data flows are maintained | partial | `inspect-topology` kinds `links`, `zones`, `maps`; `check-network-compliance` (isolation and reachability checks) | Forward shows the network as it is. What is *authorized* is only what someone wrote as a check; no check, no statement. |
| ID.AM-08 | Systems, hardware, software, services, and data are managed throughout their life cycles | partial | `inspect-inventory` kind `lifecycle` | OS support dates from Forward's lifecycle data only; a device with no record is unknown, and hardware end-of-sale is not read. |
| ID.RA-01 | Vulnerabilities in assets are identified, validated, and recorded | evidence | `inspect-vulnerabilities` (network, one CVE, one device) | CVE matches by OS version (and some configuration); a patch not yet collected is invisible, a match is not exploitability, UNCONFIRMED and UNIMPLEMENTED verdicts are unsettled, platforms without CVE analysis are not covered, and "validated, recorded" is a human step. |
| ID.RA-04 | Potential impacts and likelihoods of threats exploiting vulnerabilities are identified and recorded | partial | `investigate-reachability` from the internet, `inspect-vulnerabilities` with `internet_addressable`, and its input `threat_exposure_experimental` | exposure of devices and paths; likelihood and business impact are not computed. `internet_addressable` needs an internet node and advanced reachability; threat exposure is EXPERIMENTAL, needs the org property THREAT_EXPOSURE (off by default), a newer Forward build and the security licence facet. |
| ID.RA-07 | Changes and exceptions are managed, assessed for risk impact, recorded, and tracked | partial | `verify-change` (predict before a change), `plan-change-review`, `inspect-history` | changes to the network and their predicted effect; the change process and approvals are not visible. |

Not evidenced by Forward: ID.AM Asset Management (04, 05, 07); ID.RA Risk Assessment (02, 03, 05, 06, 08, 09, 10); ID.IM Improvement (01, 02, 03, 04).

## PROTECT (PR)

| Subcategory | NIST text | Level | Skills and views | What the evidence does not show |
|---|---|---|---|---|
| PR.AA-03 | Users, services, and hardware are authenticated | partial | `check-network-compliance` (NQE checks you write; STIG and CIS catalog queries where their rows cover it), `inspect-device-files`, the `device.services` TACACS servers in NQE (Forward 26.11) | how network devices authenticate administrators, from configuration; what a STIG or CIS query covers must be read from its rows, not assumed; user, service and workload authentication elsewhere is not read. |
| PR.AA-05 | Access permissions, entitlements, and authorizations are defined in a policy, managed, enforced, and reviewed, and incorporate the principles of least privilege and separation of duties | partial | `check-network-compliance` and `inspect-device-files` on ACL and firewall policy | access control on the network (ACLs, firewall rules); user and entitlement management in other systems is not read. |
| PR.DS-02 | The confidentiality, integrity, and availability of data-in-transit are protected | partial | `check-network-compliance` (NQE checks you write for SSH, telnet and TLS settings; STIG and CIS catalog queries where their rows cover it), `inspect-device-files` | configuration that enables protection of data in transit (for example telnet disabled); Forward does not see traffic, and a topic with no check is not evidenced. |
| PR.PS-01 | Configuration management practices are established and applied | evidence | `check-network-compliance` (predefined checks, STIG and CIS queries, your own NQE), `compare-device-config`, `inspect-history` | device configuration against a baseline someone defined: no check or baseline defined means nothing is evidenced. CIS queries need the org property CIS_BENCHMARKS (off in 26.9 and 26.10, on from 26.11; Forward plans to retire it). "Established and applied" as a practice is not visible. |
| PR.PS-02 | Software is maintained, replaced, and removed commensurate with risk | partial | `inspect-inventory` kind `lifecycle`, `inspect-vulnerabilities` | OS version and support state; patch process and software outside the network devices are not read. |
| PR.PS-04 | Log records are generated and made available for continuous monitoring | partial | `inspect-device-files` (logging configuration); `check-network-compliance` or `author-nqe-query` over the `device.services` log servers (Forward 26.11) | that logging is configured and where it goes; `device.services` is absent in 26.10, off in 26.11.0-01 and on from 26.11.0-10 (org property NQE_DEVICE_SERVICES); the log records themselves are not read. |
| PR.IR-01 | Networks and environments are protected from unauthorized logical access and usage | evidence | `investigate-reachability`, `check-network-compliance` (isolation checks), `inspect-topology` kind `zones`, `plan-security-posture` | reachability as modelled from configuration: strong for what the model covers, silent on what it does not (unmodelled internet, unsupported devices). "Unauthorized" is only what a check or zone policy states, and an isolation check can pass vacuously; see each skill's limits. |
| PR.IR-03 | Mechanisms are implemented to achieve resilience requirements in normal and adverse situations | partial | `investigate-reachability` (is there more than one path between the endpoints) | redundancy of paths between named endpoints; failover behaviour and timing are not simulated here. |
| PR.IR-04 | Adequate resource capacity to ensure availability is maintained | partial | `inspect-performance` (CPU, memory, interface utilization, loss and errors) | capacity signals Forward polls from devices and links; capacity planning and the adequacy of resources are not computed. |

Not evidenced by Forward: PR.AA Identity Management, Authentication, and Access Control (01, 02, 04, 06); PR.AT Awareness and Training (01, 02); PR.DS Data Security (01, 10, 11); PR.PS Platform Security (03, 05, 06); PR.IR Technology Infrastructure Resilience (02).

## DETECT (DE)

| Subcategory | NIST text | Level | Skills and views | What the evidence does not show |
|---|---|---|---|---|
| DE.CM-01 | Networks and network services are monitored to find potentially adverse events | partial | `inspect-performance`, `inspect-collection`, `inspect-bgp-neighbors` | device and link health Forward reads at collection and performance polls, and whether each device is collected; this is periodic, not continuous, BGP state is as of the collection, and security event monitoring is outside it. |
| DE.AE-04 | The estimated impact and scope of adverse events are understood | partial | `verify-change` view `impact`, `investigate-reachability` | what a change or a compromised device could reach; an actual incident's scope needs logs Forward does not hold. |

Not evidenced by Forward: DE.CM Continuous Monitoring (02, 03, 06, 09); DE.AE Adverse Event Analysis (02, 03, 06, 07, 08).

## RESPOND (RS)

| Subcategory | NIST text | Level | Skills and views | What the evidence does not show |
|---|---|---|---|---|
| RS.AN-03 | Analysis is performed to establish what has taken place during an incident and the root cause of the incident | partial | `plan-incident-triage`, `plan-what-changed`, `inspect-history` | what changed in the network and when; the cause of an incident outside the network is not visible. |
| RS.AN-08 | An incident's magnitude is estimated and validated | partial | `verify-change` view `impact`, `investigate-reachability` | the reach of a device or change in the network (a bound on the magnitude); an incident's real magnitude needs logs and data Forward does not hold. |
| RS.MI-01 | Incidents are contained | partial | `verify-change` (predict a containment ACL or route change before applying it) | the predicted effect of a containment change on reachability; applying it is a separate, gated write. |

Not evidenced by Forward: RS.MA Incident Management (01, 02, 03, 04, 05); RS.AN Incident Analysis (06, 07); RS.CO Incident Response Reporting and Communication (02, 03); RS.MI Incident Mitigation (02).

## RECOVER (RC)

| Subcategory | NIST text | Level | Skills and views | What the evidence does not show |
|---|---|---|---|---|
| RC.RP-05 | The integrity of restored assets is verified, systems and services are restored, and normal operating status is confirmed | partial | `compare-device-config`, `inspect-history` | that a restored device's configuration matches an earlier or intended snapshot; service restoration beyond the network is not read. |

Not evidenced by Forward: RC.RP Incident Recovery Plan Execution (01, 02, 03, 04, 06); RC.CO Incident Recovery Communication (03, 04).

22 of 106 subcategories are mapped. The rest are ones judged not evidenced by a network model (mostly policy, governance, people, physical, supplier and data-handling practices); that judgment is ours and can be wrong in a particular network, for example where a device configuration does carry the evidence.
