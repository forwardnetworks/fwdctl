package skills_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

const goodQueryB = "foreach d in network.devices select {n: d.name, v: d.platform.vendor}"

// multiLibrary holds /Team/a and /Team/b; staging records drafts, a commit applies them to the next head (c2) and Forward's typecheck says errs.
var failStage = map[string]bool{}

// stageSource reads the source out of an add or edit body.
func stageSource(body []byte) string {
	var b struct {
		SourceCode string `json:"sourceCode"`
	}
	_ = json.Unmarshal(body, &b)
	return b.SourceCode
}

// drafts is the fake library's workspace, reachable from the tests that put a draft there first or read what is left.
var drafts = map[string]string{}

func multiLibrary(head *string, dry map[string]any) (map[string]fwdtest.Handler, map[string]string, *[]string) {
	state := map[string]string{"/Team/a": "foreach d in network.devices select {a: d.name}", "/Team/b": "foreach d in network.devices select {b: d.name}"}
	draft := drafts
	for k := range draft {
		delete(draft, k)
	}
	for k := range failStage {
		delete(failStage, k)
	}
	var log []string
	src := func(r *http.Request, _ []byte) (int, any) {
		return 200, map[string]any{"sourceCode": state[r.URL.Query().Get("path")], "queryId": "Q_x"}
	}
	return map[string]fwdtest.Handler{
		"GET /api/nqe/repos/org/commits/head": func(*http.Request, []byte) (int, any) { return 200, *head },
		"GET /api/nqe/repos/org/commits/head/queries": func(*http.Request, []byte) (int, any) {
			var qs []any
			for p := range state { // committed queries only: a staged draft is not in the head listing
				qs = append(qs, map[string]any{"path": p, "lastCommitId": "c0", "queryId": "Q_" + strings.TrimPrefix(p, "/Team/")})
			}
			return 200, map[string]any{"queries": qs}
		},
		"GET /api/nqe/repos/org/commits/c1/queries": src,
		"GET /api/nqe/repos/org/commits/c2/queries": src,
		"GET /api/users/current/nqe/changes": func(r *http.Request, _ []byte) (int, any) {
			if p := r.URL.Query().Get("path"); p != "" {
				if src, ok := draft[p]; ok {
					return 200, map[string]any{"sourceCode": src}
				}
				return 404, map[string]any{"message": "No draft query at " + p}
			}
			var cs []any
			for p := range draft {
				cs = append(cs, map[string]any{"type": "QUERY_EDIT", "basis": map[string]any{"queryId": "Q_x", "commitId": "c1", "path": p}})
			}
			return 200, map[string]any{"changes": cs}
		},
		"DELETE /api/users/current/nqe/changes": func(r *http.Request, _ []byte) (int, any) {
			delete(draft, r.URL.Query().Get("path"))
			log = append(log, "discard "+r.URL.Query().Get("path"))
			return 204, nil
		},
		// Forward's rules: addQuery refuses a path that is in HEAD (409), editQuery needs one that is and a basis, bulkDiscard drops drafts at the paths.
		"POST /api/users/current/nqe/changes": func(r *http.Request, body []byte) (int, any) {
			path := r.URL.Query().Get("path")
			_, exists := state[path]
			switch r.URL.Query().Get("action") {
			case "addQuery":
				if exists {
					return 409, map[string]any{"message": "Attempted to add a query at " + path + ", but that path already exists in HEAD.", "reason": "ADD_QUERY_ALREADY_EXISTS"}
				}
				draft[path] = stageSource(body)
				log = append(log, "add "+path)
			case "editQuery":
				if !exists {
					return 409, map[string]any{"message": "no such query in HEAD", "reason": "PATH_MISSING_IN_HEAD"}
				}
				if !strings.Contains(string(body), `"commitId":"c0"}`) {
					return 400, map[string]any{"message": "'basis' is required: " + string(body)}
				}
				if failStage[path] {
					return 500, map[string]any{"message": "boom"}
				}
				draft[path] = stageSource(body)
				log = append(log, "edit "+path)
			case "addDir":
				log = append(log, "adddir "+path)
			case "bulkDiscard":
				for p := range draft {
					if strings.Contains(string(body), `"`+p+`"`) {
						delete(draft, p)
					}
				}
				log = append(log, "bulkDiscard "+string(body))
			default:
				return 400, map[string]any{"message": "unexpected action " + r.URL.Query().Get("action")}
			}
			return 200, map[string]any{}
		},
		"POST /api/nqe/repos/org/commits": func(r *http.Request, _ []byte) (int, any) {
			if r.URL.Query().Get("dryRun") == "true" {
				log = append(log, "dryrun")
				return 200, dry
			}
			for p, s := range draft {
				state[p] = s
			}
			*head = "c2"
			log = append(log, "commit")
			return 200, map[string]any{}
		},
	}, state, &log
}

const multiIn = `{"network_id":"x","changes":[{"path":"/Team/a","source":"` + goodQuery + `"},{"path":"/Team/b","source":"` + goodQueryB + `"}],"message":"update both"`

func TestEditNQEQueriesDryRunWithoutTypecheckWritesNothingAndSaysDependentsWereNotChecked(t *testing.T) {
	head := "c1"
	routes, _, log := multiLibrary(&head, map[string]any{"newErrors": map[string]any{}})
	r, srv := mustRun(t, "edit-nqe-query", routes, strings.Replace(multiIn, `"network_id":"x",`, "", 1)+`}`)
	if r.Status != result.OK || r.Mode != result.ModeDryRun || len(r.Changes) != 2 || writes(srv) != 0 || len(*log) != 0 {
		t.Fatalf("%s %s changes=%d writes=%d %v", r.Status, r.Finding, len(r.Changes), writes(srv), *log)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "NOT typechecked") {
		t.Errorf("limits: %v", r.Limits)
	}
}

func TestEditNQEQueriesTypecheckReportsImporterErrorsAndRestoresTheDrafts(t *testing.T) {
	head := "c1"
	dry := map[string]any{"newErrors": map[string]any{"/Team/importer": []any{map[string]any{"severity": "ERROR", "message": "no such field"}}}}
	routes, state, log := multiLibrary(&head, dry)
	r, _ := mustRun(t, "edit-nqe-query", routes, strings.Replace(multiIn, `"network_id":"x",`, "", 1)+`,"typecheck":true,"apply":true}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "new error") || head != "c1" {
		t.Fatalf("%s %s head=%s", r.Status, r.Finding, head)
	}
	// both paths were staged, typed, and the committed sources restored as drafts; nothing committed
	got := strings.Join(*log, ",")
	if !strings.Contains(got, "dryrun") || strings.Contains(got, "commit") || state["/Team/a"] != "foreach d in network.devices select {a: d.name}" {
		t.Errorf("log %s state %v", got, state)
	}
}

func TestEditNQEQueriesApplyCommitsAllPathsOnceAndReturnsBothCommitIDs(t *testing.T) {
	head := "c1"
	routes, state, log := multiLibrary(&head, map[string]any{"newErrors": map[string]any{}})
	r, _ := mustRun(t, "edit-nqe-query", routes, strings.Replace(multiIn, `"network_id":"x",`, "", 1)+`,"basis_commit_id":"c1","apply":true}`)
	d := r.Evidence[0].Detail
	if r.Status != result.OK || d["previous_commit_id"] != "c1" || d["commit_id"] != "c2" || d["held"] != true || state["/Team/b"] != goodQueryB {
		t.Fatalf("%s %s %v", r.Status, r.Finding, d)
	}
	commits := 0
	for _, l := range *log {
		if l == "commit" {
			commits++
		}
	}
	if commits != 1 {
		t.Errorf("all paths in one commit, got %d", commits)
	}
}

func TestEditNQEQueriesRefusesWhenTheHeadMovedPastTheBasis(t *testing.T) {
	head := "c2"
	routes, _, log := multiLibrary(&head, map[string]any{"newErrors": map[string]any{}})
	r, _ := mustRun(t, "edit-nqe-query", routes, strings.Replace(multiIn, `"network_id":"x",`, "", 1)+`,"basis_commit_id":"c1","apply":true}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "someone committed since") || len(*log) != 0 {
		t.Fatalf("%s %s %v", r.Status, r.Finding, *log)
	}
}

func TestEditNQEQueriesTypecheckEditsExistingPathsAndLeavesNoDraftBehind(t *testing.T) {
	head := "c1"
	routes, _, log := multiLibrary(&head, map[string]any{"newErrors": map[string]any{}})
	r, _ := mustRun(t, "edit-nqe-query", routes, strings.Replace(multiIn, `"network_id":"x",`, "", 1)+`,"typecheck":true}`)
	got := strings.Join(*log, ",")
	if r.Status != result.OK || !strings.Contains(got, "edit /Team/a") || strings.Contains(got, "add ") || len(drafts) != 0 {
		t.Fatalf("%s %s log=%s drafts=%v", r.Status, r.Finding, got, drafts)
	}
}

func TestEditNQEQueriesAFailedStagingDiscardsWhatWasStaged(t *testing.T) {
	head := "c1"
	routes, _, log := multiLibrary(&head, map[string]any{"newErrors": map[string]any{}})
	failStage["/Team/b"] = true
	_, _, err := runSkill(t, "edit-nqe-query", routes, strings.Replace(multiIn, `"network_id":"x",`, "", 1)+`,"typecheck":true}`)
	if err == nil || !strings.Contains(err.Error(), "discarded") || len(drafts) != 0 || strings.Contains(strings.Join(*log, ","), "commit") {
		t.Fatalf("err=%v drafts=%v log=%v", err, drafts, *log)
	}
}

func TestEditNQEQueriesRefusesToStageOverADraftThatIsNotItsOwn(t *testing.T) {
	head := "c1"
	routes, _, log := multiLibrary(&head, map[string]any{"newErrors": map[string]any{}})
	drafts["/Team/a"] = "someone's work in the editor"
	r, srv := mustRun(t, "edit-nqe-query", routes, strings.Replace(multiIn, `"network_id":"x",`, "", 1)+`,"typecheck":true}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "already have uncommitted changes") || writes(srv) != 0 || len(*log) != 0 || drafts["/Team/a"] == "" {
		t.Fatalf("%s %s writes=%d drafts=%v", r.Status, r.Finding, writes(srv), drafts)
	}
}

func TestEditNQEQueriesTypecheckOfANewPathDiscardsItsDraftToo(t *testing.T) {
	head := "c1"
	routes, _, log := multiLibrary(&head, map[string]any{"newErrors": map[string]any{}})
	r, _ := mustRun(t, "edit-nqe-query", routes, `{"changes":[{"path":"/Team/new","source":"`+goodQuery+`"}],"typecheck":true,"message":"x"}`)
	got := strings.Join(*log, ",")
	if r.Status != result.OK || !strings.Contains(got, "add /Team/new") || !strings.Contains(got, "bulkDiscard") || len(drafts) != 0 {
		t.Fatalf("%s %s log=%s drafts=%v", r.Status, r.Finding, got, drafts)
	}
}

func TestFindNQEDescribesALibraryCommitFromTheHeadListingAndHistory(t *testing.T) {
	head := "c1"
	routes, _, _ := multiLibrary(&head, map[string]any{})
	routes["GET /api/nqe/queries/Q_a/history"] = fwdtest.Const(200, map[string]any{"commits": []any{
		map[string]any{"path": "/Team/a", "id": "c0", "author": "Pat Example", "authorEmail": "pat@example.test", "committedAt": "2026-09-30T10:00:00Z", "title": "tidy a"}}})
	r, _ := mustRun(t, "find-nqe-query", routes, `{"network_id":"n1","commit_id":"c0"}`)
	d := r.Evidence[0].Detail
	if r.Status != result.OK || d["queries_last_changed_here"] != 2 || d["author"] != "Pat Example" || d["title"] != "tidy a" {
		t.Fatalf("%s %s %v", r.Status, r.Finding, d)
	}
	r, _ = mustRun(t, "find-nqe-query", routes, `{"network_id":"n1","commit_id":"c9"}`)
	if r.Status != result.Unknown {
		t.Fatalf("a commit no query last changed in is unknown, not empty-ok: %s", r.Status)
	}
}

func TestEditNQEQueryDiscardsOneOfYourOwnDraftsAndNothingElse(t *testing.T) {
	head := "c1"
	routes, _, log := multiLibrary(&head, map[string]any{})
	drafts["/Team/a"] = "work in progress A"
	drafts["/Team/b"] = "work in progress B"
	in := `{"path":"/Team/a","discard_draft":true`
	r, srv := mustRun(t, "edit-nqe-query", routes, in+`}`)
	if r.Status != result.OK || r.Mode != result.ModeDryRun || writes(srv) != 0 || r.Changes[0].Before.(map[string]any)["source"] != "work in progress A" || len(drafts) != 2 {
		t.Fatalf("dry run: %s %s writes=%d drafts=%v", r.Status, r.Finding, writes(srv), drafts)
	}
	r, _ = mustRun(t, "edit-nqe-query", routes, in+`,"apply":true}`)
	if r.Status != result.OK || !r.Changes[0].Applied || len(drafts) != 1 || drafts["/Team/b"] == "" || strings.Contains(strings.Join(*log, ","), "bulkDiscard") {
		t.Fatalf("apply: %s %s drafts=%v log=%v", r.Status, r.Finding, drafts, *log)
	}
	// nothing there: a no-op; combined with a source: refused
	r, _ = mustRun(t, "edit-nqe-query", routes, in+`,"apply":true}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "nothing to discard") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	if _, _, err := runSkill(t, "edit-nqe-query", routes, `{"path":"/Team/a","discard_draft":true,"source":"x"}`); err == nil {
		t.Errorf("discard_draft takes only path")
	}
}

const newTreeIn = `{"changes":[{"path":"/New/Sub/c","source":"` + goodQuery + `"},{"path":"/New/d","source":"` + goodQueryB + `"},{"path":"/Team/a","source":"` + goodQueryB + `"}],"message":"load"`

func TestEditNQEQueriesNewQueriesNeedTheirDirectoriesAndCreateDirectoryMakesThemInOrder(t *testing.T) {
	head := "c1"
	routes, _, log := multiLibrary(&head, map[string]any{"newErrors": map[string]any{}})
	// without the flag: refused before anything is written, naming the missing directories
	r, srv := mustRun(t, "edit-nqe-query", routes, newTreeIn+`}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "/New/") || !strings.Contains(r.Finding, "nothing was changed") || writes(srv) != 0 || len(*log) != 0 {
		t.Fatalf("%s %s writes=%d %v", r.Status, r.Finding, writes(srv), *log)
	}
	// with it, the dry run plans the directories too and still writes nothing
	r, srv = mustRun(t, "edit-nqe-query", routes, newTreeIn+`,"create_directory":true}`)
	dirs := 0
	for _, c := range r.Changes {
		if c.Action == "create_directory" {
			dirs++
		}
	}
	if r.Status != result.OK || dirs != 2 || writes(srv) != 0 {
		t.Fatalf("dry run: %s %s dirs=%d writes=%d", r.Status, r.Finding, dirs, writes(srv))
	}
	// applying: parents first (/New/ before /New/Sub/), directories before the queries, ONE commit
	r, _ = mustRun(t, "edit-nqe-query", routes, newTreeIn+`,"create_directory":true,"apply":true}`)
	if r.Status != result.OK {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	order := strings.Join(*log, ",")
	i1, i2, iq := strings.Index(order, "adddir /New/,"), strings.Index(order, "adddir /New/Sub/"), strings.Index(order, "add /New/Sub/c")
	if i1 < 0 || i2 < i1 || iq < i2 || strings.Count(order, "commit") != 1 {
		t.Errorf("want /New/ then /New/Sub/ then the queries, one commit: %s", order)
	}
}

func TestEditNQEQueriesACreateDirectoryRunThatFailsWhileStagingLeavesNothingBehind(t *testing.T) {
	head := "c1"
	routes, _, log := multiLibrary(&head, map[string]any{"newErrors": map[string]any{}})
	failStage["/Team/a"] = true // the edit of an existing query fails after the directories were staged
	_, _, err := runSkill(t, "edit-nqe-query", routes, newTreeIn+`,"create_directory":true,"apply":true}`)
	if err == nil {
		t.Fatal("a failed staging is an error")
	}
	order := strings.Join(*log, ",")
	if strings.Contains(order, "commit") || !strings.Contains(order, "bulkDiscard") {
		t.Errorf("nothing may be committed and the drafts must be discarded: %s", order)
	}
	sub, top := strings.Index(order, `bulkDiscard {"paths":["/New/Sub/"]`), strings.Index(order, `bulkDiscard {"paths":["/New/"]`)
	if sub < 0 || top < 0 || sub > top {
		t.Errorf("both directory drafts must be discarded again: %s", order)
	}
}
