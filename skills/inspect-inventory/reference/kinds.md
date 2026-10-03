# inspect-inventory: notes for the cloud kinds, devices compare and ip_owner

Reference for the `inspect-inventory` kinds that need more than the table in the SKILL.md says. Read the section for the kind you are answering from.

## Contents
- cloud_accounts and the cloud kinds
- kind devices with compare_to_snapshot_id
- Snapshots that are not ready
- kind ip_owner

## cloud_accounts and the cloud kinds

**cloud_accounts.** Read this before trusting an empty cloud result: a VPC list whose VPCs all hold zero instances is either an account Forward collected that is truly empty, or one it never collected (or that failed). `collected` is the flag in
Forward's cloud model; an account with `collected: false` was not collected in this snapshot. The model carries only that flag, not an error text or the regions or projects: for the reason, read the network's cloud setup in the collection sources
(`inspect-collection` view `config`) and `investigate-collection-failure`. `cloud` rows carry the same `collected` flag.

**cloud kinds.** `cloud` lists the VPCs or VNets; the three `cloud_*` kinds read what a cloud connectivity question needs from Forward's cloud model (NQE `network.cloudAccounts`): routes and their next hops, security group rules, and the gateways and peerings that join a VPC to anything else. Network ACLs, cloud firewalls, transit gateways, direct-connect gateways, load balancers and floating (public) IPs are in the same model but have no kind here: read them with `author-nqe-query`. These kinds were run read-only against a live Azure network (rows and counts returned); AWS and GCP rows are unverified. In `cloud_security` an empty prefix, protocol or port list on a rule appears to mean the rule does not constrain that field (any), which is unverified: check it in Forward before concluding a rule allows or blocks everything. Whether the path search (`investigate-reachability`) can trace between two cloud instances is not established here: say so rather than assume it; compare the instance subnets' route table (`cloud_routes`), the security groups on both ends (`cloud_security`) and the gateways (`cloud_gateways`).

## kind devices with compare_to_snapshot_id

 Which devices this snapshot has that another lacks, and the other way: `devices` (baseline, current, added, removed, unchanged), the added and removed names (`limit`/`offset` page them), counts by vendor, device type and name prefix for each (the prefix is the text before the first `-`, `_` or `.`, often the site), and the net change per vendor and type. It reads only each snapshot's device list, so it is quick. A device type that lost and gained about as many devices (at least 50 each, the smaller at least half the larger) is reported as `likely_renamed_or_rescoped` and in the finding: a renamed device shows as one removed and one added. Both snapshots must be processed; one that is UNPROCESSED, still processing or failed is **unknown** and the answer names its state and `edit-snapshot-reprocess`. It takes no `device` or `name` filter. It says nothing about why a device appeared: Forward records no reason and no creation time on a device. Checked on a network that went from 4,479 to 40,314 devices: 39,163 added (mostly Cisco switches and routers, in a handful of site prefixes) and 3,328 removed, with about 1,800 firewalls both removed and added, so a rename.

## Snapshots that are not ready

Any kind on a snapshot that is not ready says its state instead of a bare "no processed snapshot": UNPROCESSED means Forward has not built its model (never processed, or invalidated), the device count Forward's snapshot list records is given, and the next step is `edit-snapshot-reprocess` (dry run first). `kind summary` takes about a second on a warm snapshot; the first query against a large snapshot can take minutes while Forward loads it, and the limits say so when a run takes over 20 seconds. `fwdctl run inspect-inventory --format table` prints the largest list of the summary; `--list by_vendor` prints another, and stderr names the others.

## kind ip_owner

Answers "who owns this address" from the model, which the other kinds cannot be asked by address. Read every modelled IPv4 interface address (paged, bounded); for each address in `ips`: an exact owner (device, interface, subinterface, VRF, prefix length; more than one when several interfaces carry it), else the longest connected subnet that contains it with its interfaces, else no owner. IPv4 only. An owner is an interface of a collected device in this snapshot; no owner may mean the address is outside the network or on an uncollected device. A snapshot that is not ready is **unknown**. Evidence is one `nqe` item: `addresses` (each with `owner`, or `inside_connected_subnet` and `subnet_interfaces`, and a `note`), `owned` and `interface_addresses_read`; next: `investigate-reachability` to trace to or from the address.
