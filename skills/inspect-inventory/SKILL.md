---
name: inspect-inventory
description: Reads network contents: size, vendors, devices, interfaces, VLANs, VRFs, hosts, cloud VPCs; kind ip_owner finds an IP's interface. Use when asked how many devices exist or who owns an IP.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "inventory-topology"
  summary: "devices, interfaces, VLANs, hosts, IP owner"
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
| `devices` | name, vendor, OS and version, model, type, location, tags | `device` |
| `interfaces` | device, name, type, admin and oper status, MTU, speed, MAC, IPv4 addresses, description | `device`, `name` (interface) |
| `vlans` | device, VLAN name, VLAN id range | `device`, `name` (VLAN) |
| `vrfs` | device, VRF name, type, interface count | `device`, `name` (VRF) |
| `hosts` | device, host name, type, addresses, MAC, interfaces | `device`, `name` (host) |
| `cloud` | account, cloud, VPC name and id, regions, CIDR blocks, subnet and instance counts | `account`, `name` (VPC) |
| `cloud_routes` | per VPC: route table, region, prefixes, route type, next hop, priority, whether inactive | `account`, `name` (VPC) |
| `cloud_security` | per VPC: security group, direction, action, source and destination prefixes, protocol, ports | `account`, `name` (VPC) |
| `cloud_gateways` | per VPC: VPC peerings, VPN connections (up or down), internet and NAT gateways | `account`, `name` (VPC) |
| `ip_owner` | for each address in `ips` (1 to 100 IPv4, required): the interface that carries it, else the connected subnet it sits in, else no owner | `ips` only; it takes none of `device`, `account`, `name`, `limit`, `offset` |

Optional: `snapshot_id` (default: newest processed, collected), `limit` (default 25, at most 200) and `offset` for paging. `ips` belongs to kind `ip_owner` alone, and an input of another kind is rejected by name. See `schema.json`.

## Procedure

1. Resolve the snapshot; it must be processed. Otherwise `unknown`.
2. Run the kind's query with `limit` and `offset`. Filters are passed as query parameters, never spliced into the text.
3. Decide:
   - Rows returned: **ok**, with the rows, the total, and the window shown. If more rows exist than were returned, the
     limits say so and how to page.
   - Zero rows: **unknown**. The entity may not exist, or the filter may be wrong (names are matched exactly); the skill
     cannot tell which. For `summary`, zero devices means an empty or unmodeled network, not "nothing to report".
4. On a predicted snapshot the limits say the rows describe a prediction, not collected state.

**cloud kinds.** `cloud` lists the VPCs or VNets; the three `cloud_*` kinds read what a cloud connectivity question needs from Forward's cloud model (NQE `network.cloudAccounts`): routes and their next hops, security group rules, and the gateways and peerings that join a VPC to anything else. Network ACLs, cloud firewalls, transit gateways, direct-connect gateways, load balancers and floating (public) IPs are in the same model but have no kind here: read them with `author-nqe-query`. These kinds were run read-only against a live Azure network (rows and counts returned); AWS and GCP rows are unverified. In `cloud_security` an empty prefix, protocol or port list on a rule appears to mean the rule does not constrain that field (any), which is unverified: check it in Forward before concluding a rule allows or blocks everything. Whether the path search (`investigate-reachability`) can trace between two cloud instances is not established here: say so rather than assume it; compare the instance subnets' route table (`cloud_routes`), the security groups on both ends (`cloud_security`) and the gateways (`cloud_gateways`).

**kind ip_owner** (the retired `find-ip-owner` skill). Answers "who owns this address" from the model, which the other kinds cannot be asked by address. Read every modelled IPv4 interface address (paged, bounded); for each address in `ips`: an exact owner (device, interface, subinterface, VRF, prefix length; more than one when several interfaces carry it), else the longest connected subnet that contains it with its interfaces, else no owner. IPv4 only. An owner is an interface of a collected device in this snapshot; no owner may mean the address is outside the network or on an uncollected device. A snapshot that is not ready is **unknown**. Evidence is one `nqe` item: `addresses` (each with `owner`, or `inside_connected_subnet` and `subnet_interfaces`, and a `note`), `owned` and `interface_addresses_read`; next: `investigate-reachability` to trace to or from the address.

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
