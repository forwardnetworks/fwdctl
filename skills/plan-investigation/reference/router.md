# Router: which skill, by symptom

The symptom table of `plan-investigation`: a question or symptom on the left, the skill to reach for first on the right (the first skill named is the answer; the others are what it hands on to). `fwdctl which "<question>"` ranks this table and the playbook table offline, so try that first and read this whole table only when its guess is weak or the question spans several rows.

## Contents
- Which skill, by symptom

## Which skill, by symptom

| Symptom or question | Reach for |
|---|---|
| "Which networks do I have", "which networks can I see", which networks are workspaces, list the networks, "what is the id of network X" (no network id yet) | `inspect-networks` |
| "Can A reach B", "why is this dropped", a path question | `investigate-reachability`; when the endpoints are names or you do not have addresses, find them first with `inspect-inventory` (kind hosts or devices) |
| Internet-bound path | `investigate-reachability` with destination 8.8.8.8; source "internet" for inbound |
| "Did this change work", "is it safe to push" | `verify-change` |
| "What does this change touch", "how big is it", the extent of a change, which areas differ, which subnets lose connectivity | `verify-change` with `view: impact` |
| Collection takes too long, a slow or long collection, how long collection runs, the collection duration got worse, hours instead of minutes, is the collector the bottleneck, why a snapshot took so long, how collection time trended | `investigate-collection-failure` (view `history` for each collection's duration and device count over time, view `slow` for the slowest devices, total device time and how many ran at once) |
| Cloud (AWS, Azure, GCP) collection failing or dropping, an AWS account erroring, authentication failed for a cloud region, a C2S or access portal credentials error; A device is missing, a snapshot looks incomplete, a collection failed; which devices failed collection (refused, timed out, authentication), a parser failure, a slow or longest collection, a collection log, unmodelled neighbours | `investigate-collection-failure` (view devices, slow, logs or neighbors) |
| "Are we compliant", does the network comply with a rule, "does anything violate X" | `check-network-compliance` |
| "What version is this", "which features does this Forward have", is a feature or preview flag enabled, what value a property has | `inspect-environment` |
| "Which snapshot should I use", "is this snapshot complete", "which are predictions" | `inspect-snapshots` |
| "Is anything unhealthy now", CPU, memory, utilization, errors, packet loss | `inspect-performance` |
| Forward seems slow or wedged, "what jobs are running", a query or execution that never returns, "is anything stuck" | `inspect-jobs` |
| Cancel a stuck or wedged backend job instead of restarting a worker | `edit-jobs` (dry run first) |
| "Is collection running or healthy", "is the collector connected" | `inspect-collection` (view status) |
| What needs to be migrated to a controller-managed setup (ACI, SD-WAN), the migration count, which controller serves it, which logins could be tried | `inspect-collection` (view migration) |
| Device discovery: what discovery has found that is not yet collected, discovery jobs running or finished, scheduled discovery, why a discovered device was not added | `inspect-collection` (view discovery) |
| Which fail their connectivity test and with what error, connectivity test results by error and phase, PING_FAILED or AUTHORIZATION_FAILED on tests, a custom command authorization failure that is not in the snapshot | `inspect-collection` (view connectivity) |
| A controller-managed setup read back: its controllers, guests, collector and the last connectivity test of each, HOST_NOT_REPORTING_ANY_GUEST | `inspect-collection` (view config, `controller_setups`) |
| "What is collected", "is device X switched off for collection", which cloud or AWS accounts are enabled for collection, member accounts, the last test of each region, cloud collection setup, a C2S access portal (CAP) setup and which accounts it collects | `inspect-collection` (view config) |
| A path ends at the edge, "is the internet modelled", links added by hand, the connections of an L3 VPN or L2 VPN | `inspect-topology` (kind external) |
| "When did this check start failing", "is this new or old" | `inspect-history` |
| "Which checks exist or fail", "how many checks fail or error", "why is this check in ERROR", "what did this check find", "what checks does Forward offer" | `check-network-compliance` (view read) |
| "What does this change set do", "is it ready" | `verify-change` with `view: describe` |
| Label a snapshot | `edit-snapshot` (action note) (dry run first) |
| Add a policy check, turn an authored query into a check, or switch a check off | `edit-checks` (dry run first) |
| Create, change or remove an alias (a named group a check refers to) | `edit-alias` (dry run first) |
| "What if we made this change": stage or build a change set from CLI lines or BGP advertisements the caller supplies, and predict it | `edit-change-set` (dry run first), then `verify-change` (`view: describe`, then the default view) |
| Which collection settings differ from Forward's defaults (timeouts, retries, concurrency, rates), what is temporary, a collector's concurrency against its maximum, the effective collection settings | `inspect-platform` area `collection_settings` view `effective` |
| Put a collection setting back to Forward's default (retry delay, per-device SNMP concurrency, a timeout), restore the default, clear a setting | `edit-platform` area `collection_settings` (`restore_default`, dry run first) |
| Failed as UNSUPPORTED_DEVICE_TYPE (an MDS switch, Panorama, BIG-IQ), failed as INCOMPLETE_SETUP although it has an address, a failure's connectivity test error and who created it | `investigate-collection-failure` (view devices) |
| Is a command a custom command group's or a built-in, which custom command group holds a command, where does a rejected command come from, remove or disable a custom command group | `inspect-platform` area `custom_command_groups` (`command`); change or disable one with `edit-source` object `custom_command_group` (dry run first) |
| Move classic entries into a controller-managed setup (migrate SD-WAN or ACI), queue a connectivity test of a controller-managed setup, create or test a controller setup | `edit-source` object `controller_setup` (actions create, migrate, test; dry run first) |
| "Collect now", cancel a running collection | `edit-collection` (dry run first) |
| Reprocess a snapshot: it failed to process, or its answers are stale after an upgrade | `edit-snapshot` (action reprocess) (dry run first) |
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
| Diff the rows of an ad-hoc NQE query between two snapshots, tens of thousands of changed rows, added or removed or modified rows by key (rules, routes, anything) | `fwdctl nqe diff --before-snapshot A --after-snapshot B --file q.nqe --key COL,COL` (exact counts, every difference in `--out`); `compare-nqe-results` is for a query already saved in the library and shows at most 100 rows |
| Compare two VERSIONS of an NQE query on the same snapshot (does my rewritten query return the same rows as the original, is a rewrite byte-for-byte the same, how do the timings compare) | `author-nqe-query`, then `fwdctl nqe compare --network N --snapshot S --a old.nqe --b new.nqe --key COL,COL [--params F] [--ignore-array-order] [--rounds 2]` (exit 0 identical, 1 differs; runs are cold and alternate A,B,B,A) |
| Stop collecting one command org-wide, disable a built-in collection command for a device type (a slow command, PAN-OS external list, SD-WAN routes), re-enable a disabled command, list the disabled commands | `edit-platform` area `collection_settings` name `organization` (`disable_commands`, `enable_commands`; dry run first); `inspect-platform` area `collection_settings` reads them |
| Set or clear the External ID of AWS member accounts, add newly discovered AWS accounts to a multi-account setup, enable or disable member accounts, onboard AWS accounts a few at a time | `edit-source` object `cloud_account` action `update` (`accounts_external_ids`, `accounts_add`, `add_newly_discovered`, `accounts_enabled`; dry run first) |
| Cloud collection is failing or dropped, AWS accounts missing after a collection, C2S Access Portal (CAP) errors, HTTP 400 on a credentials request, a portal certificate expired, Azure SubscriptionNotFound, which cloud accounts the collector could not reach and why | `investigate-collection-failure` with view `cloud`, then `inspect-inventory` kind `cloud_coverage` |
| How many endpoints of a profile are collected, which endpoints failed, endpoints by profile, how many endpoints are switched off | `inspect-inventory` with kind `endpoints` |
| End of support, end of life, EoS, EoL, devices past their last support date, OS lifecycle coverage, devices with no lifecycle record | `inspect-inventory` with kind `lifecycle` |
| How much of a cloud network was collected, which cloud accounts or subscriptions were not collected and why, cloud coverage by setup | `inspect-inventory` with kind `cloud_coverage` |
| A cloud account shows fewer routes than expected, a VPC route table, cloud routes or next hops | `inspect-inventory` with kind `cloud_routes` (`account` is the cloud account NAME, `name` the VPC; kind `cloud_accounts` lists the accounts) |
| Compare two networks (a seed and a lab, production and a copy): devices, interfaces, links, cloud routes; there is no cross-network compare | `compare-nqe-results` with `after_network_id` (a saved query run on both networks, rows diffed by `key`, `ignore` for fields that should differ); with no saved query, read the same `inspect-inventory` kind on each network and diff (compare-device-config diffs two snapshots of one network) |
| Add a device to the collection list (optionally pinned to a collector), a device login (CLI, SNMP or HTTP credential), a jump server or a proxy; add, rotate, test or delete a cloud account; turn a cloud (AWS) member account on or off, enable or disable an account for collection, change the C2S access portal account list | `edit-source` (dry run first; secrets from a file) |
| Set up many devices, credentials and endpoints at once from an inventory file, onboard a lab or a whole network's collection sources, add a Linux host or an endpoint to collect, create a CLI endpoint profile, set a device's enable (privileged mode) credential or BGP collection options | `fwdctl source apply FILE` (dry run first; format in `edit-source` `reference/inventory.md`), or one at a time `edit-source` object `classic_device` / `endpoint`, `edit-platform` area `endpoint_profiles` |
| Stand up a new collector, register a collector and get its token, run the collector on a Linux or Docker host, download the collector installer, attach a new collector to a network, delete a collector | `edit-platform` area `collectors` (register, delete; steps in its `reference/collectors.md`), `fwdctl collector download`, `fwdctl wait collector`, then `edit-network` object `collector` action `assign` |
| Create, rename or delete a network, attach, assign or link a collector to a network, add a location or device cluster, rename or delete a device tag | `edit-network` (dry run first) |
| Change a banner, webhook, trusted certificate, device access label, ServiceNow or Infoblox integration, or cancel or delete a backup (organization admin settings) | `edit-platform` (dry run first) |
| Is there a banner, maintenance window, system announcement or warning showing in the Forward UI (license expired, collector outdated, low storage), what banners are on a network | `inspect-platform` area `banners` |
| What is set up on Forward, is SAML or SSO configured, when does the license expire: credentials, jump servers, proxies, collectors, cloud accounts, webhooks, licensing, backups (read, secrets removed) | `inspect-platform` |
| Counting or listing devices, interfaces, VLANs, VRFs, hosts, cloud resources ("how many", "list all", "per vendor") | `inspect-inventory` |
| The literal config text or a "show" output of one device ("what does the config say", "grep the config") | `inspect-device-files` |
| "Which devices' config files changed between snapshots", what lines were added or removed | `compare-device-config` |
| "Which link overrides differ between two snapshots", which manual links one snapshot has and another lacks | `inspect-topology` (`kind: link_overrides`, `compare_to_snapshot_id`) |
| "Which CVEs affect us", "is device X vulnerable to CVE-Y" | `inspect-vulnerabilities` |
| Which devices are internet addressable (a device list, not CVEs), or why internet exposure is unavailable: disabled, blocked or never triggered | `inspect-vulnerabilities` (`view: devices`, `internet_addressable: true`) |
| "What changed in X between snapshots" for a kind of data: which rows of a saved query changed, were added or removed | `find-nqe-query`, then `compare-nqe-results` |
| Anything not covered above (custom config questions) | write an NQE query (`author-nqe-query`) and check it with `validate-nqe-query` |
| NQE syntax or data-model questions, a field reported missing that should exist (securityPolicy, services, an NQE feature flag or org property) | `author-nqe-query` |
| "Is this NQE query valid", checking a query without Forward | `fwdctl nqe lint` (offline syntax, deprecations, field names), then `validate-nqe-query` for the type check |

Prefer a dedicated skill to a hand-written query when one exists.
