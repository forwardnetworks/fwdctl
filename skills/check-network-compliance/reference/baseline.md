# Baseline review prompts for a device's configuration

Read when asked for a general review of a device or network and there is no policy to check against. These are prompts for what to look at, not findings: a missing item can be a deliberate design, or done elsewhere (in a template, by a central policy, on another device). General practice, not checked against this repository's code; the absence of a line is **unknown** until you have read the whole config and the places a setting can hide (`fwdctl describe inspect-device-files reference/vendor-reading.md`).

To check a baseline across many devices, write it as a check (`edit-checks`, or an NQE query with a `violation` column through `author-nqe-query`) instead of reading devices one by one.

- **Management plane.** Remote access limited by source and encrypted (SSH, not telnet); SNMP v3 or a restricted community; AAA with a fallback and command accounting; a login banner; timeouts on idle sessions; logging to a remote host with synchronised time (NTP from more than one source).
- **Control plane.** Routing protocol authentication; BGP `maximum-prefix` limits and inbound prefix filtering on external peers (RFC 7454); GTSM or `ttl-security` on external sessions; BFD where fast failure detection is wanted; control-plane policing where the platform supports it.
- **Data plane.** Reverse-path filtering at the edge (RFC 3704); unused interfaces shut down; spanning-tree protection on access ports (`bpduguard`, `portfast`) and storm control; unused services off (`ip http server`, finger, small servers).
- **Routing hygiene.** Default routes and redistribution deliberate and filtered; no unintended summarisation; route policy that ends in an explicit action.
- **Cloud.** Security groups, network ACLs and route tables read as data (`fwdctl describe inspect-device-files reference/vendor-reading.md`, "Cloud is data, not CLI").

Say which of these you could read, and which you could not.
