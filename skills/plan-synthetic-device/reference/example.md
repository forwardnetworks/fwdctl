# Worked example: "paths stop at the provider core"

Illustrative: the shape of a good run, with results shortened and every name made up.

**Question:** "Traffic from branch-2 to the data center dead-ends at pe-edge-1."

1. `inspect-snapshots` -> `5120`. `investigate-reachability` branch-2 host to `10.9.0.10` -> ends at `pe-edge-1`, no next hop. `inspect-topology` kind `external` -> no node models the provider core.
2. Type: the segment carries many sites over a provider's private network, so an intranet node (not the internet), per `reference/types.md`.
3. `inspect-inventory` and `inspect-device-files` -> uplink port, gateway interface and VLAN for each of the 6 sites.
4. Six rows, so a query: authored and linted (`author-nqe-query`); committed to the library (visible to the whole organization, said before saving; `edit-nqe-query` dry run, approved).
5. `edit-synthetic-query` dry run to preview: 6 connections, no error; approved, attached.
6. New snapshot processed; the same flow now crosses the node and is delivered.

**Answer:** the type and why, the connection count and a few rows, the preview, what paths can now do, and what is not verified (the real provider's behaviour beyond what was described). Never changes a device.
