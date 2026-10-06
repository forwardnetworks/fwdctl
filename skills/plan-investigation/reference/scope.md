# Scope: what Forward can and cannot answer

Forward as a data source cannot do these, and no skill should be bent to pretend it can:

1. Changes to devices, or any write to Forward beyond the `edit-*` skills (`plan-safe-write` lists them): push, apply, configure, restart, roll back. The `edit-*` skills show a dry run first and change only Forward's own data.
2. A timeline finer than the collected snapshots. History is read across the last snapshots (`inspect-history`, `plan-what-changed`) and is only as fine as the collection interval; Forward holds no events between two collections.
3. Platform management (dashboards, licensing, users, permissions, credentials). Collector and collection state can be read and a collection started or stopped; nothing else.
4. Verifying a configuration against an external policy or framework that is not expressed as a Forward check
   or an NQE query.
5. Forecasts and capacity projections.
6. Application-level performance beyond the network.
7. Live monitoring and alerting ("notify when").

Not in the model, only in configuration text: the route-map or prefix-list applied to a BGP neighbor, and configured static routes (only installed ones are modelled). Read them with `inspect-device-files` or NQE over `device.files.config` (`author-nqe-query`, reference/config-patterns.md) and say which config forms were covered.

In scope: current network state and configuration, paths and reachability, vulnerabilities and CVEs on
devices, cloud inventory, network complexity and inventory, and anything NQE can query in the twin. A
request with an in-scope part and an out-of-scope part is answered for the part that is in scope, with the
limit stated.

Ambiguous words: "fix" or "troubleshoot" means find the cause, never apply a change. "Verify" or "check"
means against current state across devices. "Write" or "generate" applied to an NQE query means produce a
query the user runs. "Utilization" and "counters" mean the collected snapshot, not live streams.
