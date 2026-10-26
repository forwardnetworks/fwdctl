# inspect-inventory: inputs

`network_id` and `kind`:

| `kind` | Returns | Narrow with |
|---|---|---|
| `summary` | counts of devices, interfaces, VLANs, VRFs, hosts and cloud accounts; devices by vendor and by device type | nothing (on a very large network the interface, VLAN, VRF, host and cloud-account counts are read within a bounded time and left out, with a limit that says so, if too slow; the device, vendor and type counts always come) |
| `devices` | name, vendor, OS and version, model, type, location, tags | `device` (exact name), `name` (a case-insensitive substring of the name; no `*` or `?`), `vendor` (Forward's vendor name, such as `FORTINET`; also on `security_rules_experimental`) |
| `interfaces` | device, name, type, admin and oper status, MTU, speed, MAC, IPv4 addresses (subinterfaces **and routed-VLAN/SVI interfaces**), the VRFs, description | `device`, `name` (interface) |
| `vlans` | device, VLAN name, VLAN id range | `device`, `name` (VLAN) |
| `vrfs` | device, VRF name, type, interface count | `device`, `name` (VRF) |
| `hosts` | device, host name, type, addresses, MAC, interfaces | `device`, `name` (host) |
| `routes` | the forwarding table Forward modelled: device, VRF, prefix, origin protocol, next hop, interface, next-hop type (one row per next hop); the evidence also says per VRF whether an IPv4 **default route** exists, and the finding counts the VRFs without one | `device`, `name` (VRF) |
| `routes` with `ips` | network-wide, for each IPv4 prefix (or address) in `ips` (up to 20): how many devices and device-VRFs hold an **exact** route, a **more specific** route (inside it) and a **covering** route (contains it; a default route is not counted), with a sample of each and a page of the routes (`limit`, exact first). The forwarding table is what a device installed, so this says who carries a route, not who advertises it; IPv6 and MPLS are not read | `ips`, `limit`; `device`, `name`, `offset` are refused |
| `igp_neighbors` | OSPF adjacencies: device, VRF, area, process, role, remote router id and address, local interface, cost, remote device (IS-IS, EIGRP and RIP are not in Forward's model) | `device`, `name` (VRF) |
| `bgp_families` | the BGP address families a network runs, read from device CONFIG (bgpRib lists every family on every device, so it cannot say): device counts by family and OS (families are folded so the same one counts once across OSes: a bare ipv4 or ipv6 is `ipv4 unicast`, EOS `evpn` is `l2vpn evpn`) and `evpn_devices`; `name` = a family (`evpn` matches evpn and l2vpn evpn) lists the devices running it. EOS, IOS, IOS-XE and NX-OS config shapes only; `os_coverage` shows which OSes had no family found (Junos, IOS-XR and FortiOS are not read) |
| `security_rules_experimental` | per device and scope, largest rule count first: rule and object counts of Forward's **experimental** security rules model (see `reference/kinds.md`) | `device` |
| `cloud_accounts` | one row per cloud account: name, id, cloud, **collected** (true/false), cloud setup, VPC, subnet and instance counts | `account` |
| `cloud_coverage` | the coverage roll-up: per cloud setup the accounts, how many were collected, VPC, subnet and instance totals, and for each account that was NOT collected its id and what the setup records about it (an Azure subscription's last connectivity test, an AWS member account's enabled flag and last error, a C2S portal list); counts of each reason; the first 25 uncollected accounts per setup | none |
| `endpoints` | the network endpoints rolled up by profile: endpoints, collected, not collected (with the result), the first 25 not collected, configured endpoints switched off | `name` (profile name contains) |
| `lifecycle` | OS support dates rolled up by vendor and OS: support ended, ending within 12 months, later, no record; the oldest 25 devices past support | none |
| `cloud` | account, cloud, whether the account was collected, VPC name and id, regions, CIDR blocks, subnet and instance counts | `account`, `name` (VPC) |
| `cloud_subnets` | per VPC: subnet name and id, region, zone, addresses, interface count, route table | `account`, `name` (VPC) |
| `cloud_instances` | per VPC: instance name and id, type, image, up, interface count, private addresses, tags | `account`, `name` (VPC) |
| `cloud_routes` | per VPC: route table, region, prefixes, route type, next hop, priority, whether inactive | `account`, `name` (a VPC name or a route table name, exact) |
| `cloud_security` | per VPC: security group, direction, action, source and destination prefixes, protocol, ports | `account`, `name` (VPC) |
| `cloud_gateways` | per VPC: VPC peerings, VPN connections (up or down), internet and NAT gateways; the finding counts VPN connections that are down and `by_kind_and_state` counts every gateway that matches | `account`, `name` (VPC) |
| `ip_owner` | for each address in `ips` (1 to 100 IPv4, required): the interface, routed-VLAN (SVI) interface or FHRP virtual address that carries it (with its VRF), else the connected subnet it sits in, else no owner; an address with no owner also gets `covering_route`: the longest route longer than /0 that contains it in any VRF of any device (prefix, how many device-VRFs hold it, a sample with next hops; at most 20 such addresses per call; none means default routes only) | `ips` only; it takes none of `device`, `account`, `name`, `limit`, `offset` |

Optional: `snapshot_id` (default: newest processed, collected), `compare_to_snapshot_id` (kind `devices` only: see below), `limit` (default 25, at most 200) and `offset` for paging. `ips` belongs to kind `ip_owner` alone, and an input of another kind is rejected by name. A filter a kind does not take (`device` on a cloud kind, `account` on `devices`, any filter on `summary`) is refused, never silently ignored.
