# Router: which skill, by symptom

The symptom table of `plan-investigation`: a question or symptom on the left, the skill to reach for first on the right (the first skill named is the answer; the others are what it hands on to). `fwdctl which "<question>"` ranks this table and the playbook table offline, so try that first and read this whole table only when its guess is weak or the question spans several rows.

## Contents
- Which skill, by symptom

## Which skill, by symptom

| Symptom or question | Reach for |
|---|---|
| "Which networks do I have", list the networks, "what is the id of network X" (no network id yet) | `inspect-networks` |
| "Can A reach B", "why is this dropped", a path question | `investigate-reachability`; when the endpoints are names or you do not have addresses, find them first with `inspect-inventory` (kind hosts or devices) |
| Internet-bound path | `investigate-reachability` with destination 8.8.8.8; source "internet" for inbound |
| "Did this change work", "is it safe to push" | `verify-change` |
| "What does this change touch", "how big is it", the extent of a change, which areas differ, which subnets lose connectivity | `verify-change` with `view: impact` |
| Collection takes too long, a slow or long collection, how long collection runs, the collection duration got worse, hours instead of minutes, is the collector the bottleneck, why a snapshot took so long, how collection time trended | `investigate-collection-failure` (view `history` for each collection's duration and device count over time, view `slow` for the slowest devices, total device time and how many ran at once) |
| A device is missing, a snapshot looks incomplete, a collection failed; which devices failed collection (refused, timed out, authentication), a parser failure, a slow or longest collection, a collection log, unmodelled neighbours | `investigate-collection-failure` (view devices, slow, logs or neighbors) |
| "Are we compliant", does the network comply with a rule, "does anything violate X" | `check-network-compliance` |
| "What version is this", "which features does this Forward have", is a feature or preview flag enabled, what value a property has | `inspect-environment` |
| "Which snapshot should I use", "is this snapshot complete", "which are predictions" | `inspect-snapshots` |
| "Is anything unhealthy now", CPU, memory, utilization, errors, packet loss | `inspect-performance` |
| "Is collection running or healthy", "is the collector connected" | `inspect-collection` (view status) |
| "What is collected", "is device X switched off for collection" | `inspect-collection` (view config) |
| A path ends at the edge, "is the internet modelled", links added by hand, the connections of an L3 VPN or L2 VPN | `inspect-topology` (kind external) |
| "When did this check start failing", "is this new or old" | `inspect-history` |
| "Which checks exist or fail", "how many checks fail or error", "why is this check in ERROR", "what did this check find", "what checks does Forward offer" | `check-network-compliance` (view read) |
| "What does this change set do", "is it ready" | `verify-change` with `view: describe` |
| Label a snapshot | `edit-snapshot-note` (dry run first) |
| Add a policy check, turn an authored query into a check, or switch a check off | `edit-checks` (dry run first) |
| Create, change or remove an alias (a named group a check refers to) | `edit-alias` (dry run first) |
| "What if we made this change": stage or build a change set from CLI lines or BGP advertisements the caller supplies, and predict it | `edit-change-set` (dry run first), then `verify-change` (`view: describe`, then the default view) |
| "Collect now", cancel a running collection | `edit-collection` (dry run first) |
| Reprocess a snapshot: it failed to process, or its answers are stale after an upgrade | `edit-snapshot-reprocess` (dry run first) |
| Internet exposure (internet_addressable) is unavailable, or a snapshot shows advanced reachability UNPROCESSED: start the analysis for that snapshot | `edit-advanced-reachability` (dry run first) |
| Save an authored query to the library, or remove a saved one | `edit-nqe-query` (dry run first) |
| Tag devices or take a tag off them | `edit-device-tags` (dry run first) |
| Try a collection change away from production (a temporary workspace network, endpoints added to it, delete it afterwards) | `edit-workspace` (dry run first) |
| What can this login do, why was I refused (403, permission denied), who has access, which users or groups exist | `inspect-access` |
| Create or disable a user, make someone org admin, give a user or group a role on a network, define an access group | `edit-access` (dry run first) |
| Upload a dataset (CSV/JSON/XML/YAML/TEXT) for NQE to join, or attach/detach one on a network | `edit-data-file` (dry run first) |
| Add, update, delete or test a per-network HTTP data connector | `edit-data-connector` (dry run first) |
| Change an organization-wide Forward setting (a property), or list what is configurable and how risky each is | `edit-org-property` (dry run first) |
| Try an extra OID, or a different profile, on an endpoint (create an SNMP profile as a copy plus OIDs, repoint endpoints, delete the copy) | `edit-endpoint-profile` (dry run first) |
| Model a leased line or provider L2 circuit between two edge ports (a WAN circuit) | `edit-wan-circuit` (dry run first) |
| Where should the internet attach or egress, why a trace dead-ends at the edge, which default route points at an unowned next hop or upstream | `inspect-edge` (`view: exits`, the default) |
| Show BGP neighbors or peers, the upstream's AS, session state, what a device advertises to a peer (advertised, received prefixes) | `inspect-bgp-neighbors` |
| Who owns this IP address: which device, interface or VRF has it, or whether it is inside a connected subnet | `inspect-inventory` (`kind: ip_owner`, `ips`) |
| Which devices carry public IPv4 addresses on their own interfaces (management, loopback, firewall sides), by role, device type or VRF; what to review before excluding prefixes from the internet node or when a device is flagged internet addressable | `inspect-edge` (`view: public_addresses`) |
| Is there already a saved NQE query for a question; browse the NQE library: list its top-level folders or directories and the queries in one | `find-nqe-query` (`list: true`) |
| Which internal devices are good sources to trace from; choose a source that is not blackholed before tracing to an external or synthetic destination | `inspect-edge` (`view: trace_sources`, `target_ip`) |
| Keep public space the network routes internally off the internet node (its excluded subnets) | `edit-internet-exclusions` (dry run first) |
| Drive a synthetic node (internet, intranet, L3 VPN, L2 VPN, adjacent network) from a saved NQE query, or take it off | `edit-synthetic-query` (dry run first) |
| Add a link Forward missed, or ignore a wrong one, in a snapshot's topology | `edit-link-overrides` (dry run first) |
| What a device connects to, which sites, tags or aliases exist | `inspect-topology` |
| Counting or listing devices, interfaces, VLANs, VRFs, hosts, cloud resources ("how many", "list all", "per vendor") | `inspect-inventory` |
| The literal config text or a "show" output of one device ("what does the config say", "grep the config") | `inspect-device-files` |
| "Which devices' config files changed between snapshots", what lines were added or removed | `compare-device-config` |
| "Which link overrides differ between two snapshots", which manual links one snapshot has and another lacks | `inspect-topology` (`kind: link_overrides`, `compare_to_snapshot_id`) |
| "Which CVEs affect us", "is device X vulnerable to CVE-Y" | `inspect-vulnerabilities` |
| Which devices are internet addressable (a device list, not CVEs), or why internet exposure is unavailable: disabled, blocked or never triggered | `inspect-vulnerabilities` (`view: devices`, `internet_addressable: true`) |
| "What changed in X between snapshots" for a kind of data: which rows of a saved query changed, were added or removed | `find-nqe-query`, then `compare-nqe-results` |
| Anything not covered above (custom config questions) | write an NQE query (`author-nqe-query`) and check it with `validate-nqe-query` |
| NQE syntax or data-model questions | `author-nqe-query` |
| "Is this NQE query valid", checking a query without Forward | `fwdctl nqe lint` (offline syntax, deprecations, field names), then `validate-nqe-query` for the type check |

Prefer a dedicated skill to a hand-written query when one exists.
