# Worked example: "something is wrong"

Illustrative: the shape of a good run, with results shortened.

**Question:** "Users in the Chicago office can't reach the ERP since about 9am. Where do I start?"

1. `inspect-snapshots` + `inspect-collection` (view status) -> newest processed snapshot `4033`, collected 08:40; the collector is connected. The picture is **before** the symptom, so a fresh collection is worth asking for (`plan-snapshot-recovery`, `edit-collection` through `plan-safe-write`).
2. `plan-what-changed` between `4030` (yesterday) and `4033`: `chi-fw01` configuration changed (`compare-device-config`: one access-list line added); nothing else.
3. `inspect-performance` unhealthy views -> no device over threshold; samples exist, so this is "healthy", not "unknown".
4. `check-network-compliance` `{view: "read"}` -> one check newly failing on `4033`: "ERP servers reachable from branch subnets". `inspect-history` -> passed on `4030`, failing on `4033`.
5. `plan-troubleshoot-connectivity` for one Chicago host to the ERP port -> dropped at `chi-fw01` by the access-list line from step 2.

**Answer:** lead 1: the line added to `chi-fw01` between `4030` and `4033` blocks Chicago to the ERP (reachability and the failing check agree). Not measured: anything after 08:40 (the snapshot predates the symptom), and live device state. Recommend: confirm the line with the firewall owner and take a fresh collection to verify after any fix. Nothing was changed.
