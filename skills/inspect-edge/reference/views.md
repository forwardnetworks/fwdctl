# inspect-edge: the public_addresses and trace_sources views

Reference for `inspect-edge` views `public_addresses` and `trace_sources`: what each reads, how to read the result, and what it cannot tell you. The SKILL.md has the views table and the `exits` view.

## Contents
- View public_addresses
- View trace_sources

## View public_addresses (the retired `find-public-addresses` skill)

### Intent

Answers "where does this network use public addresses on its own equipment, and what kind of interface carries each one" from the model. It is the evidence step before anyone touches the internet node's excluded subnets or explains an internet-exposure result.
`inspect-inventory` kind `ip_owner` answers for one address; this lists them all.

### Inputs

See the table of views above.

### Procedure

1. Read the snapshot; none ready is **unknown**.
2. Read every IPv4 interface address with its device type, interface type, VRF and description (paged, bounded). Keep the public ones (outside Forward's own non-public list).
3. Read the security zones, the devices' management IPs and, for firewall interfaces, the topology links and the device types on the other side, where the model has them (a failed read only weakens the roles, and is said so).
4. Give each address a role and the reason (`role_basis`), in this priority: management IP; a management name on the VRF, interface or description; a loopback interface; for a FIREWALL a security zone, then the interface description, name or VRF saying inside or outside. The context name (`-ext-dmz`, `-int-dmz`, `-lb`: `context_hint`) and the link's other side (`linked_to`) are shown and qualify the basis; they do not assign a role by themselves. Nothing else: the role stays `unknown`. Apply the filters, count by role, device type and VRF, and aggregate each role's addresses to the fewest exact CIDR blocks.
5. Return the summary and one page of rows (by role, then device). No match is **unknown**, never zero exposure.

### Limits worth stating

A role is inferred, not measured. A Cisco ASA has no security zone in Forward's model (its nameif and security level are not NQE fields), and most ASA firewalls are virtual contexts, so on an ASA-only estate customer-facing interfaces are mostly `unknown`: the `firewall_role_unknown` list is for review. management: the device's management IP, or a management name on the VRF, interface or description. loopback: a loopback interface. customer_facing and internet_facing: FIREWALL devices only, and only when a security zone, description or interface
name says so; otherwise unknown (nothing is guessed from the address). The candidate set is for the person to review: it never includes `internet_facing` or `unknown` addresses, the exclude list is prefix-based, it applies from the next processed snapshot, and an
address on a collected interface is located there before the internet node is considered, so excluding it need not change a device's internet-addressable flag (`plan-synthetic-device`, `reference/exposure.md`). IPv4 only; collected devices only.

### Evidence

One `nqe` item: `snapshot_id`, `interface_addresses_read`, `public_addresses`, `matching`, `distinct_addresses`, `devices`, `by_role`, `by_device_type`, `by_vrf`, `candidate_exclude` (per role: `addresses`, `cidr_count`, `cidrs`, `ownership_not_verified`), `prefix_groups` (the /8 and /16 spread of the candidates) and `largest_aggregates`, `firewall_role_unknown` (firewall addresses whose role stayed unknown, aggregated, grouped, labelled needs review: do not exclude blindly; never part of `candidate_exclude`), `unknown_by_group` (all unknown rows by device type, VRF and interface-name pattern with the top descriptions), `verify` (the before and after steps as concrete skill calls), `offset`, `addresses` (the page; rows may carry `context_hint` and `linked_to`) and `model_reads`. The skill does not check who owns a public prefix (no whois or registry lookup): excluding space the customer does not own only hides it.

### Next actions

`investigate-reachability` with `from: "internet"` to one address; `inspect-topology` (kind external) for what the internet node routes; `edit-internet-exclusions` (dry run first) only for public space the network routes internally; `plan-synthetic-device`.

## View trace_sources (the retired `find-trace-source` skill)

### Intent

A trace from a source that is blackholed at its first hop proves nothing about the destination. This skill picks sources that are not: it traces each candidate to a **control address** that must be reachable (the edge device's own interface
address, or a gateway) and reports which candidates arrive. It then hands you a source for `investigate-reachability`. Why and how to judge a synthetic-device change: `plan-synthetic-device`.

### Inputs

See the table of views above.

### Procedure

1. Read the snapshot; none ready is **unknown**.
2. List the devices that have an IPv4 routing table (in the VRF, when named), minus `exclude`, sorted by name. None is **unknown**.
3. Trace each of the first `candidates` from the device to `target_ip` (bounded: a few paths, ten seconds each) and classify the best path.
4. **ok** when at least one candidate is delivered, ranked delivered first and then by hop count (a farther source exercises more of the path); **failed** when none is.

### Limits worth stating

A delivered control means the source reaches the control address, **not** the real destination. The candidates are devices, not hosts: trace from a host with `investigate-reachability` `src_ip` when a host matters. Only the first `candidates` by name are traced.

### Evidence

One `path` item: `target_ip`, `vrf`, `candidates_traced`, `good`, `truncated` and `sources` (each: `device`, `control` classification, `hops`, `last_hop`, `delivered`).

### Next actions

`investigate-reachability` from the best source to the real destination.
