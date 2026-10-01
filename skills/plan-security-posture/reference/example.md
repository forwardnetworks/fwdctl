# Worked example: "are we exposed?"

Illustrative: the shape of a good run, with results shortened.

**Question:** "Give me a security review of the network."

1. `inspect-snapshots` -> snapshot `4021`, 3 hours old.
2. `check-network-compliance` `{view: "read"}` -> 61 checks, 4 failing: "no telnet" (7 devices), "SSH v2 only" (2), "management ACL present" (1), "NTP configured" (12).
3. `inspect-vulnerabilities` -> 9 CVEs expose devices; 2 are known-exploited; 1 of those is on an internet-addressable firewall.
4. `investigate-reachability` `{from: "internet", dst_ip: "10.0.50.10", protocol: "tcp", dst_port: "22"}` for the management range -> **delivered** to `dc1-edge02`: a finding. The same flow to `10.0.60.0/24` databases is blocked.
5. `inspect-topology` `{kind: "external"}` -> the internet node is modelled with one uplink; no intranet or L3 VPN nodes, so exposure through partner links is **not measured**.
6. `inspect-device-files` on `dc1-edge02`, search `transport input` -> telnet enabled on vty 0-4.

**Answer:** ranked findings: (1) SSH from the internet reaches `dc1-edge02`, which also runs telnet; (2) a known-exploited CVE on an internet-addressable firewall; (3) four failing checks. Unknown, not clean: partner links (not modelled), devices Forward does not collect. Recommendations only; nothing was changed.
