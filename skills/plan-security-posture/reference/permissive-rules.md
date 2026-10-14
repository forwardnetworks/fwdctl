# Finding permissive ACL rules across devices

Read when the question is "which devices have an any-to-any permit". A hit is a lead: read the rule and the object it sits in before reporting it.

This query matches the text of the rule's configuration line, so it sees the rule syntax that spells `permit <protocol> any any` (Cisco IOS, IOS-XE, NX-OS and ASA style, Arista EOS). Vendors that store rules as structured objects (PAN-OS, FortiOS, Junos policies) do not match and are not covered: say so, and read those with `inspect-device-files` (`fwdctl describe inspect-device-files reference/footguns.md`). A device with no match is "not found by this query", never "clean".

```nqe
foreach device in network.devices
foreach entry in device.aclEntries
where entry.action == AclAction.PERMIT
let text = sourceConfigText(entry.name)
where matches(text, "*permit * any any*")
select {
  Device: device.name,
  ACL: entry.name,
  Line: text
}
```

Run it with `fwdctl nqe run --network ID --file permissive.nqe --limit 20 --format table`, or check it first with `fwdctl nqe lint --org permissive.nqe` (against the organization's live schema).

- **Read the line.** `permit ip any any` is fully open. `permit tcp any any eq 22` has any source and destination address but one port; the port is on the line.
- **Defined is not applied.** An ACL counts only where something uses it (an interface binding, a class map). The query lists definitions; check the binding before calling the exposure real.
- **Cost.** Reading the config text of every ACL entry is heavy on a network of thousands of devices (a run against one timed out at 250 seconds; a small network took seconds). Narrow it first, for example with `where device.name == "NAME"` or a name pattern.
- **Checked** with the offline and the live-schema lint, and by running it against small real networks, where it returned the expected `permit ip any any` and `permit icmp any any` lines. Not run against a firewall-vendor network.
