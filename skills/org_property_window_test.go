package skills_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/knowledge"
	"github.com/forwardnetworks/fwdctl/result"
	"github.com/forwardnetworks/fwdctl/skills"
)

// windowWorld is one organization with two networks: n1 holds the snapshot to reprocess, n2 is where a collection or processing may interfere. The parsing-mode property
// starts at original (an override, or none), and the fake records every change to it.
type windowWorld struct {
	prop       map[string]any // effective values
	configured map[string]any
	puts       []string
	deletes    []string
	n1State    string // state of s1 after the reprocess starts
	n2Snaps    []map[string]any
	schedules  []any
	failPut    int  // fail the Nth PUT (1-based), 0 never
	lateBusy   bool // a snapshot in n2 starts processing once the reprocess has begun
	reprocess  int
	polls      int
}

func newWindowWorld() *windowWorld {
	return &windowWorld{prop: map[string]any{"fortinet_parsing_mode": "PV2_AND_PV3", "time_zone": "UTC"}, configured: map[string]any{}, n1State: "PROCESSED"}
}

func (w *windowWorld) routes() map[string]fwdtest.Handler {
	r := cfgRoutes(w.prop, w.configured, w.configured, w.configured, map[string]any{"fortinet_parsing_mode": "PV2_AND_PV3", "time_zone": "UTC"})
	r["GET /api/networks"] = fwdtest.Const(200, []any{map[string]any{"id": "n1", "name": "one"}, map[string]any{"id": "n2", "name": "two"}})
	state := "PROCESSED"
	r["GET /api/networks/n1/snapshots"] = func(*http.Request, []byte) (int, any) {
		if w.reprocess > 0 {
			w.polls++
			if w.polls >= 2 {
				state = w.n1State
			} else {
				state = "PROCESSING"
			}
		}
		return 200, []any{fwdtest.Snap("s1", state, "COLLECTION", "2026-09-30T00:00:00.000Z")}
	}
	r["GET /api/networks/n2/snapshots"] = func(*http.Request, []byte) (int, any) {
		out := append([]any{}, anySlice(w.n2Snaps)...)
		if w.lateBusy && w.reprocess > 0 {
			out = append(out, fwdtest.Snap("late", "PROCESSING", "COLLECTION", "2026-10-01T12:01:00.000Z"))
		}
		return 200, out
	}
	r["GET /api/networks/n1/collection-schedules"] = fwdtest.Const(200, []any{})
	r["GET /api/networks/n2/collection-schedules"] = func(*http.Request, []byte) (int, any) { return 200, w.schedules }
	r["POST /api/snapshots/s1"] = func(*http.Request, []byte) (int, any) {
		w.reprocess++
		return 200, map[string]any{"previousState": "PROCESSED", "state": "PROCESSING"}
	}
	r["PUT /api/config/fortinet_parsing_mode"] = func(q *http.Request, _ []byte) (int, any) {
		w.puts = append(w.puts, q.URL.Query().Get("value"))
		if w.failPut > 0 && len(w.puts) == w.failPut {
			return 500, map[string]any{"message": "boom"}
		}
		w.prop["fortinet_parsing_mode"], w.configured["fortinet_parsing_mode"] = q.URL.Query().Get("value"), q.URL.Query().Get("value")
		return 200, w.prop
	}
	r["DELETE /api/config/fortinet_parsing_mode"] = func(*http.Request, []byte) (int, any) {
		w.deletes = append(w.deletes, "x")
		w.prop["fortinet_parsing_mode"] = "PV2_AND_PV3"
		delete(w.configured, "fortinet_parsing_mode")
		return 204, nil
	}
	return r
}

func anySlice(m []map[string]any) []any {
	out := make([]any, len(m))
	for i, x := range m {
		out[i] = x
	}
	return out
}

const windowIn = `{"property":"FORTINET_PARSING_MODE","value":"PV3_ONLY","network_id":"n1","reprocess_snapshot_id":"s1","window_minutes":30`

func needWindowModel(t *testing.T) {
	if len(knowledge.OrgPropertyNames()) == 0 {
		t.Skip("this build carries no property table")
	}
	t.Cleanup(skills.WindowTestHooks(time.Millisecond, func() time.Time { return time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC) }))
}

func TestWindowDryRunIsClearWhenNothingIsProcessingAndNothingIsDue(t *testing.T) {
	needWindowModel(t)
	w := newWindowWorld()
	r, srv := mustRun(t, "edit-org-property", w.routes(), windowIn+`}`)
	if r.Status != result.OK || r.Mode != result.ModeDryRun || writes(srv) != 0 || !strings.Contains(r.Finding, "window is clear") {
		t.Fatalf("%s %s writes=%d", r.Status, r.Finding, writes(srv))
	}
}

func TestWindowRefusesWhileAnotherNetworkIsProcessingOrACollectionIsDue(t *testing.T) {
	needWindowModel(t)
	w := newWindowWorld()
	w.n2Snaps = []map[string]any{fwdtest.Snap("b", "PROCESSING", "COLLECTION", "2026-10-01T11:00:00.000Z")}
	r, srv := mustRun(t, "edit-org-property", w.routes(), windowIn+`,"apply":true}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "Window blocked") || !strings.Contains(r.Finding, "snapshot b is PROCESSING") || len(w.puts) != 0 || w.reprocess != 0 {
		t.Fatalf("%s %s puts=%v", r.Status, r.Finding, w.puts)
	}
	_ = srv
	// a time-of-day schedule that fires inside the window blocks; one outside does not
	w = newWindowWorld()
	w.schedules = []any{map[string]any{"id": "1", "enabled": true, "timeZone": "UTC", "times": []string{"12:10"}}}
	r, _ = mustRun(t, "edit-org-property", w.routes(), windowIn+`}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "a schedule fires at 2026-10-01T12:10:00Z") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	w.schedules = []any{map[string]any{"id": "1", "enabled": true, "timeZone": "UTC", "times": []string{"20:00"}}}
	r, _ = mustRun(t, "edit-org-property", w.routes(), windowIn+`}`)
	if r.Status != result.OK {
		t.Fatalf("a schedule after the window must not block: %s %s", r.Status, r.Finding)
	}
	// a periodic schedule with no known last collection blocks (unknown is never a pass)
	w.n2Snaps = nil
	w.schedules = []any{map[string]any{"id": "2", "enabled": true, "periodInSeconds": 3600}}
	r, _ = mustRun(t, "edit-org-property", w.routes(), windowIn+`}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "no next-run time") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
}

func TestWindowSetsReprocessesAndRestoresTheOriginalValue(t *testing.T) {
	needWindowModel(t)
	w := newWindowWorld()
	r, _ := mustRun(t, "edit-org-property", w.routes(), windowIn+`,"apply":true}`)
	if r.Status != result.OK || w.reprocess != 1 || len(w.puts) != 1 || w.puts[0] != "PV3_ONLY" || len(w.deletes) != 1 || w.prop["fortinet_parsing_mode"] != "PV2_AND_PV3" {
		t.Fatalf("%s %s puts=%v deletes=%v reprocess=%d", r.Status, r.Finding, w.puts, w.deletes, w.reprocess)
	}
	if !strings.Contains(r.Finding, "original value PV2_AND_PV3 was put back") {
		t.Errorf("%s", r.Finding)
	}
	// an existing override is set back to its value, not cleared
	w = newWindowWorld()
	w.prop["fortinet_parsing_mode"], w.configured["fortinet_parsing_mode"] = "PV2_ONLY", "PV2_ONLY"
	r, _ = mustRun(t, "edit-org-property", w.routes(), windowIn+`,"apply":true}`)
	if r.Status != result.OK || len(w.puts) != 2 || w.puts[1] != "PV2_ONLY" || len(w.deletes) != 0 {
		t.Fatalf("%s %s puts=%v deletes=%v", r.Status, r.Finding, w.puts, w.deletes)
	}
}

func TestWindowRestoresEvenWhenTheReprocessFailsOrNeverFinishes(t *testing.T) {
	needWindowModel(t)
	w := newWindowWorld()
	w.n1State = "FAILED"
	r, _ := mustRun(t, "edit-org-property", w.routes(), windowIn+`,"apply":true}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "ended FAILED") || w.prop["fortinet_parsing_mode"] != "PV2_AND_PV3" || len(w.deletes) != 1 {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	// the restore itself fails: loud
	w = newWindowWorld()
	w.failPut = 0
	routes := w.routes()
	routes["DELETE /api/config/fortinet_parsing_mode"] = fwdtest.Const(500, map[string]any{"message": "boom"})
	r, _ = mustRun(t, "edit-org-property", routes, windowIn+`,"apply":true}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "RESTORE FAILED") || !strings.Contains(r.Finding, "Set it back to PV2_AND_PV3") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
}

func TestWindowReportsWhatBeganProcessingElsewhereAndATimeoutStillRestores(t *testing.T) {
	needWindowModel(t)
	w := newWindowWorld()
	w.lateBusy = true
	r, _ := mustRun(t, "edit-org-property", w.routes(), windowIn+`,"apply":true}`)
	if r.Status != result.OK || !strings.Contains(strings.Join(r.Limits, " "), "began processing during the window") || !strings.Contains(strings.Join(r.Limits, " "), "snapshot late") {
		t.Fatalf("%s %s %v", r.Status, r.Finding, r.Limits)
	}
	// the reprocess never finishes within the window: failed, original value back
	w = newWindowWorld()
	w.n1State = "PROCESSING"
	tick := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	t.Cleanup(skills.WindowTestHooks(time.Millisecond, func() time.Time { tick = tick.Add(40 * time.Minute); return tick }))
	r, _ = mustRun(t, "edit-org-property", w.routes(), windowIn+`,"window_minutes":1,"apply":true}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "still") || w.prop["fortinet_parsing_mode"] != "PV2_AND_PV3" {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
}
