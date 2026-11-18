package skills_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

const checksPath = "/api/snapshots/s1/checks"

func mcRoutes(existing ...map[string]any) map[string]fwdtest.Handler {
	list := make([]any, 0, len(existing))
	for _, e := range existing {
		list = append(list, e)
	}
	return map[string]fwdtest.Handler{
		snapsPath:           ready1(),
		"GET " + checksPath: fwdtest.Const(200, list),
		"POST " + checksPath: func(r *http.Request, _ []byte) (int, any) {
			return 200, map[string]any{"id": "77", "name": "no telnet", "status": "PASS"}
		},
		"GET " + checksPath + "/9":    fwdtest.Const(200, map[string]any{"id": "9", "name": "noisy", "status": "FAIL", "definition": map[string]any{"checkType": "Predefined"}}),
		"DELETE " + checksPath + "/9": fwdtest.Const(200, map[string]any{}),
	}
}

// a named NQE check must be parameterized (Forward: "'name' required for parameterized NQE checks", and "not allowed" without params), so the fixture carries params
const mcCreate = `{"network_id":"n1","action":"create","name":"no telnet","definition":{"checkType":"NQE","queryId":"Q1","params":{"port":23}}`

func TestManageChecksDryRunWritesNothing(t *testing.T) {
	for _, in := range []string{mcCreate + `}`, `{"network_id":"n1","action":"deactivate","check_id":"9"}`} {
		r, srv := mustRun(t, "edit-checks", mcRoutes(), in)
		if r.Status != result.OK || r.Mode != result.ModeDryRun || len(r.Changes) != 1 || r.Changes[0].Applied {
			t.Fatalf("%s: %s %s %+v", in, r.Status, r.Mode, r.Changes)
		}
		if writes(srv) != 0 {
			t.Fatalf("a dry run must not write: %+v", srv.Calls())
		}
	}
}

func TestManageChecksCreateIsSnapshotLocalUnlessPersistentAndNamesTheUndo(t *testing.T) {
	r, srv := mustRun(t, "edit-checks", mcRoutes(), mcCreate+`,"apply":true}`)
	if r.Status != result.OK || r.Mode != result.ModeApplied || !strings.Contains(r.Changes[0].Undo, "check_id=77") || writes(srv) != 1 {
		t.Fatalf("%s %+v writes=%d", r.Status, r.Changes, writes(srv))
	}
	var q string
	for _, c := range srv.Calls() {
		if c.Method == "POST" {
			q = c.Query["persistent"]
		}
	}
	if q != "false" {
		t.Errorf("a create without persistent must ask Forward for persistent=false, sent %q", q)
	}
	_, srv = mustRun(t, "edit-checks", mcRoutes(), mcCreate+`,"apply":true,"persistent":true}`)
	for _, c := range srv.Calls() {
		if c.Method == "POST" && c.Query["persistent"] != "true" {
			t.Errorf("persistent:true must be sent, got %q", c.Query["persistent"])
		}
	}
}

func TestManageChecksRefusesADuplicateNameAndAMissingCheck(t *testing.T) {
	r, srv := mustRun(t, "edit-checks", mcRoutes(map[string]any{"id": "5", "name": "no telnet", "status": "PASS"}), mcCreate+`,"apply":true}`)
	if r.Status != result.Unknown || writes(srv) != 0 || !strings.Contains(r.Finding, "already exists") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	routes := mcRoutes()
	routes["GET "+checksPath+"/9"] = fwdtest.Const(404, map[string]any{})
	r, srv = mustRun(t, "edit-checks", routes, `{"network_id":"n1","action":"deactivate","check_id":"9","apply":true}`)
	if r.Status != result.Unknown || writes(srv) != 0 {
		t.Fatalf("%s", r.Status)
	}
}

func TestManageChecksDeactivateIsOneCheckAndSaysItIsIrreversible(t *testing.T) {
	r, srv := mustRun(t, "edit-checks", mcRoutes(), `{"network_id":"n1","action":"deactivate","check_id":"9","apply":true}`)
	var del []string
	for _, c := range srv.Calls() {
		if c.Method == "DELETE" {
			del = append(del, c.Path)
		}
	}
	if r.Status != result.OK || len(del) != 1 || del[0] != checksPath+"/9" {
		t.Fatalf("exactly the one check, never the whole snapshot: %v", del)
	}
	if r.Changes[0].Reversible || !strings.Contains(r.Finding, "cannot be reactivated") || !strings.Contains(jsonOf(r), "Predefined") {
		t.Errorf("%+v %s", r.Changes[0], r.Finding)
	}
}

func TestManageChecksRejectsBadInput(t *testing.T) {
	for _, in := range []string{`{"network_id":"n1","action":"create"}`, `{"network_id":"n1","action":"deactivate"}`, `{"network_id":"n1","action":"wipe"}`} {
		if _, _, err := runSkill(t, "edit-checks", mcRoutes(), in); err == nil {
			t.Errorf("must refuse %s", in)
		}
	}
}

func TestEditChecksRefusesANameOnAPredefinedCheckBeforeAnythingIsSent(t *testing.T) {
	_, srv, err := runSkill(t, "edit-checks", mcRoutes(), `{"network_id":"n1","action":"create","name":"loops","definition":{"checkType":"Predefined","predefinedCheckType":"LOOP"},"apply":true}`)
	if err == nil || !strings.Contains(err.Error(), "Predefined check takes no name") || writes(srv) != 0 {
		t.Fatalf("a predefined check takes no name: %v", err)
	}
}

// Forward's NewNetworkCheck refuses some name, note and tags combinations with a 400 that only the apply would show; the dry run says so first and sends nothing (the rules are the SDK's
// ValidateNewCheck, read from Forward's source): an NQE check takes a name only when it has params (and then requires one), never a note or tags; tags are for existential, isolation and
// reachability checks only.
func TestEditChecksAppliesForwardsFieldRulesInTheDryRunBeforeAnythingIsSent(t *testing.T) {
	refused := map[string]struct{ body, want string }{
		"non-parameterized NQE with a name": {`{"network_id":"n1","action":"create","name":"x","definition":{"checkType":"NQE","queryId":"Q1"}}`, "takes no name"},
		"parameterized NQE without a name":  {`{"network_id":"n1","action":"create","definition":{"checkType":"NQE","queryId":"Q1","params":{"a":1}}}`, "requires a name"},
		"NQE with a note":                   {`{"network_id":"n1","action":"create","definition":{"checkType":"NQE","queryId":"Q1"},"note":"n"}`, "takes no note"},
		"predefined with tags":              {`{"network_id":"n1","action":"create","definition":{"checkType":"Predefined","predefinedCheckType":"LOOP"},"tags":["a"]}`, "tags are only allowed"},
	}
	for name, c := range refused {
		for _, apply := range []string{"", `,"apply":true`} { // the dry run refuses too, not only the apply
			body := strings.TrimSuffix(c.body, "}") + apply + "}"
			_, srv, err := runSkill(t, "edit-checks", mcRoutes(), body)
			if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "nothing was sent to Forward") || writes(srv) != 0 {
				t.Errorf("%s (apply %q): %v", name, apply, err)
			}
		}
	}
	for name, body := range map[string]string{
		"existential with tags and a note":           `{"network_id":"n1","action":"create","definition":{"checkType":"Existential","filters":{"from":{"type":"HostFilter","values":["a"]},"to":{"type":"HostFilter","values":["b"]}}},"tags":["a"],"note":"n"}`,
		"predefined with a note and no name":         `{"network_id":"n1","action":"create","definition":{"checkType":"Predefined","predefinedCheckType":"LOOP"},"note":"n"}`,
		"non-parameterized NQE with no name or note": `{"network_id":"n1","action":"create","definition":{"checkType":"NQE","queryId":"Q1"}}`,
	} {
		if r, _, err := runSkill(t, "edit-checks", mcRoutes(), body); err != nil || r.Status != result.OK {
			t.Errorf("%s is a valid combination and the dry run must plan it: %v %s", name, err, r.Status)
		}
	}
}
