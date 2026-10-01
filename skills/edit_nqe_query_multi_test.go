package skills_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

const goodQueryB = "foreach d in network.devices select {n: d.name, v: d.platform.vendor}"

// multiLibrary holds /Team/a and /Team/b; staging records drafts, a commit applies them to the next head (c2) and Forward's typecheck says errs.
func multiLibrary(head *string, dry map[string]any) (map[string]fwdtest.Handler, map[string]string, *[]string) {
	state := map[string]string{"/Team/a": "foreach d in network.devices select {a: d.name}", "/Team/b": "foreach d in network.devices select {b: d.name}"}
	draft := map[string]string{}
	var log []string
	src := func(r *http.Request, _ []byte) (int, any) {
		return 200, map[string]any{"sourceCode": state[r.URL.Query().Get("path")], "queryId": "Q_x"}
	}
	return map[string]fwdtest.Handler{
		"GET /api/nqe/repos/org/commits/head": func(*http.Request, []byte) (int, any) { return 200, *head },
		"GET /api/nqe/repos/org/commits/head/queries": fwdtest.Const(200, map[string]any{"queries": []any{
			map[string]any{"path": "/Team/a", "lastCommitId": "c1", "queryId": "Q_a"}, map[string]any{"path": "/Team/b", "lastCommitId": "c1", "queryId": "Q_b"}}}),
		"GET /api/nqe/repos/org/commits/c1/queries": src,
		"GET /api/nqe/repos/org/commits/c2/queries": src,
		"POST /api/users/current/nqe/changes": func(r *http.Request, body []byte) (int, any) {
			draft[r.URL.Query().Get("path")] = strings.TrimSuffix(strings.TrimPrefix(string(body), `{"sourceCode":"`), `"}`)
			log = append(log, "stage "+r.URL.Query().Get("path"))
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
