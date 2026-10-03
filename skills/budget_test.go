package skills

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The always-loaded cost of the skill set. A harness that lists skills (Claude Code, Codex, an embedding platform) puts every skill's
// name and description in the model's context on every turn, before any work. Measured at v0.5.24 that was 19,842 characters (about 4,960 tokens
// at 4 characters a token) for 56 skills, and Claude Code gives the listing about 1% of the context window, dropping the descriptions of
// the least-used skills past it. These tests keep the set from growing back.
//
// THE RULE: a skill's description is what it does and "Use when ...", in as few characters as it takes. If a description needs more than
// its cap, the skill is doing two jobs: make it a view (kind or view input) of a sibling or split the detail into SKILL.md or reference/.
// Do not raise a cap. Raise alwaysLoadedBudget only when a new skill is justified by "Adding a skill" in docs/internal.md, and say what
// was added in the comment next to the number.
const (
	maxDescription     = 190   // characters, every skill
	maxPlaybookDesc    = 140   // characters, plan-* playbooks
	maxGapDescription  = 100   // plan-report-skill-gap is DOGFOOD-TEMP and must cost almost nothing in every listing
	alwaysLoadedBudget = 10450 // v0.5.66 measured 10,440 for 57 skills after plan-health-check was folded into plan-incident-triage; +edit-alias (a named group of hosts, devices, interfaces or headers that checks refer to by name: a different object, with its own per-snapshot effect and undo, from every other write skill; its fields do not fit edit-checks within the input ceiling); +edit-data-connector (a per-network HTTP source, a different object and undo from every other write skill, including edit-data-file); +edit-data-file (uploading and attaching a dataset is a different object and undo from every other write skill: upload has none at all); +inspect-access and edit-access (access is a different object, with its own refusal explanation, from every other skill); +edit-org-property (an org-wide setting is a different object and undo from every other write skill);  +edit-workspace (a temporary network to try a collection change off production; a different object and undo from edit-collection and edit-endpoint-profile);  +edit-endpoint-profile (the only way to try an extra OID on one endpoint, a new object with its own undo, not a view of edit-collection);  sum of len(name)+len(description) over all skills; v0.5.26 measured 10,218 for 56 skills, v0.5.27 9,064 for 50 (merged views)
)

func descriptionCap(name string) int {
	switch {
	case name == "plan-report-skill-gap":
		return maxGapDescription
	case strings.HasPrefix(name, "plan-"):
		return maxPlaybookDesc
	}
	return maxDescription
}

func TestEveryDescriptionIsWithinItsCap(t *testing.T) {
	for _, name := range Names() {
		m, err := Describe(name)
		if err != nil {
			t.Fatal(err)
		}
		if c := descriptionCap(name); len(m.Description) > c {
			t.Errorf("%s: description is %d characters, cap %d (over by %d). Say less; put detail in SKILL.md, not the description", name, len(m.Description), c, len(m.Description)-c)
		}
	}
}

func TestAlwaysLoadedBudget(t *testing.T) {
	total := 0
	for _, name := range Names() {
		m, _ := Describe(name)
		total += len(m.Name) + len(m.Description)
	}
	if total > alwaysLoadedBudget {
		t.Errorf("always-loaded listing is %d characters (about %d tokens) for %d skills; the budget is %d. A new skill must justify its cost (docs/internal.md, \"Adding a skill\"); raising the budget is a deliberate edit of alwaysLoadedBudget",
			total, total/4, len(Names()), alwaysLoadedBudget)
	}
	t.Logf("always-loaded: %d characters (about %d tokens), %d skills, budget %d", total, total/4, len(Names()), alwaysLoadedBudget)
}

// ---- Adding a skill: the mechanical part of the gate (docs/internal.md) ----

// maxReadInputs is how many inputs a new read skill may take. A skill that needs more is two skills in one schema, or a view skill whose
// views should each name their own inputs. The skills below pre-date the rule; each may not grow past the number listed, and none may pass maxInputsEver.
const (
	maxReadInputs = 8
	maxInputsEver = 12
)

var wideReadSkills = map[string]int{
	"check-network-compliance": 11,
	"inspect-bgp-neighbors":    12,
	"compare-nqe-results":      9, // after_network_id, key and ignore compare two networks
	"inspect-device-files":     11,
	"investigate-reachability": 10,
	// Merged views (v0.5.27): each view names its own inputs in SKILL.md and rejects another view's, so the schema is wide and the call is not.
	"inspect-edge":                   12, // view exits | public_addresses | trace_sources
	"inspect-inventory":              10, // kind ip_owner adds ips; kind devices adds compare_to_snapshot_id
	"verify-change":                  9,  // view describe adds device
	"inspect-access":                 12, // view activity adds since, until, method and status
	"investigate-collection-failure": 9,  // view slow adds compare_to_snapshot_id
	"inspect-vulnerabilities":        9,  // view devices
}

func inputCount(m Meta) int {
	var s struct {
		Properties map[string]any `json:"properties"`
	}
	_ = json.Unmarshal(m.InputSchema, &s)
	return len(s.Properties)
}

func TestSkillsDeclareAClusterAndSummary(t *testing.T) {
	valid := map[string]bool{}
	for _, c := range Clusters {
		valid[c.Name] = true
	}
	for _, name := range Names() {
		m, _ := Describe(name)
		if !valid[m.Cluster] {
			t.Errorf("%s: metadata cluster %q is not one of the clusters in skills.Clusters; a new skill declares the cluster it sits in", name, m.Cluster)
		}
		if n := len(strings.Fields(m.Summary)); n == 0 || n > 6 {
			t.Errorf("%s: metadata summary %q must be 1 to 6 words (it stands for the skill in the agents block)", name, m.Summary)
		}
	}
}

func TestInputCountsStayBounded(t *testing.T) {
	for _, name := range Names() {
		m, _ := Describe(name)
		n := inputCount(m)
		if n > maxInputsEver {
			t.Errorf("%s takes %d inputs; no skill takes more than %d. Split it, or move rarely used inputs into a view", name, n, maxInputsEver)
		}
		if m.Class != "write" {
			limit := maxReadInputs
			if w, ok := wideReadSkills[name]; ok {
				limit = w
			}
			if n > limit {
				t.Errorf("%s takes %d inputs; a read skill takes at most %d (docs/internal.md, \"Adding a skill\")", name, n, limit)
			}
		}
	}
}

// Every skill but the router is the expected answer of at least one routing case that names the skill it is most likely to be confused with.
// A skill with no case against its rival has never been shown to be distinguishable from it.
func TestEverySkillHasARoutingCaseAgainstARival(t *testing.T) {
	b, err := os.ReadFile("../evals/routing.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Expect string `json:"expect"`
		Rival  string `json:"rival"`
	}
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, c := range cases {
		if c.Expect != "" && c.Rival != "" {
			target, _ := Resolve(c.Expect)
			have[target] = true
			if _, ok := Aliases()[c.Rival]; ok {
				t.Errorf("routing case rival %q is an alias; name the surviving skill", c.Rival)
			}
		}
	}
	for _, name := range Names() {
		if name != "plan-investigation" && !have[name] {
			t.Errorf("%s: no evals/routing.json case expects it with a rival; add 1-2 cases against its nearest rival", name)
		}
	}
}

// Offline routing may not get worse. `fwdctl which` ranks the router table of plan-investigation; this is a floor for that table, not the
// model-based eval (cmd/skill-eval --routing). Raise the floor when you improve the table; never lower it to land a change.
func TestOfflineRoutingDoesNotRegress(t *testing.T) {
	n, top1, top4, misses := routingOffline(t)
	const floor1, floor4 = 70, 92 // v0.5.57 measured 72 and 94 of 101 (duration questions, and edit-* down-weighted for diagnostic ones); v0.5.25 measured 47 and 61 of 72 cases; v0.5.26 55 and 70 of 75; v0.5.27 59 and 79 of 82; v0.5.28 60 and 81 of 84
	t.Logf("offline routing: %d cases, first %d, top four %d", n, top1, top4)
	if top1 < floor1 || top4 < floor4 {
		t.Errorf("offline routing fell: first %d (floor %d), top four %d (floor %d); missed: %s", top1, floor1, top4, floor4, strings.Join(misses, " | "))
	}
}
