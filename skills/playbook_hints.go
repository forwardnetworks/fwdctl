package skills

import (
	"slices"

	"github.com/forwardnetworks/fwdctl/result"
)

// playbookFor is the playbook that owns each skill's problem area. A result that is not a plain success (failed, unknown, error) or that is
// the dry run of an edit names it in next_actions, so an agent that only reads results still finds the order of steps and the protocol
// without having read the router. A test keeps every name here real and every skill covered.
var playbookFor = map[string]string{
	"investigate-reachability":       "plan-troubleshoot-connectivity",
	"inspect-topology":               "plan-troubleshoot-connectivity",
	"inspect-device-files":           "plan-troubleshoot-connectivity",
	"inspect-vulnerabilities":        "plan-vulnerability-response",
	"check-network-compliance":       "plan-compliance-audit",
	"inspect-history":                "plan-what-changed",
	"compare-device-config":          "plan-what-changed",
	"compare-nqe-results":            "plan-what-changed",
	"verify-change":                  "plan-change-review",
	"edit-change-set":                "plan-change-review",
	"inspect-snapshots":              "plan-snapshot-recovery",
	"inspect-collection":             "plan-snapshot-recovery",
	"investigate-collection-failure": "plan-snapshot-recovery",
	"edit-collection":                "plan-snapshot-recovery",
	"edit-endpoint-profile":          "plan-health-check",
	"edit-workspace":                 "plan-health-check",
	"edit-org-property":              "plan-health-check",
	"edit-snapshot-reprocess":        "plan-snapshot-recovery",
	"edit-advanced-reachability":     "plan-snapshot-recovery",
	"inspect-performance":            "plan-incident-triage",
	"inspect-inventory":              "plan-device-audit",
	"find-nqe-query":                 "author-nqe-query",
	"validate-nqe-query":             "author-nqe-query",
	"edit-nqe-query":                 "author-nqe-query",
	"edit-checks":                    "plan-compliance-audit",
	"edit-device-tags":               "plan-device-audit",
	"edit-link-overrides":            "plan-link-overrides",
	"edit-synthetic-query":           "plan-synthetic-device",
	"edit-wan-circuit":               "plan-synthetic-device",
	"edit-internet-exclusions":       "plan-synthetic-device",
	"inspect-edge":                   "plan-synthetic-device",
	"inspect-bgp-neighbors":          "plan-synthetic-device",
	"inspect-networks":               "plan-investigation",
	"inspect-environment":            "plan-investigation",
	"inspect-access":                 "plan-investigation",
	"edit-access":                    "plan-health-check",
	"edit-data-file":                 "plan-investigation",
	"edit-data-connector":            "plan-investigation",
	"edit-snapshot-note":             "plan-maintenance-window",
}

const maxNextActions = 4

// addPlaybookHints names the owning playbook when a result is not a clean success, and the write protocol when a result is the dry run of
// an edit. It never repeats an action, never names the skill itself and keeps the list short.
func addPlaybookHints(name string, r *result.Result) {
	add := func(n string) {
		if n == "" || n == name || slices.Contains(r.NextActions, n) || len(r.NextActions) >= maxNextActions {
			return
		}
		r.NextActions = append(r.NextActions, n)
	}
	if r.Status != result.OK {
		add(playbookFor[name])
	}
	// DOGFOOD-TEMP: an error may be a skill bug; point at the issue-reporting playbook.
	if r.Status == result.Error {
		add("plan-report-skill-gap")
	}
	if r.Mode == result.ModeDryRun && len(r.Changes) > 0 {
		add("plan-safe-write")
	}
}
