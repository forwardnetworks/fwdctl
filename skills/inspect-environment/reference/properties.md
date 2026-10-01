# Organization properties that change what the skills see

Source: Forward's property catalogue (each property's own documentation string) and its source. A property whose documentation is empty is marked; where the effect on a skill is a reading, not Forward's statement, it says **inferred**. `inspect-environment` with `include_properties` returns an `explanations` field with the same lines for the non-default properties it shows.

## Contents

The properties; what to do with a non-default value.

## The properties

| Property | Default | What Forward says it does | What it changes in these skills |
|---|---|---|---|
| `ADVANCED_REACHABILITY_ANALYSIS` | `ON_DEMAND` | When the flow analysis (the DAG) is computed: `ASYNC` starts it after a snapshot is processed (not for predicted snapshots, not while flow computation is disabled), `ON_DEMAND` only on request | `inspect-snapshots` shows `advanced_reachability` UNPROCESSED; `inspect-vulnerabilities` has no `internet_addressable` until it is PROCESSED; `edit-advanced-reachability` starts it |
| `DISABLE_FLOW_COMPUTATION` | `false` | Documentation empty. Its source skips the automatic advanced reachability and reports internet exposure as `REACHABILITY_COMPUTATION_DISABLED` | exposure unavailable with that reason; no flow analysis for `investigate-reachability` |
| `BACKGROUND_SNAPSHOT_REPROCESS` | `HIGH_PRIORITY` | Priority of background reprocessing (no impact if `ALLOW_BACKGROUND_SNAPSHOT_REPROCESS` is true) | how long a reprocessed or backdated snapshot stays unavailable; `LOW_PRIORITY` waits behind other work |
| `ALLOWED_ONPREM_COLLECTORS` | `BUNDLED_ONLY` | Which kinds of on-premises collector may be used | `inspect-collection`, `edit-collection` (needs an allowed collector up) |
| `MAX_PROCESSING_AI_CHATS_PER_USER` | `1` | Concurrent processing AI chats per user, to bound load | nothing in these skills (inferred: they do not use the AI chat); explains load on the same Forward |
| `TASK_THRESHOLDS_MULTIPLIER` | `1.0` | Scales the long-running and large-resource thresholds per job type and device count | how slow tasks are classified in `inspect-collection` and `inspect-snapshots` (inferred); not what is computed |
| `REACHABILITY_MAX_CONCURRENT_DEVICES_PER_WORKER` | `8` | Most devices one worker computes flowlets for at once, to avoid running out of memory | time and memory of processing and advanced reachability |
| `REACHABILITY_TIMEOUT_MINUTES` | `120` | Documentation empty; read from the name | a computation over it ends TIMED_OUT (inferred) |
| `ACL_LESS_ANALYSIS` | `true` | Also computes reachability in ACL-less mode | more computation; the skills read the regular mode |
| `PROACTIVE_INTERNET_CONNECTION_SUGGESTIONS_COMPUTATION` | `true` | Computes the internet connection suggestions after processing | `inspect-topology` `kind: external` suggestions; off means no suggestions are made, not that none are needed |
| `COMPUTE_PARALLELISM`, `DAG_PHASED_MERGE` | `-1`, `false` | Fork-join pool size of DAG computation; whether the DAG merges in phases | speed and memory of advanced reachability (inferred: not answers) |

## What to do with a non-default value

A value that is not in the table has no explanation held here: read Forward's documentation, do not guess. Changing a property is not something these skills do. `ADVANCED_REACHABILITY_ANALYSIS`, `DISABLE_FLOW_COMPUTATION` and the reachability limits are configurable by an organization administrator on-premises (Forward's `OrgAdminConfigurability`; level organization or network), so the administrator of that Forward owns them.
