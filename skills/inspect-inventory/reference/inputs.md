# inspect-inventory: inputs

`network_id` and `kind`:

| `kind` | Returns | Narrow with |
|---|---|---|
| `summary` | counts of devices, interfaces, VLANs, VRFs, hosts and cloud accounts; devices by vendor and by device type | nothing |
| `devices` | name, vendor, OS and version, model, type, location, tags | `device` (exact name), `name` (a case-insensitive substring of the name; no `*` or `?`), `vendor` (Forward's vendor name, such as `FORTINET`; also on `security_rules_experimental`) |
| `interfaces` | device, name, type, admin and oper status, MTU, speed, MAC, IPv4 addresses (subinterfaces **and routed-VLAN/SVI interfaces**), the VRFs, description | `device`, `name` (interface) |
| `vlans` | device, VLAN name, VLAN id range | `device`, `name` (VLAN) |
| `vrfs` | device, VRF name, type, interface count | `device`, `name` (VRF) |
| `hosts` | device, host name, type, addresses, MAC, interfaces | `device`, `name` (host) |
| `routes` | the forwarding table Forward modelled: device, VRF, prefix, origin protocol, next hop, interface, next-hop type (one row per next hop); the evidence also says per VRF whether an IPv4 **default route** exists, and the finding counts the VRFs without one | `device`, `name` (VRF) |
| `igp_neighbors` | OSPF adjacencies: device, VRF, area, process, role, remote router id and address, local interface, cost, remote device (IS-IS, EIGRP and RIP are not in Forward's model) | `device`, `name` (VRF) |
| `security_rules_experimental` | per device and scope, largest rule count first: rule and object counts of Forward's **experimental** security rules model (see `reference/kinds.md`) | `device` |
| `cloud_accounts` | one row per cloud account: name, id, cloud, **collected** (true/false), cloud setup, VPC, subnet and instance counts | `account` |
| `cloud` | account, cloud, whether the account was collected, VPC name and id, regions, CIDR blocks, subnet and instance counts | `account`, `name` (VPC) |
| `cloud_subnets` | per VPC: subnet name and id, region, zone, addresses, interface count, route table | `account`, `name` (VPC) |
| `cloud_instances` | per VPC: instance name and id, type, image, up, interface count, private addresses, tags | `account`, `name` (VPC) |
| `cloud_routes` | per VPC: route table, region, prefixes, route type, next hop, priority, whether inactive | `account`, `name` (a VPC name or a route table name, exact) |
| `cloud_security` | per VPC: security group, direction, action, source and destination prefixes, protocol, ports | `account`, `name` (VPC) |
| `cloud_gateways` | per VPC: VPC peerings, VPN connections (up or down), internet and NAT gateways; the finding counts VPN connections that are down and `by_kind_and_state` counts every gateway that matches | `account`, `name` (VPC) |
| `ip_owner` | for each address in `ips` (1 to 100 IPv4, required): the interface, routed-VLAN (SVI) interface or FHRP virtual address that carries it (with its VRF), else the connected subnet it sits in, else no owner | `ips` only; it takes none of `device`, `account`, `name`, `limit`, `offset` |

Optional: `snapshot_id` (default: newest processed, collected), `compare_to_snapshot_id` (kind `devices` only: see below), `limit` (default 25, at most 200) and `offset` for paging. `ips` belongs to kind `ip_owner` alone, and an input of another kind is rejected by name. A filter a kind does not take (`device` on a cloud kind, `account` on `devices`, any filter on `summary`) is refused, never silently ignored.
