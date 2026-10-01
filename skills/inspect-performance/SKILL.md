---
name: inspect-performance
description: Reads device and interface health from performance data: unhealthy devices, highest CPU, memory, utilization, loss or errors, and trends. Use when asked what is busiest or dropping packets.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "inventory-topology"
  summary: "CPU, memory, loss, errors"
  maturity: "3"
  tools: "performance"
---

# inspect-performance

## Intent

Answer "is it healthy" from measured performance data, and never from silence. Forward returns an empty list both when
everything is fine and when performance was never collected; this skill tells them apart.

## Inputs

`network_id` and `view`:

| `view` | Returns | Needs |
|---|---|---|
| `unhealthy_devices` | devices Forward marks unhealthy | nothing more |
| `unhealthy_interfaces` | interfaces of one device Forward marks unhealthy | `device` |
| `devices` | the latest reading per device, highest first | `metric` `CPU` or `MEMORY`; without one, both are returned |
| `interfaces` | the latest reading per interface, highest first | `metric` `UTILIZATION`, `PACKET_LOSS` or `ERROR`; optional `direction` `INGRESS` (default) or `EGRESS`; without a `metric`, UTILIZATION is used and the limits say so |
| `device_history` | min, max, mean, latest and a thinned series | `device`, `metric` `CPU` or `MEMORY` |
| `interface_history` | the same for one interface | `device`, `interface`, `metric` |

Optional: `days` (default 1, at most 30), `limit` (default 15, at most 100). See `schema.json`.

## Procedure

1. Ask Forward for the view.
2. Decide:
   - Readings: **ok**. Unhealthy devices or interfaces found: **failed**, with each one and Forward's reason.
   - **No samples: unknown.** A network with performance collection off, a wrong device name and a permissions gap all return
     nothing, so the skill never says "healthy" or "no problems" from an empty answer.
   - An "unhealthy" list that is empty is trusted only if raw CPU samples exist for the network; otherwise it is unknown.
3. Metric names are Forward's exact strings; a wrong one is an `error` that names the valid ones.

## Evidence

One `state` item: the view's readings, or the summary of the series.

## Output

The envelope in `schema/skill-result.schema.json`.

## Next actions

`inspect-inventory` for the devices behind a reading, `investigate-reachability` when a link looks saturated.

## Running this skill

Give the inputs as one JSON object on stdin: `echo '{"network_id": "<id>"}' | fwdctl run inspect-performance`. It needs the
`fwdctl` binary and `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD` in the environment. Read `status`
(`ok`, `failed`, `unknown`, `error`) and the `limits`; `unknown` is never a pass. Inputs are listed above and in `schema.json`.

## When there are no samples

An empty answer is `unknown`, never health. The limits say what the API shows about why: whether the organization's `performance_data`
feature is on, and the SNMP state of the network's sources: how many have SNMP collection enabled and a credential, and the last collection's result by error type (NO_RESPONSE, NOT_AUTHORIZED, ...). Forward reports a status only for a source with SNMP enabled. `unhealthy_interfaces`
has no network-wide form because Forward answers it per device: for the worst interfaces across the network use view `interfaces` with
`PACKET_LOSS`, `ERROR` or `UTILIZATION`.
