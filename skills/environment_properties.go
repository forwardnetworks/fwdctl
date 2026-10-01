package skills

import "strings"

// propertyMeaning says in one line what an organization property does and which skill behaviour it changes. The meanings are Forward's own documentation strings (its property
// catalogue) and, where a property's documentation is empty, what Forward's source does with it; each is marked. The default is the build's default.
type propertyMeaning struct {
	Default, Meaning, Changes string
}

var propertyMeanings = map[string]propertyMeaning{
	"advanced_reachability_analysis": {"ON_DEMAND", "When the flow analysis (the DAG) is computed: ASYNC starts it automatically after a snapshot is processed (not for predicted snapshots, and not while flow computation is disabled); ON_DEMAND, the default, only when someone asks.",
		"inspect-snapshots shows advanced_reachability UNPROCESSED for snapshots nobody computed it for; inspect-vulnerabilities cannot give internet_addressable until it is PROCESSED; edit-advanced-reachability starts it."},
	"disable_flow_computation": {"false", "Switches off the flow (reachability) computation for a network. Forward's documentation for it is empty; its source skips the automatic advanced reachability and reports internet exposure as REACHABILITY_COMPUTATION_DISABLED when it is set.",
		"inspect-vulnerabilities returns internet exposure unavailable (REACHABILITY_COMPUTATION_DISABLED); investigate-reachability has no flow analysis to read."},
	"background_snapshot_reprocess": {"HIGH_PRIORITY", "The priority at which snapshots are reprocessed in the background (Forward: if ALLOW_BACKGROUND_SNAPSHOT_REPROCESS is set to true, setting this has no impact).",
		"How long a reprocessed or invalidated snapshot stays unavailable: LOW_PRIORITY lets it wait behind other work (inspect-snapshots shows it PROCESSING longer; edit-snapshot-reprocess and a backdate take effect later)."},
	"allowed_onprem_collectors": {"BUNDLED_ONLY", "Which kinds of on-premises collector may be used.",
		"inspect-collection (whether a collector is connected, which kind) and edit-collection (a collection can start only when an allowed collector is up)."},
	"max_processing_ai_chats_per_user": {"1", "The most processing AI chats one user may have at a time; it bounds the load of Forward's own AI chat.",
		"no skill here: these skills do not use Forward's AI chat. It only explains load on the same Forward."},
	"task_thresholds_multiplier": {"1.0", "Scales the thresholds that decide whether a task is long-running or needs large resources, per job type and number of devices.",
		"how Forward classifies slow collection and processing tasks (inspect-collection, inspect-snapshots); it does not change what is computed (inferred from the documentation)."},
	"reachability_max_concurrent_devices_per_worker": {"8", "The most devices one worker computes flowlets for at once, to avoid running out of memory.",
		"how long processing and advanced reachability take and how much memory they use (edit-advanced-reachability names this as its cost limit)."},
	"reachability_timeout_minutes": {"120", "The reachability computation timeout in minutes. Forward's documentation for it is empty: this is the name's reading.",
		"a computation that exceeds it ends TIMED_OUT (inspect-snapshots advanced_reachability, edit-advanced-reachability refuses a final state); inferred from the name."},
	"acl_less_analysis": {"true", "Also computes reachability in ACL-less mode in addition to the regular mode.",
		"adds computation to processing; the skills read the regular mode."},
	"proactive_internet_connection_suggestions_computation": {"true", "Computes Forward's internet connection suggestions after snapshot processing.",
		"inspect-topology kind external shows internet_connection_suggestion_count and plan-synthetic-device reads the suggestions: when it is off, no suggestions is not 'none to make'."},
	"compute_parallelism": {"-1", "The size of the fork-join pool of DAG computation; zero or less means full parallelism.",
		"how fast and how heavy advanced reachability is (edit-advanced-reachability cost)."},
	"dag_phased_merge": {"false", "Whether the DAG is merged in phases.",
		"memory and time of advanced reachability; it does not change answers (inferred from the documentation)."},
}

// explainProperties returns an explanation for each shown property that has one, and the names without one.
func explainProperties(names []string, values map[string]string) (map[string]any, []string) {
	out := map[string]any{}
	var missing []string
	for _, n := range names {
		m, ok := propertyMeanings[strings.ToLower(n)]
		if !ok {
			missing = append(missing, n)
			continue
		}
		out[n] = map[string]any{"value": values[n], "default": m.Default, "meaning": m.Meaning, "changes": m.Changes}
	}
	return out, missing
}
