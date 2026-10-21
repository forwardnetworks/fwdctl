package skills_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

const csChecksPath = "/api/networks/n1/change-sets/cs9/checks"

// checkWorld serves change set cs9 on base s1 with the given checks already attached and records the writes.
type checkWorld struct {
	existing []any
	posted   []string
	patched  []string
	deleted  []string
	status   string // the status Forward answers a new check with
}

func (w *checkWorld) routes() map[string]fwdtest.Handler {
	if w.status == "" {
		w.status = "PASS"
	}
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/change-sets": fwdtest.Const(200, []any{map[string]any{"id": "cs9", "name": "withdraw", "snapshotId": "s1"}}),
		"GET " + csChecksPath:              func(*http.Request, []byte) (int, any) { return 200, map[string]any{"checks": w.existing} },
		"POST " + csChecksPath: func(_ *http.Request, b []byte) (int, any) {
			w.posted = append(w.posted, string(b))
			return 200, map[string]any{"id": "chk7", "name": "x", "status": w.status}
		},
		"PATCH " + csChecksPath + "/chk1":  func(_ *http.Request, b []byte) (int, any) { w.patched = append(w.patched, string(b)); return 204, nil },
		"DELETE " + csChecksPath + "/chk1": func(*http.Request, []byte) (int, any) { w.deleted = append(w.deleted, "chk1"); return 204, nil },
	}
}

const nqeCheck = `"check":{"type":"NQE","query_id":"Q_default_absent","params":{"vrf":"default"}}`
const reachCheck = `"check":{"type":"Reachability","filters":{"from":{"type":"HostFilter","values":["lab"]},"to":{"type":"HostFilter","values":["8.8.8.8"]}}}`

// add_check is a dry run until apply: it validates the definition (Validate refuses combinations Forward refuses), lists what is attached to warn about a duplicate, and posts nothing; Forward has no dry run of its own.
func TestAddCheckDryRunValidatesAndPostsNothing(t *testing.T) {
	w := &checkWorld{}
	r, _ := mustRun(t, "edit-change-set", w.routes(), `{"network_id":"n1","change_set_id":"cs9","lifecycle":{"op":"add_check",`+nqeCheck+`}}`)
	if r.Mode != result.ModeDryRun || len(w.posted) != 0 || !strings.Contains(r.Finding, "cs9") || !strings.Contains(strings.Join(r.Limits, " "), "runs the check on the base snapshot") {
		t.Fatalf("dry run: %s %v %s %v", r.Mode, w.posted, r.Finding, r.Limits)
	}
	// a combination Forward refuses is refused before any request
	if _, _, err := runSkill(t, "edit-change-set", w.routes(), `{"network_id":"n1","change_set_id":"cs9","lifecycle":{"op":"add_check","check":{"type":"NQE","query_id":"Q","filters":{"x":1}}}}`); err == nil {
		t.Errorf("an NQE check with filters is refused")
	}
	if _, _, err := runSkill(t, "edit-change-set", w.routes(), `{"network_id":"n1","change_set_id":"cs9","lifecycle":{"op":"add_check","check":{"type":"Reachability"}}}`); err == nil {
		t.Errorf("an intent check needs filters")
	}
	if _, _, err := runSkill(t, "edit-change-set", w.routes(), `{"network_id":"n1","change_set_id":"cs9","lifecycle":{"op":"add_check","name":"x","check":{"type":"NQE","query_id":"Q"}}}`); err == nil {
		t.Errorf("an NQE check takes no name")
	}
}

// apply posts the check once and reports the status Forward gave it, including a check that was added but failed to run (Forward answers 2xx with ERROR or TIMEOUT), and says to list before re-adding because Forward does not deduplicate.
func TestAddCheckApplyReportsTheStatusIncludingAFailedRun(t *testing.T) {
	w := &checkWorld{}
	r, _ := mustRun(t, "edit-change-set", w.routes(), `{"network_id":"n1","change_set_id":"cs9","apply":true,"lifecycle":{"op":"add_check","name":"lab reaches 8.8.8.8",`+reachCheck+`}}`)
	if r.Status != result.OK || len(w.posted) != 1 || !strings.Contains(w.posted[0], `"checkType":"Reachability"`) || !strings.Contains(w.posted[0], `"name":"lab reaches 8.8.8.8"`) || !strings.Contains(r.Finding, "chk7") || !strings.Contains(r.Finding, "PASS") {
		t.Fatalf("apply: %s %v %s", r.Status, w.posted, r.Finding)
	}
	w2 := &checkWorld{status: "TIMEOUT"}
	r, _ = mustRun(t, "edit-change-set", w2.routes(), `{"network_id":"n1","change_set_id":"cs9","apply":true,"lifecycle":{"op":"add_check",`+nqeCheck+`}}`)
	l := strings.Join(r.Limits, " ") + r.Finding
	if len(w2.posted) != 1 || !strings.Contains(l, "TIMEOUT") || !strings.Contains(l, "did not run") || !strings.Contains(l, "list") {
		t.Errorf("a check that failed to run is said so: %s", l)
	}
}

// A check already attached with the same definition is named as a likely duplicate (Forward adds a second one).
func TestAddCheckWarnsOfAnIdenticalCheckAlreadyThere(t *testing.T) {
	w := &checkWorld{existing: []any{map[string]any{"id": "chk1", "name": "", "status": "PASS", "definition": map[string]any{"checkType": "NQE", "queryId": "Q_default_absent", "params": map[string]any{"vrf": "default"}}}}}
	r, _ := mustRun(t, "edit-change-set", w.routes(), `{"network_id":"n1","change_set_id":"cs9","lifecycle":{"op":"add_check",`+nqeCheck+`}}`)
	if !strings.Contains(strings.Join(r.Limits, " "), "chk1") || !strings.Contains(strings.Join(r.Limits, " "), "duplicate") {
		t.Errorf("duplicate: %v", r.Limits)
	}
}

// edit_check renames or replaces the definition of an attached check; an unknown check id is unknown, not an error. remove_check deletes it, with the definition kept in the plan so it can be added again.
func TestEditAndRemoveCheck(t *testing.T) {
	w := &checkWorld{existing: []any{map[string]any{"id": "chk1", "name": "old", "status": "PASS", "definition": map[string]any{"checkType": "NQE", "queryId": "Q_default_absent"}}}}
	r, _ := mustRun(t, "edit-change-set", w.routes(), `{"network_id":"n1","change_set_id":"cs9","apply":true,"lifecycle":{"op":"edit_check","check_id":"chk1","name":"new name"}}`)
	if r.Status != result.OK || len(w.patched) != 1 || !strings.Contains(w.patched[0], `"name":"new name"`) {
		t.Fatalf("edit: %s %v %s", r.Status, w.patched, r.Finding)
	}
	r, _ = mustRun(t, "edit-change-set", w.routes(), `{"network_id":"n1","change_set_id":"cs9","lifecycle":{"op":"edit_check","check_id":"nope","name":"x"}}`)
	if r.Status != result.Unknown {
		t.Errorf("unknown check id: %s %s", r.Status, r.Finding)
	}
	r, _ = mustRun(t, "edit-change-set", w.routes(), `{"network_id":"n1","change_set_id":"cs9","lifecycle":{"op":"remove_check","check_id":"chk1"}}`)
	if r.Mode != result.ModeDryRun || len(w.deleted) != 0 || !strings.Contains(jsonOf(r), "Q_default_absent") {
		t.Fatalf("remove dry run: %s %v %s", r.Mode, w.deleted, jsonOf(r))
	}
	r, _ = mustRun(t, "edit-change-set", w.routes(), `{"network_id":"n1","change_set_id":"cs9","apply":true,"lifecycle":{"op":"remove_check","check_id":"chk1"}}`)
	if r.Status != result.OK || len(w.deleted) != 1 {
		t.Errorf("remove: %s %v", r.Status, w.deleted)
	}
}

// A colleague measured that an Existential check with returnPath SYMMETRIC is accepted and stored but did not assert symmetry (it passed on a flow whose two directions crossed different backbones, and on every prediction of a change set that moved traffic). The limits say so on add and edit, and name the two-checks-per-direction way to assert symmetry; the claim is attributed as a measurement on one build, not ours.
func TestSymmetricReturnPathCheckCarriesTheNotAssertedCaveat(t *testing.T) {
	w := &checkWorld{}
	const sym = `"check":{"type":"Existential","return_path":"SYMMETRIC","filters":{"from":{"type":"HostFilter","values":["a"]},"to":{"type":"HostFilter","values":["b"]}}}`
	r, _ := mustRun(t, "edit-change-set", w.routes(), `{"network_id":"n1","change_set_id":"cs9","lifecycle":{"op":"add_check","name":"a to b",`+sym+`}}`)
	l := strings.Join(r.Limits, " ")
	for _, want := range []string{"SYMMETRIC", "not asserted", "one build", "one check per direction"} {
		if !strings.Contains(l, want) {
			t.Errorf("limits miss %q: %s", want, l)
		}
	}
	r, _ = mustRun(t, "edit-change-set", w.routes(), `{"network_id":"n1","change_set_id":"cs9","lifecycle":{"op":"add_check",`+reachCheck+`}}`)
	if strings.Contains(strings.Join(r.Limits, " "), "SYMMETRIC") {
		t.Errorf("an ordinary check carries no symmetric caveat")
	}
}
