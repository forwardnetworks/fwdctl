# inspect-performance: inputs

`network_id` and `view`:

| `view` | Returns | Needs |
|---|---|---|
| `unhealthy_devices` | devices Forward marks unhealthy | nothing more |
| `unhealthy_interfaces` | interfaces of one device Forward marks unhealthy | `device` |
| `devices` | the latest reading per device, highest first | `metric` `CPU` or `MEMORY`; without one, both are returned |
| `interfaces` | the latest reading per interface, highest first | `metric` `UTILIZATION`, `PACKET_LOSS` or `ERROR`; optional `direction` `INGRESS` (default) or `EGRESS`; without a `metric`, UTILIZATION is used and the limits say so |
| `device_history` | min, max, mean, latest and a thinned series | `device`, `metric` `CPU` or `MEMORY` |
| `interface_history` | the same for one interface | `device`, `interface`, `metric` |

Optional: `days` (default 1, at most 30), `limit` (default 15, at most 100).
