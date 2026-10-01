package skills_test

import (
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

func blast(t *testing.T, f changeFix) (result.Result, *fwdtest.Server) {
	return mustRun(t, "analyze-blast-radius", f.routes(), csInput+"}")
}

func TestBlastRadiusReportsExtentFromAreasAndConnectivity(t *testing.T) {
	r, _ := blast(t, changeFix{counts: map[string]int{"acl": 4, "interfaces": 9}, conn: conn(false, 10, 3, 1)})
	if r.Status != result.OK || r.Confidence != result.Deterministic {
		t.Fatalf("got %s/%s", r.Status, r.Confidence)
	}
	if !strings.Contains(r.Finding, "interfaces (9)") || !strings.Contains(r.Finding, "3 subnet pair(s) lost connectivity") {
		t.Errorf("finding %q", r.Finding)
	}
	if strings.Index(r.Finding, "interfaces (9)") > strings.Index(r.Finding, "acl (4)") {
		t.Errorf("areas are not ordered by size: %q", r.Finding)
	}
	if r.NextActions[0] != "investigate-reachability" {
		t.Errorf("next %v", r.NextActions)
	}
}

func TestBlastRadiusACompleteZeroEverywhereIsARealAnswer(t *testing.T) {
	r, _ := blast(t, changeFix{})
	if r.Status != result.OK || !strings.Contains(r.Finding, "No observable difference") {
		t.Fatalf("got %s: %s", r.Status, r.Finding)
	}
}

func TestBlastRadiusIncompleteCountsAreNotCounts(t *testing.T) {
	r, _ := blast(t, changeFix{counts: map[string]int{"acl": 5}, incomplete: map[string]bool{"acl": true}})
	if !strings.Contains(strings.Join(r.Limits, " "), "not complete for: acl") {
		t.Errorf("limits %v", r.Limits)
	}
	if strings.Contains(r.Finding, "acl (5)") {
		t.Errorf("an incomplete count was reported as a difference: %q", r.Finding)
	}
}

func TestBlastRadiusNothingMeasuredIsUnknown(t *testing.T) {
	inc := map[string]bool{}
	for _, a := range diffAreas {
		inc[a] = true
	}
	r, _ := blast(t, changeFix{incomplete: inc, conn: conn(true, 0, 0, 0)})
	if r.Status != result.Unknown {
		t.Fatalf("status %s: %s", r.Status, r.Finding)
	}
}

func TestBlastRadiusUnsettledConnectivityIsALimitNotEvidence(t *testing.T) {
	r, _ := blast(t, changeFix{counts: map[string]int{"acl": 2}, conn: conn(true, 0, 0, 0)})
	if r.Status != result.OK || strings.Contains(r.Finding, "lost connectivity") ||
		!strings.Contains(strings.Join(r.Limits, " "), "did not settle") {
		t.Fatalf("got %s %q %v", r.Status, r.Finding, r.Limits)
	}
}

func TestBlastRadiusZeroPairsComparedIsUnmeasuredNotNoChange(t *testing.T) {
	r, _ := blast(t, changeFix{counts: map[string]int{"acl": 2}, conn: conn(false, 0, 0, 0)})
	if !strings.Contains(strings.Join(r.Limits, " "), "no subnet pairs") || strings.Contains(r.Finding, "lost connectivity") {
		t.Fatalf("limits %v finding %q", r.Limits, r.Finding)
	}
}

func TestBlastRadiusNoPredictionIsUnknownAndPredictIsNotRun(t *testing.T) {
	r, srv := blast(t, changeFix{predicted: []map[string]any{}})
	if r.Status != result.Unknown {
		t.Fatalf("status %s", r.Status)
	}
	for _, c := range srv.Calls() {
		if c.Method == "POST" {
			t.Errorf("unexpected %s %s", c.Method, c.Path)
		}
	}
}

func TestBlastRadiusUnavailableAreaIsAnIncompleteAreaNotAZero(t *testing.T) {
	f := changeFix{counts: map[string]int{"acl": 1}}
	routes := f.routes()
	routes["GET /api/diffs/b1/a1/l2"] = fwdtest.Const(404, map[string]any{"message": "not found"})
	r, _ := mustRun(t, "analyze-blast-radius", routes, csInput+"}")
	if !strings.Contains(strings.Join(r.Limits, " "), "l2") {
		t.Errorf("limits %v", r.Limits)
	}
}

func TestBlastRadiusMissingSelectorIsAnErrorResult(t *testing.T) {
	r, _ := mustRun(t, "analyze-blast-radius", map[string]fwdtest.Handler{}, `{"network_id":"n1"}`)
	if r.Status != result.Error {
		t.Fatalf("status %s", r.Status)
	}
}

func TestBlastRadiusAConnectivityFeatureSwitchedOffStillReportsTheAreaCounts(t *testing.T) {
	f := changeFix{counts: map[string]int{"acl": 2}}
	routes := f.routes()
	routes[connPath] = fwdtest.Const(403, map[string]any{"message": "LOCATION_CONNECTIVITY_DIFFS is off for your organization"})
	r, _ := mustRun(t, "analyze-blast-radius", routes, csInput+"}")
	if r.Status != result.OK || !strings.Contains(r.Finding, "acl (2)") || !strings.Contains(strings.Join(r.Limits, " "), "is off") {
		t.Fatalf("got %s %q %v", r.Status, r.Finding, r.Limits)
	}
}
