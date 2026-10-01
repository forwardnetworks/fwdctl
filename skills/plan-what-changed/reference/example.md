# Worked example: "what changed since last week?"

Illustrative: the shape of a good run, with results shortened.

**Question:** "What changed in the network since last Monday?"

1. `inspect-snapshots` -> processed collected snapshots `3990` (Mon 02:00) ... `4033` (today 08:40); pick `3990` and `4033`.
2. `compare-device-config` `{before_snapshot_id: "3990", after_snapshot_id: "4033"}` -> 4 devices changed: `chi-fw01`, `dc1-sw02`, `dc1-sw03`, `lon-rtr01`. For `chi-fw01`: one line added to an access list; for `dc1-sw02/03`: the same VLAN 310 added.
3. `inspect-history` with `device: "lon-rtr01"` -> its configuration last changed between `4012` and `4015` (Wed): the only change to it in the week.
4. `find-nqe-query` "bgp neighbors" then `compare-nqe-results` between `3990` and `4033` -> one BGP neighbour on `lon-rtr01` went from established to idle.
5. `verify-change` is not needed: no change set is in play.

**Answer:** four devices changed in configuration; one BGP neighbour on `lon-rtr01` is down and the router's config changed on Wednesday, so that is the lead. A difference in the same interval is a lead, not a cause. Not measured: anything between collections and settings Forward does not collect. Nothing was changed.
