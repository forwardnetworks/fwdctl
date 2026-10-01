# Features the skills depend on

How to tell whether a feature is available to the skills, what it looks like when it is not, and how it is switched on. `inspect-environment` returns the `features` table below (one row per property, with its effective value, the deployment default, whether the organization overrides it and its level and `org_admin_configurability`). Sources: Forward's property catalogue and its source at the pinned builds; nothing here is observed beyond what a skill reads.

## Contents

The property-gated features; the features no property gates; what the API cannot tell.

## The property-gated features

Read the row of `inspect-environment` first (`features.rows[].state`). `level` ORG is an override per organization only; BOTH adds an overridable deployment default. `org_admin_configurability` says who may change it: ANYWHERE (an organization administrator on any deployment), ON_PREMISES (an organization administrator of an on-premises Forward; on SaaS only Forward support), NONE (Forward support or a Forward admin only). **A change reaches existing snapshots only after they are reprocessed** where the row says `reprocess_to_apply`.

| Feature | Property (built-in default) | Probe | Symptom when it is off | How to enable |
|---|---|---|---|---|
| Advanced reachability (the flow DAG), which internet exposure is read from | `advanced_reachability_analysis` (`ON_DEMAND`) | `inspect-snapshots`: `advanced_reachability.state` per snapshot; the `features` row | `UNPROCESSED`; `inspect-vulnerabilities` with `internet_addressable` says unavailable (`PENDING_ADVANCED_REACHABILITY`) | per snapshot, `edit-advanced-reachability`; for every new snapshot set the property to `ASYNC` (organization administrator on-premises); `ASYNC` skips predicted snapshots |
| Flow (reachability) computation | `disable_flow_computation` (`false`; `true` means OFF) | the `features` row; `inspect-vulnerabilities` reason | `REACHABILITY_COMPUTATION_DISABLED` once the DAG stage finished; before that `PENDING_ADVANCED_REACHABILITY` and `UNPROCESSED`, which read like never triggered; `edit-advanced-reachability` refuses | set the property to `false` (organization administrator on-premises, or Forward support); a license without path analysis also disables it and cannot be changed here |
| Computation time and memory limits | `reachability_timeout_minutes` (`120`), `reachability_max_concurrent_devices_per_worker` (`8`) | the `features` rows; `advanced_reachability.state` | a computation over the timeout ends `TIMED_OUT`, a final state that `edit-advanced-reachability` refuses until the snapshot is reprocessed | raise the timeout (organization administrator on-premises) and reprocess |
| Internet connection suggestions | `proactive_internet_connection_suggestions_computation` (`true`) | `inspect-topology` kind `external`: the suggestion count | an empty list means none were computed, not none are needed | set `true` (organization administrator on-premises) and reprocess |
| Predict (change sets, predicted snapshots and their checks) | `predict_modeling` (`false` on the primary track, `true` on stable) | `inspect-snapshots` lists predicted snapshots; `verify-change` view `describe` on a change set | change sets cannot be predicted or reviewed | set `true` (organization administrator on-premises) |
| Predict protocol modelling | `predict_model_bgp`, `predict_model_ospf`, `predict_model_ext_adv` | the `features` rows | a prediction leaves out the effect of that protocol; a BGP advertisement change shows nothing | set `true` (organization administrator on-premises) and predict again |
| Link-override staging at network level | `link_override_staging` (`true`) | the `features` row; `edit-link-overrides` limits | editing a snapshot's overrides invalidates it and nothing is staged for the next snapshot (Forward documents the property as UI-gated) | set `true` (organization administrator on-premises) |
| Performance data | `performance_data` (`true`) | `inspect-performance` returns no readings | no performance data is shown | set `true` (organization administrator, any deployment); SNMP collection must also be configured |
| CVE configuration analysis and index updates | `enable_cve_config_analysis` (`true`), `auto_update_cve_index` (`false`) | `inspect-environment` `cve_index` (built and updated times) | more CVEs than apply; an index that is the one bundled with the build | set `true` (organization administrator on-premises); an index can also be uploaded |
| NQE fields | `nqe_security_rules_panos`, `nqe_security_rules_fortios`, `nqe_lldp_chassis_id` (all `false`), `peer_lldp_cdp_capability` (`true`) | `validate-nqe-query` on a query that reads the field | the query compiles and returns no rows, or the field is empty; zero rows is `unknown`, not none | set `true` (organization administrator on-premises) and reprocess |
| Who may reprocess or start advanced reachability | `snapshot_reprocess_minimum_role` (`LIMITED_READ_ONLY`) | the `features` row | a login below the role is refused (403); organization administrators are not subject to it | set the role (organization administrator, any deployment) |

## The features no property gates

These are switched by the build or by a route, not by an organization property. Probe by calling the skill and reading the status in the result; the SDK records a route Forward does not have as a capability (`capabilities` in `inspect-environment`: `unsupported`, where `unknown` only means not yet seen).

| Feature | Probe | Symptom when the build lacks it | How to get it |
|---|---|---|---|
| NQE-driven synthetic nodes (a node's connections generated from a saved query) | `inspect-topology` kind `external` shows a node's `query` and generated connections; `edit-synthetic-query` lists the compatible queries | the compatible-queries route or the query attach answers 404, which these skills report as unavailable, never as "no queries" | a Forward build that has it (it is a preview in the builds checked) |
| Snapshot progress and process estimate | `inspect-snapshots` with a `snapshot_id` reads the stage detail | a limit says the progress route is absent, so only the snapshot's own state is shown | a Forward build that has it |
| Internet exposure | `inspect-vulnerabilities` with `internet_addressable`; the unavailable reason | one of `INTERNET_NODE_NOT_DEFINED` (no internet node: `plan-synthetic-device`), `REACHABILITY_COMPUTATION_DISABLED` (flow computation disabled), `PENDING_ADVANCED_REACHABILITY` (the DAG stage has not finished: never asked for, or blocked) | model the internet node, then enable and compute advanced reachability |

## What the API cannot tell

- Who set a value, when, or why: the configuration API returns values and the organization override rows, nothing about the author or time.
- A network-level override of `disable_flow_computation` or `advanced_reachability_analysis` (Forward holds them per network and returns them through no route), per-user toggles, and the license tier (a license without path analysis disables flow computation too). A feature that reads on in `features` can still be off for one network.
- Whether a snapshot was computed under the current value: a changed computation property reaches existing snapshots only after they are reprocessed.
- Why advanced reachability stayed `UNPROCESSED` under `ASYNC`: a predicted snapshot, a network-level disable, an unlicensed path analysis and a trigger that did not run look the same. The skills say UNKNOWN and name the candidates.
