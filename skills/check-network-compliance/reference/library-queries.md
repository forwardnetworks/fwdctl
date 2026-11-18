# Forward's security queries: STIG, CIS and unused firewall rules

Read when the question is "which devices fail STIG Cat I", "what CIS findings do we have, and which are new", or "which firewall rules are unused". These are saved queries in Forward's own NQE library (find them with `find-nqe-query`, repository `fwd`, directory `/Security`). Run them with `fwdctl nqe run --query-id`, not as a `check-network-compliance` policy: that skill counts a row as a violation only when `violation` is true and answers **unknown** when any row is false or empty ("the rows are not all violations"), which is the case for all of these queries (STIG and CIS rows are mostly not violations, and the unused-rules query has one row per firewall, true or false).

**Measured** on a Forward 26.11.0-14 organization with read-only runs: the query IDs below are the ones the library holds there (`FQ_` plus the hash; the same hashes are in the library source of 26.11.0-10, where the files show a `Q_` prefix). If an ID answers 404 on another build, resolve it by path with `find-nqe-query`.

## The queries

| Question | Path | Query id | One row is |
|---|---|---|---|
| All STIG rules | `/Security/STIGs/STIG Findings` | `FQ_602c90895b90161ff2f0a61b61f77ec714635b02` | a device and a rule |
| STIG Cat I (high) | `/Security/STIGs/STIG Findings - Cat I` | `FQ_c3e31d389e8d9d73c55a7f5833e4628c0a835aa3` | a device and a rule |
| STIG Cat II (medium) | `/Security/STIGs/STIG Findings - Cat II` | `FQ_3f1972b35e637189a898ca9925cde53cda5dd550` | a device and a rule |
| STIG Cat III (low) | `/Security/STIGs/STIG Findings - Cat III` | `FQ_3031f6401e73840cdf66a33b200876bc7ac2ab29` | a device and a rule |
| CIS findings | `/Security/CIS/CIS Findings` | `FQ_581031da146a962b5e93ccdea436abf5c555743d` | a device, a benchmark and a rule |
| CIS rule catalog | `/Security/CIS/CIS Benchmark Rules` | `FQ_fd42795968223cb234a167f88b28e851075fc350` | a benchmark rule (no device) |
| Firewalls with unused rules | `/Security/Firewalls with Unused Security Rules` | `FQ_198425bad25e62b8dcba70d7cfd68631bf60242e` | an active firewall |

## STIG

Columns (from the library source): `violation` (true, false, or null when the rule was skipped for that device), `Outcome` (`Open`, `Not a Finding` or `Not Reviewed`, as STIG Viewer words them), `Severity` (`1 - high` for Cat I), `Device`, `Vulnerability ID`, `Rule ID`, `Rule Version`, `STIG`, `STIG Version`, `Intent`, `Description`, `Verification`, `Remediation`, `Tags`, `OS`, `Model`, `Management IPs`, `Management MACs`, `Available parameters`.

- **Every rule is a row, not only failures.** `Open` is the finding. `Not Reviewed` is a rule the library marks as skipped for the device, or a check that returned null: it is neither a pass nor a failure. A device whose OS or model matches no rule, or that has no configuration file, gets **no row at all** for that rule. `Not a Finding` is a pass for that rule.
- **Vendors covered** are the folders under `/Security/STIGs`: Arista, Cisco, F5, Juniper and Palo Alto Networks. A device from another vendor, or one with no collected configuration, has no rows, which is not the same as compliant. Say which devices have any `Not a Finding` or `Open` row at all.
- **Juniper:** by default an SRX gets the SRX STIGs, a JunOS device whose model starts with `ex` the EX Series STIGs, and any other JunOS device the Router STIGs. The device tags `JunosEXSeries`, `JunosRouter` and `JunosSRX` override that.
- **Parameters:** some rules take values from the organization's STIG policy (`Available parameters` names them); a wrong parameter changes the outcome of a rule. The queries read the policy from NQE (`network.stigDatabase.policy`); `edit-data-file` refuses the STIG policy file, so fwdctl neither reads nor changes it here.

Counts (do these first; they cost one cheap call):

    fwdctl nqe run --network N --query-id FQ_c3e31d389e8d9d73c55a7f5833e4628c0a835aa3 --count-by Outcome

Which devices have an Open finding, and how many rules each:

    fwdctl nqe run --network N --query-id FQ_c3e31d389e8d9d73c55a7f5833e4628c0a835aa3 --format jsonl > cat1.jsonl
    jq -r 'select(.violation == true) | .Device' cat1.jsonl | sort | uniq -c | sort -rn

Answer in devices and rules, never in rows: say how many devices have an Open Cat I rule, how many rules are Open, how many are Not Reviewed, and how many devices were in scope (distinct `Device` values). A run on a demo network returned 512 Cat I rows for 30 devices: 183 Open, 135 Not a Finding, 194 Not Reviewed; reading "512 findings" from that would have been wrong. The query returns up to `--max` rows (default 50,000); the all-rules query is the largest (a demo network gave about 16,800 rows).

**New since the last snapshot:** run the query on each snapshot and compare the Open set exactly (a rule that turned Open is in the later set and not the earlier one):

    fwdctl nqe run --network N --query-id FQ_c3e31d389e8d9d73c55a7f5833e4628c0a835aa3 --snapshot BEFORE --format jsonl > before.jsonl
    fwdctl nqe run --network N --query-id FQ_c3e31d389e8d9d73c55a7f5833e4628c0a835aa3 --snapshot AFTER --format jsonl > after.jsonl
    jq -r 'select(.violation == true) | [.Device, ."Vulnerability ID"] | @tsv' before.jsonl | sort > before.txt
    jq -r 'select(.violation == true) | [.Device, ."Vulnerability ID"] | @tsv' after.jsonl | sort > after.txt
    comm -13 before.txt after.txt

This has no row cap and does not depend on how Forward matches rows. `compare-nqe-results` also takes a saved query id and two snapshots, but it shows at most 100 differences (the counts by type cover only those), and the STIG queries declare no `@primaryKey`, so Forward infers the row key from the data: a rule that turned Open can appear as a modified row rather than an added one. Use it for a small change, count added plus modified rows whose later row has `violation` true, and say the diff behaviour is unverified here (not run against two processed snapshots).

## CIS

`CIS Findings` has `@primaryKey(Device, Benchmark, Rule)`, so a rule that flips between snapshots shows as a modified row in `compare-nqe-results`; columns include `violation`, `Profile`, `Implementation Status`, `Description`, `Rationale`, `Audit`, `Remediation`, `OS`, `Tags` and `RuleId`. `CIS Benchmark Rules` lists the catalog (a demo network showed 411 rules across several benchmarks, among them Cisco IOS XE 16.x and 17.x and Palo Alto Firewall 11).

- **The org property `CIS_BENCHMARKS` must be on** (`inspect-environment`, the feature row with `topic: security`; off in 26.9 and 26.10.0, on from 26.10.6 and in 26.11). When it is off the NQE fields `network.cisDatabase` and `device.cisFindings` do not exist.
- **An empty `CIS Findings` is unknown, not compliant.** On a 26.11 organization with the property on, the catalog returned 411 rules and the findings returned **zero rows** for a processed snapshot: a possible cause is that a snapshot processed before the property was on has no findings (Forward's note on feature flags says a flag turned on after processing may not give that snapshot the new data), another that no benchmark covers the vendors and versions present. This is a hypothesis: the output does not say which. Check when the snapshot was processed against when the property was turned on, and ask the person before reprocessing it (`edit-snapshot`, a dry run first), because a reprocess is not shown here to fill the findings.

## Unused firewall rules

The query needs one parameter, `expectedUnusedRules` (a list of rule names that are meant to be idle, such as a failover rule); pass an empty list for none:

    fwdctl nqe run --network N --query-id FQ_198425bad25e62b8dcba70d7cfd68631bf60242e --param 'expectedUnusedRules=[]' --format jsonl

One row per active firewall (devices whose HA mode is BACKUP or STANDALONE_INACTIVE are skipped): `violation` is true when any rule last processed a packet more than 30 days before the device's collection, `Count of Unused Rules`, `Unused Rules`, `Count of New Rules` (created in the last 30 days), `New Rules`, `Count of ACL Entries`. Every row has a `violation` of true or false, but `check-network-compliance` would still answer unknown unless every firewall is true, so read the rows yourself.

- **Zero unused rules can mean no usage data.** The query counts a rule only when the device reports when it was last used or created. On a demo network with four PAN-OS firewalls it returned `violation` false and 0 unused for all four while **none** of their 44 rules had either timestamp, so nothing was proven. Check coverage before saying "no unused rules":

      foreach device in network.devices
      where device.platform.deviceType == DeviceType.FIREWALL
      select { device: device.name, total: length(device.aclEntries),
               withLastUsed: length(foreach e in device.aclEntries where isPresent(e.lifecycleData?.lastUsed) select e) }

  If `withLastUsed` is 0 for a firewall, say its rules' usage is not collected and the answer is **unknown** for it.
- A rule can appear as several ACL entries; the query counts distinct rule names.
- Times are measured against the device's own collection time, so a firewall collected long ago is judged as of then.

## Answering

State the snapshot and the query (path and id). Report devices, rules and outcomes separately from rows. Treat `Not Reviewed`, a vendor with no rows, an empty result and a firewall with no usage data as **unknown**, never as compliant. Say which vendors the query covers and which devices fall outside it. To make one of these a standing check you must filter it to violations first (see `plan-compliance-audit` and `edit-checks`).
