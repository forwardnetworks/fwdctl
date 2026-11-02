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

## Read before relying on a result

### When there are no samples

An empty answer is `unknown`, never health. The limits say what the API shows about why: whether the organization's `performance_data`
feature is on, what stops SNMP polling (the network's performance setting, whether an SNMP credential exists, each source's collection and SNMP-collection flags, with the reason a source is excluded; necessary, not sufficient, because the device's vendor OS and the collector that serves it are not checked), and the SNMP state of the network's sources: how many have SNMP collection enabled and a credential, and the last collection's result by error type (NO_RESPONSE, NOT_AUTHORIZED, ...). Forward reports a status only for a source with SNMP enabled. `unhealthy_interfaces`
has no network-wide form because Forward answers it per device: for the worst interfaces across the network use view `interfaces` with
`PACKET_LOSS`, `ERROR` or `UTILIZATION`.
