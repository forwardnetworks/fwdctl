package skills_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

const goodQuery = "foreach d in network.devices select {name: d.name}"

// libraryRoutes is a library holding one query ("/Team/q" with source prior, when prior is not empty); a commit makes the pending
// draft the committed state.
func libraryRoutes(prior string, held bool) map[string]fwdtest.Handler {
	state := prior
	pending, deleted := "", false
	return map[string]fwdtest.Handler{
		"GET /api/nqe/repos/org/commits/head": fwdtest.Const(200, "c1"),
		"GET /api/nqe/repos/org/commits/head/queries": func(*http.Request, []byte) (int, any) {
			// /Team/other keeps the /Team/ directory known to exist, as a real library's neighbours do
			anchor := map[string]any{"path": "/Team/other", "lastCommitId": "c1", "queryId": "Q_0"}
			if state == "" {
				return 200, map[string]any{"queries": []any{anchor}}
			}
			return 200, map[string]any{"queries": []any{anchor, map[string]any{"path": "/Team/q", "lastCommitId": "c0", "queryId": "Q_1"}}}
		},
		"GET /api/nqe/repos/org/commits/c1/queries": func(*http.Request, []byte) (int, any) {
			if state == "" {
				return 404, map[string]any{"message": "no such query"}
			}
			return 200, map[string]any{"sourceCode": state, "queryId": "Q_1"}
		},
		"GET /api/users/current/nqe/changes": fwdtest.Const(200, map[string]any{"changes": []any{}}),
		// Forward's rules: a path in HEAD cannot be added (409), only edited with a basis; a new one is added
		"POST /api/users/current/nqe/changes": func(r *http.Request, body []byte) (int, any) {
			switch r.URL.Query().Get("action") {
			case "addQuery":
				if state != "" {
					return 409, map[string]any{"message": "path already exists in HEAD", "reason": "ADD_QUERY_ALREADY_EXISTS"}
				}
				pending, deleted = stageSource(body), false
			case "editQuery":
				if state == "" || !strings.Contains(string(body), `"basis":{"queryId":"Q_1","commitId":"c0"}`) {
					return 409, map[string]any{"message": "bad edit", "reason": "PATH_MISSING_IN_HEAD"}
				}
				pending, deleted = stageSource(body), false
			case "bulkDiscard":
			default:
				pending, deleted = "", true
			}
			return 200, map[string]any{}
		},
		"POST /api/nqe/repos/org/commits": func(*http.Request, []byte) (int, any) {
			if held {
				if deleted {
					state = ""
				} else {
					state = pending
				}
			}
			return 200, map[string]any{}
		},
	}
}

func TestEditNQEQueryDryRunShowsBeforeAndAfterAndWritesNothing(t *testing.T) {
	r, srv := mustRun(t, "edit-nqe-query", libraryRoutes("old source", true), `{"path":"/Team/q","source":"`+goodQuery+`"}`)
	if r.Status != result.OK || r.Mode != result.ModeDryRun || len(r.Changes) != 1 || r.Changes[0].Before != "old source" || r.Changes[0].Applied {
		t.Fatalf("%s %s %+v", r.Status, r.Mode, r.Changes)
	}
	if writes(srv) != 0 {
		t.Fatalf("a dry run must not write: %+v", srv.Calls())
	}
}

func TestEditNQEQueryRefusesSourceThatDoesNotPassTheOfflineCheck(t *testing.T) {
	needCorpora(t)
	r, srv := mustRun(t, "edit-nqe-query", libraryRoutes("", true), `{"path":"/Team/q","source":"foreach d in network.devices select {n: d.nmae}","apply":true}`)
	if r.Status != result.Failed || writes(srv) != 0 || !strings.Contains(r.Finding, "offline check") {
		t.Fatalf("%s %s writes=%d", r.Status, r.Finding, writes(srv))
	}
}

func TestEditNQEQueryApplyCommitsThenReadsItBack(t *testing.T) {
	r, srv := mustRun(t, "edit-nqe-query", libraryRoutes("", true), `{"path":"/Team/q","source":"`+goodQuery+`","apply":true}`)
	if r.Status != result.OK || r.Mode != result.ModeApplied || !r.Changes[0].Applied || writes(srv) != 2 {
		t.Fatalf("%s %s %+v writes=%d", r.Status, r.Mode, r.Changes, writes(srv))
	}
	if !strings.Contains(r.Changes[0].Undo, "delete=true") {
		t.Errorf("a new query is undone by deleting it: %s", r.Changes[0].Undo)
	}
	// a commit the library does not keep is failed, not ok
	r, _ = mustRun(t, "edit-nqe-query", libraryRoutes("", false), `{"path":"/Team/q","source":"`+goodQuery+`","apply":true}`)
	if r.Status != result.Failed {
		t.Fatalf("a library that does not hold the query must be failed: %s", r.Status)
	}
}

func TestEditNQEQueryChangesAnExistingQueryWithEditNotAdd(t *testing.T) {
	r, srv := mustRun(t, "edit-nqe-query", libraryRoutes("old source", true), `{"path":"/Team/q","source":"`+goodQuery+`","apply":true}`)
	if r.Status != result.OK || !r.Changes[0].Applied || writes(srv) != 2 {
		t.Fatalf("an existing query is edited (Forward refuses adding it): %s %s writes=%d", r.Status, r.Finding, writes(srv))
	}
}

func TestEditNQEQueryDeletesAndNoOps(t *testing.T) {
	r, srv := mustRun(t, "edit-nqe-query", libraryRoutes("old source", true), `{"path":"/Team/q","delete":true,"apply":true}`)
	if r.Status != result.OK || !r.Changes[0].Applied || r.Changes[0].Action != "delete_query" || writes(srv) != 2 {
		t.Fatalf("%s %+v writes=%d", r.Status, r.Changes, writes(srv))
	}
	r, srv = mustRun(t, "edit-nqe-query", libraryRoutes("", true), `{"path":"/Team/q","delete":true,"apply":true}`)
	if r.Status != result.OK || len(r.Changes) != 0 || writes(srv) != 0 {
		t.Fatalf("deleting what is not there changes nothing: %s %+v", r.Status, r.Changes)
	}
	r, srv = mustRun(t, "edit-nqe-query", libraryRoutes(goodQuery, true), `{"path":"/Team/q","source":"`+goodQuery+`","apply":true}`)
	if len(r.Changes) != 0 || writes(srv) != 0 {
		t.Fatalf("the same source changes nothing: %+v", r.Changes)
	}
}

func TestEditNQEQueryChecksItsInputs(t *testing.T) {
	for _, in := range []string{`{"path":"no-slash","source":"1"}`, `{"path":"/a/../b","source":"1"}`, `{"path":"/a"}`, `{"path":"/a","source":"x","delete":true}`} {
		if _, _, err := runSkill(t, "edit-nqe-query", libraryRoutes("", true), in); err == nil {
			t.Errorf("must refuse: %s", in)
		}
	}
}

func TestFindNQEQueryReadsASavedQueryByPathAndByID(t *testing.T) {
	routes := libraryRoutes(goodQuery, true)
	routes["GET /api/nqe/queries/Q_1/source-code"] = fwdtest.Const(200, map[string]any{"sourceCode": goodQuery, "intent": "list devices"})
	r, _ := mustRun(t, "find-nqe-query", routes, `{"network_id":"n1","path":"/Team/q"}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "/Team/q") || !strings.Contains(evText(r), "foreach d in network.devices") {
		t.Fatalf("by path: %s %s", r.Status, r.Finding)
	}
	r, _ = mustRun(t, "find-nqe-query", routes, `{"network_id":"n1","query_id":"Q_1"}`)
	if r.Status != result.OK || !strings.Contains(evText(r), "list devices") {
		t.Fatalf("by id: %s %s", r.Status, r.Finding)
	}
	// the by-id read must say where the query lives (it comes from the head listing), so an export can place the file
	if got := r.Evidence[0].Detail["path"]; got != "/Team/q" {
		t.Errorf("by id must carry the library path, got %q", got)
	}
	r, _ = mustRun(t, "find-nqe-query", routes, `{"network_id":"n1","path":"/Team/none"}`)
	if r.Status != result.Unknown {
		t.Fatalf("a path that is not there is unknown: %s", r.Status)
	}
	r, _ = mustRun(t, "find-nqe-query", routes, `{"network_id":"n1","query_id":"FQ_9"}`)
	if r.Status != result.Unknown {
		t.Fatalf("a built-in id is not read: %s", r.Status)
	}
	if _, _, err := runSkill(t, "find-nqe-query", routes, `{"network_id":"n1","question":"x","path":"/a"}`); err == nil {
		t.Error("question with path must be refused")
	}
}

func evText(r result.Result) string {
	b, _ := json.Marshal(r.Evidence)
	return string(b)
}

func jsonUnmarshal(b []byte, v any) error {
	return json.Unmarshal([]byte(strings.ToLower(string(b))), v)
}

func TestEditNQEQueryNamesAMissingEnclosingDirectoryAndCreatesItOnRequest(t *testing.T) {
	// /Acme/ has no committed query, so it does not exist: failed, nothing written, and it says how to proceed
	r, srv := mustRun(t, "edit-nqe-query", libraryRoutes("", true), `{"path":"/Acme/DIR L3VPN connections","source":"`+goodQuery+`"}`)
	if r.Status != result.Failed || writes(srv) != 0 || !strings.Contains(r.Finding, "/Acme/") || !strings.Contains(strings.Join(r.Limits, " "), "create_directory") {
		t.Fatalf("%s %s %v writes=%d", r.Status, r.Finding, r.Limits, writes(srv))
	}
	// a root-level query needs no directory
	if r, _ := mustRun(t, "edit-nqe-query", libraryRoutes("", true), `{"path":"/Acme DIR L3VPN connections","source":"`+goodQuery+`"}`); r.Status != result.OK {
		t.Fatalf("a root-level query is fine: %s %s", r.Status, r.Finding)
	}
	// with create_directory the dry run lists the directory as its own change
	r, srv = mustRun(t, "edit-nqe-query", libraryRoutes("", true), `{"path":"/Acme/Sub/q","source":"`+goodQuery+`","create_directory":true}`)
	if r.Status != result.OK || len(r.Changes) != 3 || r.Changes[1].Action != "create_directory" || r.Changes[1].After != "/Acme/" || r.Changes[2].After != "/Acme/Sub/" || writes(srv) != 0 {
		t.Fatalf("%s %+v writes=%d", r.Status, r.Changes, writes(srv))
	}
	// apply: directories first (parents before children), then the query, then ONE commit that names the directories and the query
	r, srv = mustRun(t, "edit-nqe-query", libraryRoutes("", true), `{"path":"/Acme/Sub/q","source":"`+goodQuery+`","create_directory":true,"apply":true}`)
	var actions []string
	var commitPaths any
	for _, c := range srv.Calls() {
		if c.Method == "POST" && strings.HasPrefix(c.Path, "/api/users/current/nqe/changes") {
			actions = append(actions, c.Query["action"]+" "+c.Query["path"])
		}
		if c.Method == "POST" && c.Path == "/api/nqe/repos/org/commits" {
			commitPaths = c.Body["paths"]
		}
	}
	if got := strings.Join(actions, ";"); got != "addDir /Acme/;addDir /Acme/Sub/;addQuery /Acme/Sub/q" {
		t.Errorf("order of changes: %s", got)
	}
	if got := strings.Join(toStrings(commitPaths), ";"); got != "/Acme/Sub/q" {
		t.Errorf("Forward refuses a commit that names a directory: only the query may be in it, got %v", commitPaths)
	}
}

func toStrings(v any) []string {
	var out []string
	if l, ok := v.([]any); ok {
		for _, x := range l {
			out = append(out, fmt.Sprint(x))
		}
	}
	return out
}
