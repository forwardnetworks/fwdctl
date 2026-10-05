---
name: inspect-inventory
description: Reads network contents: size, vendors, devices, interfaces, VLANs, VRFs, hosts, cloud VPCs; kind ip_owner finds an IP's interface. Use when asked how many devices exist or who owns an IP.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "inventory-topology"
  summary: "devices, interfaces, VLANs, IP owner"
  maturity: "3"
  tools: "snapshots, query"
---

# inspect-inventory

## Intent

Answer inventory questions from Forward's model of the network, as exact rows with the snapshot they came from. It
reads and never judges: a row is a fact about the collected network, not a verdict.

## Inputs

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

Optional: `snapshot_id` (default: newest processed, collected), `compare_to_snapshot_id` (kind `devices` only: see below), `limit` (default 25, at most 200) and `offset` for paging. `ips` belongs to kind `ip_owner` alone, and an input of another kind is rejected by name. A filter a kind does not take (`device` on a cloud kind, `account` on `devices`, any filter on `summary`) is refused, never silently ignored. See `schema.json`.

## Procedure

1. Resolve the snapshot; it must be processed. Otherwise `unknown`.
2. Run the kind's query with `limit` and `offset`. Filters are passed as query parameters, never spliced into the text.
3. Decide:
   - Rows returned: **ok**, with the rows, the total, and the window shown. If more rows exist than were returned, the
     limits say so and how to page.
   - Zero rows: **unknown**. The entity may not exist, or the filter may be wrong (names are matched exactly); the skill
     cannot tell which. For `summary`, zero devices means an empty or unmodeled network, not "nothing to report".
4. On a predicted snapshot the limits say the rows describe a prediction, not collected state.

**Cloud kinds, `devices` with `compare_to_snapshot_id`, a snapshot that is not ready, and `ip_owner`** each have notes that decide how to read the answer: an empty cloud result is not proof of no cloud resources, the device diff flags probable renames and says when a snapshot cannot be diffed, and `ip_owner` finds the interface and VRF that own an address. Read `reference/kinds.md` (`fwdctl describe inspect-inventory reference/kinds.md`) before answering from any of those.

## Evidence

One `state` item holding the kind, the filters, the total row count, the window, and the rows.

## Output

The envelope in `schema/skill-result.schema.json`. `next_actions` point at the investigation the inventory suggests.

## Next actions

`investigate-reachability`, `inspect-vulnerabilities`, `investigate-collection-failure`, `check-network-compliance`.

## Running this skill

Give the inputs as one JSON object on stdin: `echo '{"network_id": "<id>"}' | fwdctl run inspect-inventory`. It needs the
`fwdctl` binary and `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD` in the environment. Read `status`
(`ok`, `failed`, `unknown`, `error`) and the `limits`; `unknown` is never a pass. Inputs are listed above and in `schema.json`.
