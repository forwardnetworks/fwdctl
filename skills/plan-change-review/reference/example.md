# Worked example: "is it safe to block telnet on the edge firewall?"

Illustrative: the shape of a good run, with results shortened and every name made up.

**Question:** "Will adding a deny for tcp/23 on edge-fw01 break anything?"

1. `inspect-snapshots` -> newest processed collected snapshot `5120`, 3 hours old.
2. The person gave the CLI lines, so `edit-change-set` as a dry run (`plan-safe-write`): shows the one command added to `edge-fw01`. They approve; applied once, change set `cs-17` staged. Nothing touched a device.
3. `verify-change` with `run_predict` (said first: it leaves a predicted snapshot that cannot be deleted) -> predicted snapshot `5121`.
4. `verify-change` `view: verify` with expectations: "mgmt-net to dc-db tcp/1433 delivered" (kept), "internet to dc-web tcp/23 blocked" (now blocked).
5. `verify-change` `view: impact` -> two areas differ; 6 subnet pairs lost, all tcp/23.
6. `check-network-compliance` `view: read` -> no check got worse.

**Answer:** on snapshot `5120` the change blocks tcp/23 from the internet and breaks nothing listed; the 6 pairs lost are all telnet. Not measured: flows nobody listed, and traffic that depends on dynamic state. Any `unknown` above would have been reported as not measured, not as safe.
