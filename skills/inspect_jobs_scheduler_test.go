package skills_test

import (
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

// state scheduler is the job scheduler's point-in-time view (system administrators only): tasks per organization, job type, worker class (REGULAR or LARGE) and status (INITIALIZED, QUEUED, RUNNING), so a slow Predict can be
// told from a queue behind another organization's work. It is a debug dump, not a history, and carries no per-org worker cap.
func TestInspectJobsSchedulerGroupsTasksByOrgClassAndStatus(t *testing.T) {
	task := func(status, class string) map[string]any {
		return map[string]any{"taskStatus": status, "resourceExpectancy": class, "worker": nil}
	}
	view := map[string]any{"COMPUTE": map[string]any{"NORMAL": map[string]any{"idToJobDetails": map[string]any{
		"j1": map[string]any{"jobType": "PREDICT", "orgId": 7, "priority": "NORMAL", "tasks": []any{task("RUNNING", "REGULAR"), task("QUEUED", "REGULAR"), task("QUEUED", "REGULAR")}},
		"j2": map[string]any{"jobType": "PREDICT", "orgId": 9, "priority": "NORMAL", "tasks": []any{task("QUEUED", "LARGE")}},
	}}}}
	routes := map[string]fwdtest.Handler{"GET /api/debug/jobs": fwdtest.Const(200, view)}
	r, srv := mustRun(t, "inspect-jobs", routes, `{"state":"scheduler"}`)
	b := jsonOf(r)
	if r.Status != result.OK || writes(srv) != 0 || !strings.Contains(b, `"worker_class":"LARGE"`) || !strings.Contains(b, `"status":"QUEUED"`) || !strings.Contains(b, `"tasks":2`) {
		t.Fatalf("grouping: %s %s", r.Status, b)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "point-in-time") || !strings.Contains(strings.Join(r.Limits, " "), "worker cap") {
		t.Errorf("limits: %v", r.Limits)
	}
	// match narrows by organization id or job type
	r, _ = mustRun(t, "inspect-jobs", routes, `{"state":"scheduler","match":"9"}`)
	if strings.Contains(jsonOf(r), `"org_id":"7"`) || !strings.Contains(jsonOf(r), `"org_id":"9"`) {
		t.Errorf("match: %s", jsonOf(r))
	}
	// not a system administrator: unknown, not an empty queue
	routes["GET /api/debug/jobs"] = fwdtest.Const(403, map[string]any{"message": "Missing permission: ADMINISTER_SYSTEM"})
	r, _ = mustRun(t, "inspect-jobs", routes, `{"state":"scheduler"}`)
	if r.Status != result.Unknown || !strings.Contains(strings.Join(r.Limits, " "), "ADMINISTER_SYSTEM") {
		t.Errorf("403: %s %v", r.Status, r.Limits)
	}
}
