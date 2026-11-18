package fwd_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
)

// A draft is reported once per requested path it touches: an edit that moves a query touches the path it came from (its basis) and the path it goes to;
// a path no draft touches is absent; the order is by path then type and does not depend on the listing order.
func TestDraftConflictsReportsEveryPathADraftTouches(t *testing.T) {
	drafts := []any{ // deliberately NOT in path order
		map[string]any{"type": "QUERY_ADD", "path": "/Z/new, with comma"},
		map[string]any{"type": "QUERY_EDIT", "basis": map[string]any{"queryId": "Q_2", "commitId": "c1", "path": "/Y/other"}},
		map[string]any{"type": "QUERY_EDIT", "directory": "/X/", "name": "q", "basis": map[string]any{"queryId": "Q_1", "commitId": "c1", "path": "/Y/q"}}, // moved Y/q to X/q
	}
	s, _ := fwdtest.New(t, map[string]fwdtest.Handler{"GET /api/users/current/nqe/changes": func(*http.Request, []byte) (int, any) {
		return 200, map[string]any{"changes": drafts}
	}})
	got, err := s.DraftConflicts(context.Background(), []string{"/X/q", "/Y/q", "/Z/new, with comma", "/Untouched"})
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ path, typ string }{{"/X/q", "QUERY_EDIT"}, {"/Y/q", "QUERY_EDIT"}, {"/Z/new, with comma", "QUERY_ADD"}}
	if len(got) != len(want) {
		t.Fatalf("every touched path once, an untouched one absent: %+v", got)
	}
	for i, w := range want {
		if got[i].Path != w.path || got[i].Type != w.typ {
			t.Errorf("entry %d: %+v, want %v", i, got[i], w)
		}
	}
	// only the basis path requested: the moved draft is reported once, at the basis path
	only, _ := s.DraftConflicts(context.Background(), []string{"/Y/q"})
	if len(only) != 1 || only[0].Path != "/Y/q" {
		t.Errorf("one requested path, one entry: %+v", only)
	}
	// the single-path wrapper keeps working
	paths, _ := s.DraftsAt(context.Background(), []string{"/X/q", "/Y/q"})
	if len(paths) != 2 {
		t.Errorf("DraftsAt is the paths of DraftConflicts: %v", paths)
	}
}
