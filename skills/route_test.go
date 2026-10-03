package skills

import "testing"

// Route is the offline first guess: each of these questions must put the expected skill first or second. (The routing eval, which uses a
// model, is the real measure; this keeps the table and the matcher from drifting apart.)
func TestRouteFindsTheRightSkillOrPlaybook(t *testing.T) {
	for q, want := range map[string]string{
		"Why can't A reach B on tcp/443?":                             "plan-troubleshoot-connectivity",
		"Which CVEs affect our devices?":                              "inspect-vulnerabilities",
		"Is the network healthy?":                                     "plan-incident-triage",
		"What changed since last week?":                               "plan-what-changed",
		"Are the PCI and corporate zones isolated?":                   "plan-segmentation-check",
		"Do we comply with the logging policy? I need audit evidence": "plan-compliance-audit",
		"Tag these devices as edge":                                   "edit-device-tags",
		"How many devices per vendor?":                                "inspect-inventory",
		"Is it safe to make this ACL change?":                         "plan-change-review",
		"Tell me about device atl-ce01":                               "plan-device-audit",
	} {
		got, err := Route(q, 3)
		if err != nil || len(got) == 0 {
			t.Errorf("%q: %v %v", q, got, err)
			continue
		}
		ok := false
		for i, s := range got {
			ok = ok || (i < 2 && s.Skill == want)
		}
		if !ok {
			t.Errorf("%q: want %s in the top two, got %+v", q, want, got)
		}
	}
	if got, _ := Route("What is the capital of France?", 3); len(got) != 0 {
		t.Errorf("an unrelated question must match nothing: %+v", got)
	}
}
