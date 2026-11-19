# Permissive firewall rules (view `permissive_rules`)

Read when asked which firewall rules are too permissive, which rules allow any source or any destination, or which have an any-any rule. Run `{"network_id": "N", "view": "permissive_rules"}` (no policy; `limit` caps the rows shown, default 25). For vendors whose rules are written as text (Cisco style), see `plan-security-posture` reference `permissive-rules.md`; this view reads the structured rules Forward models for PAN-OS.

A rule is reported when a permit entry has, in the ACL entries Forward models:

- **any_source_any_destination_any_service**: an any source, an any destination and every protocol and port. The worst case.
- **any_source_any_destination**: an any source and an any destination with specific services.
- **any_service_from_or_to_any**: every protocol and port, with an any source or an any destination.

"Any" means an address set that covers the whole address space (0.0.0.0/0 or ::/0, or prefixes that merge to it), **or an entry with no address set at all**. Forward models some any rules with no address set (seen on a live network); a finding says which (`basis`). Treat the no-address-set basis as lower confidence and confirm it in the rule base.

A finding gives the device, the rule, the position of its first permissive entry, the tier, the basis, and how many of the rule's entries are permissive (a rule with several service combinations is several entries with one name; it is reported once, at its worst entry; `entries` is how many entries of the device share the name, for a named rule). Two rules of the same name in different virtual systems of one device would be merged: check the rule base if a name repeats.

## Limits that always apply

- A finding is a **candidate**. The ACL entries carry no application, user, URL category or schedule condition, so an any-address rule that also requires an application is narrower than it looks. Confirm each in the rule base (`inspect-device-files`) before reporting it as a risk.
- Only zone-based **PAN-OS** rules are read (the vendor must be one whose rule model was checked); a rule with no zone scope, a default rule, a rule marked over-approximated, a rule that does not permit and an entry with addresses on only one side of an IP version (`half_an_address_family`) are counted and not analyzed. A negated address, an address object Forward cannot resolve (dynamic group, external list, FQDN) and a service that uses the application default are not visible in the entries, and they can make a rule **look like** any: a false candidate unless Forward marks the entry over-approximated. The headline counts how many findings rest on the no-address-set basis; confirm those in the rule base first.
- The result is `unknown`, never ok, when nothing is found: it does not prove the rule base is tight.
- A permissive rule that an earlier rule shadows never decides anything: `shadowed_rules` says so. Whether a rule is used is a different question: see `library-queries.md` (the unused-rules query).
