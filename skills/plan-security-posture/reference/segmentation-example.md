# Worked example: "are guest and server zones isolated?"

Illustrative: the shape of a good run, with results shortened and every name made up.

**Question:** "Is the guest VLAN isolated from the servers?"

1. `inspect-snapshots` -> `5120`, 3 hours old.
2. Intent from the person: guest `10.50.0.0/16` must not reach servers `10.20.0.0/16` on any port; guests must reach DNS `10.20.0.53` udp/53. `inspect-topology` kind `zones` lists the firewall's zones `GUEST` and `SERVERS`.
3. `investigate-reachability` guest host to `10.20.4.10` tcp/443 -> `security_denied` at `fw-int-01`, rule `guest-to-any`. Guest to `10.20.0.53` udp/53 -> delivered.
4. Bulk: an NQE check for guest-to-server permits -> 0 violations across 18 in scope (a populated scope).
5. After a change: `verify-change` with the same flows as expectations.

**Answer:** each flow: expected, observed, snapshot, and for a violation the first rule responsible. Not tested: other zones and protocols. A passing isolation check is only as good as its flows: see `reference/check-vacuity.md`.
