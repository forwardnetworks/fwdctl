# Settings worth flagging when you read a device's text

Read when a device's text is open and the question is whether it is exposed or loosely set. General vendor behaviour, not checked against this repository's code. Items marked (unverified) are from memory of vendor documentation: confirm before you report them.

A hit here is a prompt to look closer, not a finding. Read the whole object it sits in, and say which file and line. To find every device with the same setting, use `check-network-compliance` or an NQE query (`author-nqe-query`), not a text search over files.

- **Cisco IOS and IOS-XE.** `ip http server` with no `access-class` leaves web management open to any source. `transport input telnet` under `line vty` enables telnet. `snmp-server community public RO` is open SNMPv1/v2c read access.
- **Cisco ASA.** `same-security-traffic permit intra-interface` allows hairpinning: intended in some designs, risky if unplanned. VPN traffic bypasses interface ACLs by default (`sysopt connection permit-vpn`); `no sysopt connection permit-vpn` turns that off, so its absence means the bypass is on.
- **Arista EOS.** `management api http-commands` with HTTPS enabled and no ACL restriction exposes the eAPI to any source that can reach it.
- **Juniper Junos.** `system services telnet` enables telnet. `snmp community public` is open SNMP read. On SRX, traffic between zones is dropped unless a policy permits it; read the zone pair's policy list, not one policy.
- **Palo Alto PAN-OS.** A rule with `application any` and `service any` and any source and destination address permits everything in that zone pair. A rule's `service` still limits the ports even when `application` is `any`: read both fields together.
- **Fortinet FortiOS.** `set logtraffic disable` on a policy means that policy's traffic is not logged. A policy that lists a UTM profile names it; the policy alone does not tell you what that profile inspects. Flow and proxy inspection modes differ in what the profiles can do (unverified).
- **Cisco NX-OS.** An ACL applied on an interface needs hardware table space; a TCAM region sized to zero for that ACL type can leave it not programmed even though it parses (unverified).
