# Shadowed firewall rules (view `shadowed_rules`)

Read when asked which firewall rules are shadowed, redundant, or can be removed or reordered (which rules are never hit is a different question: see the end). Run `{"network_id": "N", "view": "shadowed_rules"}` (no policy and no other input; `limit` caps the rows shown, default 25).

A rule is **shadowed** when earlier rules match every packet it matches, so it never decides anything they have not already decided. The view reads each PAN-OS device's ACL entries as Forward models them and compares each entry with the entries before it, **for the rule as a whole**: Forward models a rule with several service combinations (for example tcp/80 and udp/53) as several entries with one name, so a rule is reported only when **every** one of its entries is analyzed and covered by an earlier entry of **another** rule. Entries of one rule never cover each other. A rule with only some entries covered is counted (`rules_partly_covered`) and not reported: removing it would break the traffic of its other entries. A finding names the covering rules:

- **redundant**: every covering rule has the same action. Removing the later rule changes nothing the model shows.
- **ineffective**: every covering rule has the opposite action, so the later rule never applies. This is the more serious finding: the author probably meant the later rule to act.
- **mixed**: the covering rules include both actions.

The result is `failed` with confidence `inferred` when there are findings, and **`unknown`** when there are none (it is never a pass: see the limits). Each finding gives the device, the rule, the position of its first entry and how many entries it has, the covering rules with their positions and actions, and the kind.

## What the model holds, and what that means for the answer

Read from Forward's source (AclNatFetcherUtils on release/26.11.0-10) and checked against live results:

- A device's `aclEntries` is an **ordered list**; the position is the order. The view asks for the position explicitly.
- `headerMatches` is the layer 3 and 4 match: address sets per IP version (a list of CIDR prefixes; **empty for an IP version the entry does not match**), protocol, source and destination port, ICMP type and code (ranges; an unconstrained field is the full range, so these are never empty on a real rule), and `overApproximated`.
- `metadataMatches` has the ingress and egress **interfaces**, filled from the rule's zones, and **empty for an ACL that is applied separately to interfaces**. Entries of several such ACLs share one flat list with no ACL name, so they cannot be compared.
- The ACL entries carry **no application, user, URL category or schedule** condition, and no rule type. (`device.securityPolicy` has them, but it is EXPERIMENTAL, off by default, and this view does not read it.) A PAN-OS rule is universal, intrazone or interzone, and the entries give the same zone sets for all three.
- A rule with several service combinations is **several entries with one name**, and an entry is one product of the match fields.

So a finding is a **candidate**, not proof. An earlier rule that also requires an application or a user is narrower than its modelled match: the later rule may not be shadowed at all. Confirm each candidate in the rule base (`inspect-device-files`) before removing anything, and test a removal with `verify-change` or `edit-change-set` first.

## What is analyzed, and what is counted but left out

Only **PAN-OS** devices are read and compared so far (the query filters on the vendor, and `vendors_not_compared` counts the configured rules of every other vendor's devices, by operating system: a position in the list is not first-match order everywhere, for example FortiOS may be ordered by policy id, an SRX evaluates global policies after zone-pair policies, and a Check Point layer does not end at a permit). Of those, only **zone-based rules** are compared: entries that are configured (not implicit), have both ingress and egress interfaces, are not default rules, are not marked over-approximated, have a permit or deny action, and say which IP version they match. The result states how many of each: `rules_without_zone_scope`, and a `skipped` count by reason (`default_rule`, `over_approximated`, `action_not_permit_or_deny`, `no_ip_version`, `half_an_address_family`, `not_understood`).

- A rule with **no zone scope**, or an entry whose four address sets are all empty (it does not say which IP version it matches; seen on "deny all" and "any" rules), is not analyzed, so **an any-to-any rule above other rules is not detected**. Say so when the answer is "none found".
- **Zone types:** because the entries do not say whether a PAN-OS rule is universal, intrazone or interzone, an earlier rule is trusted only for the zone pairs every type matches: a rule whose ingress and egress interface sets are equal and hold more than one interface shadows nothing, and an earlier rule whose sets overlap shadows a later rule only if the later one has no same-zone pair. The sets are **interfaces**, not zone names, so a single zone with several interfaces is treated as several zones (conservative). The built-in intrazone default is modelled with equal ingress and egress sets, one entry per zone; how a *configured* intrazone or interzone rule is modelled was not measured, because the measured network has none.
- A device with more than 3,000 analyzable rules is not compared (the cost grows with the square of the rule count); it is named in the limits.

## Limits that always apply

- **Only coverage by a single earlier rule is found.** A rule covered by the union of two or three earlier rules (for example two adjacent halves of a network written as separate rules) is not reported. Address sets, protocol and port ranges **within one rule** are merged exactly, so a rule written with several prefixes or ranges is handled.
- Not visible in the ACL entries, so unchecked: whether a **disabled** rule is listed (a disabled earlier rule shadows nothing), the order of **Panorama** pre, local and post rules, **negated** addresses and services that use the application default, and addresses Forward cannot resolve (dynamic address groups, external lists, FQDN objects). Those can leave an **earlier** rule modelled wider than configured (a false finding) or a **later** rule modelled narrower (it can look covered when it is not), unless Forward marks the entry over-approximated.
- "None found" means none among the analyzed rules by this method. It does not prove the rule base is clean.
- Order is Forward's modelled order. Whether a rule is **used** is a different question (hit counts and last-used times, when the device reports them): see the unused-rules query in the skill's other reference note on the library queries.

## Measured

On a Forward 26.11.0-14 demo network the view ran both queries live: 18 PAN-OS rules on 3 devices, 15 with a zone scope, 11 analyzed, 4 skipped as having no IP version, 5 rules on a Cisco IOS device not compared, no finding, so `unknown`. PAN-OS is the only vendor measured. Rules becoming several entries is the norm there: one device has 6 entries under 2 names, and up to 8 entries share one name. It has not been run against a network with a known shadowed rule; the analysis is tested on fixtures shaped like the real results (CIDR-string lists, `{start,end}` ranges, interface name lists) and on the exact set arithmetic.

## Answering

Say how many rules were configured, how many were analyzed and why the rest were not; list the candidates with the covering rule; say redundant or ineffective for each; state that a candidate must be confirmed (application, user and schedule conditions are not in the model); and say what the method cannot see (union coverage, any rules, ACLs applied to interfaces).
