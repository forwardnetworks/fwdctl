# Worked example: a dropped flow

Illustrative: the shape of a good run, with results shortened. Names and numbers are made up.

**Question:** "Why can't 10.1.1.5 reach the database 10.2.2.9 on tcp/5432?"

1. `inspect-snapshots` -> newest processed collected snapshot `4021`, 3 hours old. Fine: the problem was reported this morning.
2. The endpoints are addresses already; no `inspect-inventory` needed. Flow: tcp, dst port 5432.
3. `investigate-reachability` `{dst_ip: "10.2.2.9", src_ip: "10.1.1.5", protocol: "tcp", dst_port: "5432"}` -> `failed`, first failure at `dc1-fw01`: denied by access list `DMZ-IN`, rule 40. Two paths; both end at `dc1-fw01`.
4. `inspect-device-files` on `dc1-fw01`, search `DMZ-IN` -> rule 40 `deny tcp any any eq 5432` (added after rule 30 permits the app subnet only).
5. `plan-what-changed` -> `inspect-history` with `device: dc1-fw01` -> its configuration last changed between snapshots `4018` (Tue 02:10) and `4021`.
6. Reverse flow and a sibling destination: the reverse flow is also dropped; `10.2.2.8` on 5432 is delivered, so the block is specific to `10.2.2.9`.

**Answer (what the agent says):** the flow is dropped at `dc1-fw01` by rule 40 of `DMZ-IN` (snapshot `4021`, 3 h old). The firewall's configuration changed between snapshots `4018` and `4021`; I can't say which line or who from Forward alone. Not measured: anything between the two collections, and whether the application itself listens. Recommend: review rule 40 with the firewall owner. Nothing was changed.
