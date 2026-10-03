package skills_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

// aliasWorld is a snapshot with a store of aliases, served through the alias routes.
func aliasWorld(store map[string]map[string]any) map[string]fwdtest.Handler {
	const base = "/api/snapshots/s1/aliases/"
	return map[string]fwdtest.Handler{
		snapsPath: fwdtest.Snapshots(fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")),
		"GET " + base + "web": func(*http.Request, []byte) (int, any) {
			if a, ok := store["web"]; ok {
				return 200, a
			}
			return 404, map[string]any{"message": "none"}
		},
		"PUT " + base + "web": func(_ *http.Request, body []byte) (int, any) {
			var a map[string]any
			_ = json.Unmarshal(body, &a)
			a["value"] = "resolved by Forward"
			store["web"] = a
			return 200, a
		},
		"DELETE " + base + "web": func(*http.Request, []byte) (int, any) {
			a := store["web"]
			delete(store, "web")
			return 200, a
		},
	}
}

func TestEditAliasPutIsADryRunThenCreatesReadsBackAndIsIdempotent(t *testing.T) {
	store := map[string]map[string]any{}
	in := `{"network_id":"n1","action":"put","name":"web","definition":{"type":"HOSTS","values":["10.1.0.0/24"]}`
	r, srv := mustRun(t, "edit-alias", aliasWorld(store), in+`}`)
	if r.Status != result.OK || r.Mode != result.ModeDryRun || writes(srv) != 0 || !r.Changes[0].Reversible || !strings.Contains(r.Changes[0].Undo, "deactivate") {
		t.Fatalf("dry run: %s %s %+v", r.Status, r.Finding, r.Changes)
	}
	r, srv = mustRun(t, "edit-alias", aliasWorld(store), in+`,"apply":true}`)
	if r.Status != result.OK || !r.Changes[0].Applied || len(store) != 1 || writes(srv) != 1 || !strings.Contains(strings.Join(r.Limits, "|"), "LATER snapshot") {
		t.Fatalf("apply: %s %s store=%v", r.Status, r.Finding, store)
	}
	// Forward adds a resolved value to what it stores: the same definition is still a no-op
	r, srv = mustRun(t, "edit-alias", aliasWorld(store), in+`,"apply":true}`)
	if r.Status != result.OK || writes(srv) != 0 || !strings.Contains(r.Finding, "nothing to change") {
		t.Fatalf("same definition: %s %s writes=%d", r.Status, r.Finding, writes(srv))
	}
	// a different one replaces it, and the undo is the old definition
	r, _ = mustRun(t, "edit-alias", aliasWorld(store), `{"network_id":"n1","action":"put","name":"web","definition":{"type":"HOSTS","values":["10.2.0.0/24"]}}`)
	if !strings.Contains(r.Finding, "replace the definition") || !strings.Contains(jsonOf(r), "10.1.0.0/24") {
		t.Fatalf("replace shows what it replaces: %s %s", r.Finding, jsonOf(r))
	}
}

func TestEditAliasDeactivateAndWhatItRefuses(t *testing.T) {
	store := map[string]map[string]any{"web": {"name": "web", "type": "HOSTS", "values": []any{"10.1.0.0/24"}}}
	r, srv := mustRun(t, "edit-alias", aliasWorld(store), `{"network_id":"n1","action":"deactivate","name":"web"}`)
	if r.Mode != result.ModeDryRun || writes(srv) != 0 || len(store) != 1 || !strings.Contains(strings.Join(r.Limits, "|"), "name can only be used again") {
		t.Fatalf("dry run: %s %+v", r.Finding, r.Changes)
	}
	r, _ = mustRun(t, "edit-alias", aliasWorld(store), `{"network_id":"n1","action":"deactivate","name":"web","apply":true}`)
	if r.Status != result.OK || len(store) != 0 {
		t.Fatalf("apply: %s %s", r.Status, r.Finding)
	}
	if r, _ := mustRun(t, "edit-alias", aliasWorld(store), `{"network_id":"n1","action":"deactivate","name":"web","apply":true}`); !strings.Contains(r.Finding, "nothing to deactivate") {
		t.Errorf("an absent alias is a no-op: %s", r.Finding)
	}
	for _, bad := range []string{
		`{"network_id":"n1","action":"put","name":"web"}`,
		`{"network_id":"n1","action":"put","name":"web","definition":{"type":"DEVICES","locations":["x"]}}`,
		`{"network_id":"n1","action":"put","name":"web","definition":{"type":"HOSTS","bogus":1}}`,
		`{"network_id":"n1","action":"put","name":"web","definition":{"type":"HEADERS","headerValues":{"nope":["1"]}}}`,
		`{"network_id":"n1","action":"deactivate","name":"web","definition":{"type":"HOSTS"}}`,
		`{"network_id":"n1","action":"put","definition":{"type":"HOSTS","values":["a"]}}`,
	} {
		if _, srv, err := runSkill(t, "edit-alias", aliasWorld(store), bad); err == nil || writes(srv) != 0 {
			t.Errorf("%s must be refused before anything is sent: %v", bad, err)
		}
	}
}
