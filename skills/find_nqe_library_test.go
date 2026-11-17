package skills_test

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
	"github.com/forwardnetworks/fwdctl/skills"
)

// A synthetic library: queries two levels below /Team/Cat/, with intents that carry categories at the end (a stand-in for tags) and some with none.
func libQueries() []any {
	q := func(id, path, intent, repo string) map[string]any {
		return map[string]any{"queryId": id, "path": path, "intent": intent, "repository": repo}
	}
	return []any{
		q("Q_1", "/Team/Cat/a", "list devices [tags: inventory]", "ORG"),
		q("Q_2", "/Team/Cat/b", "", "ORG"),
		q("Q_3", "/Team/Cat/Sub/c", "check bgp [tags: health-check, bgp]", "ORG"),
		q("Q_4", "/Team/Cat/Sub/Deeper/d", "   ", "ORG"),
		q("Q_5", "/Team/Other/e", "list acls [tags: health-check]", "ORG"),
		q("FQ_6", "/Team/Cat/builtin", "", "FWD"),
	}
}

func libRoutes() map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{"GET /api/nqe/queries": fwdtest.Const(200, libQueries())}
}

func TestFindNQEQueryRecursiveListsEveryDepthWithIntent(t *testing.T) {
	r, _ := mustRun(t, "find-nqe-query", libRoutes(), `{"network_id":"n1","list":true,"recursive":true,"directory":"/Team/Cat/"}`)
	j := jsonOf(r)
	for _, want := range []string{"/Team/Cat/a", "/Team/Cat/b", "/Team/Cat/Sub/c", "/Team/Cat/Sub/Deeper/d", "/Team/Cat/builtin", "[tags: health-check, bgp]"} {
		if !strings.Contains(j, want) {
			t.Errorf("a recursive listing holds %s: %s", want, j)
		}
	}
	if strings.Contains(j, "/Team/Other/e") || !strings.Contains(r.Finding, "5 queries under /Team/Cat/") {
		t.Errorf("only the directory's queries, counted: %s", r.Finding)
	}
	// one level (the existing listing) still shows only the direct queries
	r, _ = mustRun(t, "find-nqe-query", libRoutes(), `{"network_id":"n1","list":true,"directory":"/Team/Cat/"}`)
	if strings.Contains(jsonOf(r), "/Team/Cat/Sub/Deeper/d") {
		t.Errorf("the one-level listing is unchanged")
	}
}

func TestFindNQEQueryIntentFilters(t *testing.T) {
	for in, want := range map[string]struct {
		n    int
		must string
	}{
		`{"network_id":"n1","intent":"^\\s*$"}`:                            {3, "/Team/Cat/Sub/Deeper/d"}, // no intent: empty, blank and the built-in
		`{"network_id":"n1","intent":"tags:.*health-check"}`:               {2, "/Team/Cat/Sub/c"},        // a category at the end of the intent
		`{"network_id":"n1","list":true,"intent":"(?i)INVENTORY"}`:         {1, "/Team/Cat/a"},            // case-insensitive when asked
		`{"network_id":"n1","recursive":true,"intent":"health-check\\]$"}`: {1, "/Team/Other/e"},          // anchored at the end: only the intent that ENDS with the tag
	} {
		r, _ := mustRun(t, "find-nqe-query", libRoutes(), in)
		if !strings.Contains(r.Finding, fmt.Sprintf("%d quer", want.n)) || !strings.Contains(jsonOf(r), want.must) {
			t.Errorf("%s: want %d incl %s: %s", in, want.n, want.must, r.Finding)
		}
	}
	// with a question the intent narrows the matches; an invalid regex is refused before any request
	r, _ := mustRun(t, "find-nqe-query", libRoutes(), `{"network_id":"n1","question":"list devices acls bgp","intent":"health-check"}`)
	if strings.Contains(jsonOf(r), "Q_1") || !strings.Contains(jsonOf(r), "Q_5") {
		t.Errorf("a question search honours the intent filter: %s", jsonOf(r))
	}
	_, srv, err := runSkill(t, "find-nqe-query", libRoutes(), `{"network_id":"n1","intent":"(unclosed"}`)
	if !errors.Is(err, skills.ErrInvalidInput) || len(srv.Calls()) != 0 {
		t.Errorf("a bad regex is refused before any call: %v %d", err, len(srv.Calls()))
	}
}

func srcRoutes(sources map[string]string, status map[string]int) map[string]fwdtest.Handler {
	r := libRoutes()
	r["GET /api/nqe/repos/org/commits/head"] = fwdtest.Const(200, "c1")
	for id, src := range sources {
		if st := status[id]; st != 0 {
			r["GET /api/nqe/queries/"+id+"/source-code"] = fwdtest.Const(st, map[string]any{"message": "no"})
			continue
		}
		r["GET /api/nqe/queries/"+id+"/source-code"] = fwdtest.Const(200, map[string]any{"sourceCode": src})
	}
	return r
}

const negSrc = "foreach d in network.devices\nforeach r in d.securityPolicy.rules\nwhere r.sourceAddresses.negated\nselect {a: r.sourceAddresses.negated, b: 1}"

func TestFindNQEQuerySourceSearchListsMatchesWithLines(t *testing.T) {
	routes := srcRoutes(map[string]string{"Q_1": "select 1", "Q_2": negSrc, "Q_3": "select 2", "Q_4": "x.negatedNot", "Q_5": "select 3"}, nil)
	r, srv := mustRun(t, "find-nqe-query", routes, `{"network_id":"n1","source_matches":"\\.negated\\b"}`)
	j := jsonOf(r)
	if r.Status != result.OK || !strings.Contains(r.Finding, "1 of 5 query source(s) under / match") || !strings.Contains(j, "/Team/Cat/b") || !strings.Contains(j, `"matching_lines":2`) || !strings.Contains(j, "where r.sourceAddresses.negated") {
		t.Fatalf("source search: %s %s", r.Status, r.Finding)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "1 Forward built-in query") {
		t.Errorf("built-ins are said to be unread: %v", r.Limits)
	}
	for _, c := range srv.Calls() {
		if c.Method != "GET" {
			t.Errorf("a source search only reads: %s %s", c.Method, c.Path)
		}
	}
	// nothing matches and every source was read: a plain none (ok), with the count
	r, _ = mustRun(t, "find-nqe-query", srcRoutes(map[string]string{"Q_1": "a", "Q_2": "b", "Q_3": "c", "Q_4": "d", "Q_5": "e"}, nil), `{"network_id":"n1","source_matches":"zzz"}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "None of the 5 query source(s)") {
		t.Errorf("a complete search with no match: %s %s", r.Status, r.Finding)
	}
	// intent and directory narrow which sources are read
	r, _ = mustRun(t, "find-nqe-query", routes, `{"network_id":"n1","source_matches":"negated","directory":"/Team/Other/"}`)
	if !strings.Contains(r.Finding, "None of the 1 query source(s) under /Team/Other/") {
		t.Errorf("directory narrows the search: %s", r.Finding)
	}
}

func TestFindNQEQuerySourceSearchIsPartialOrRefusedNeverAQuietNone(t *testing.T) {
	src := map[string]string{"Q_1": "a", "Q_2": "b", "Q_3": "c", "Q_4": "d", "Q_5": "e"}
	// a source that cannot be read: no match among the rest is unknown, not none
	r, _ := mustRun(t, "find-nqe-query", srcRoutes(src, map[string]int{"Q_3": 500}), `{"network_id":"n1","source_matches":"zzz"}`)
	if r.Status != result.Unknown || !strings.Contains(strings.Join(r.Limits, " "), "1 query source(s) could not be read") {
		t.Errorf("a failed read makes a no-match unknown: %s %s", r.Status, r.Finding)
	}
	// with a match elsewhere the answer is ok and says one was not searched
	src["Q_2"] = negSrc
	r, _ = mustRun(t, "find-nqe-query", srcRoutes(src, map[string]int{"Q_3": 500}), `{"network_id":"n1","source_matches":"negated"}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "1 qualifying query not searched") {
		t.Errorf("a partial hit says what was not searched: %s %s", r.Status, r.Finding)
	}
	// a refusal stops the search: unknown
	r, _ = mustRun(t, "find-nqe-query", srcRoutes(src, map[string]int{"Q_1": 403}), `{"network_id":"n1","source_matches":"negated"}`)
	if r.Status != result.Unknown {
		t.Errorf("a refused read is unknown: %s %s", r.Status, r.Finding)
	}
}

func TestFindNQEQuerySourceSearchReadsAtMost300AndSaysSo(t *testing.T) {
	var lib []any
	routes := map[string]fwdtest.Handler{"GET /api/nqe/repos/org/commits/head": fwdtest.Const(200, "c1")}
	for i := 0; i < 305; i++ {
		id := fmt.Sprintf("Q_%03d", i)
		lib = append(lib, map[string]any{"queryId": id, "path": fmt.Sprintf("/Many/q%03d", i), "intent": "", "repository": "ORG"})
		routes["GET /api/nqe/queries/"+id+"/source-code"] = fwdtest.Const(200, map[string]any{"sourceCode": "select 1"})
	}
	routes["GET /api/nqe/queries"] = fwdtest.Const(200, lib)
	r, srv := mustRun(t, "find-nqe-query", routes, `{"network_id":"n1","source_matches":"zzz"}`)
	reads := 0
	for _, c := range srv.Calls() {
		if strings.HasSuffix(c.Path, "/source-code") {
			reads++
		}
	}
	if reads != 300 || r.Status != result.Unknown || !strings.Contains(r.Finding, "5 of 305") {
		t.Errorf("at most 300 reads, a partial none is unknown: %d reads, %s %s", reads, r.Status, r.Finding)
	}
}

func TestFindNQEQueryRefusesBadCombinationsBeforeAnyRequest(t *testing.T) {
	for _, in := range []string{
		`{"network_id":"n1","source_matches":"x","question":"devices"}`,
		`{"network_id":"n1","source_matches":"x","list":true}`,
		`{"network_id":"n1","source_matches":"x","recursive":true}`,
		`{"network_id":"n1","recursive":true,"question":"devices"}`,
		`{"network_id":"n1","source_matches":"(bad"}`,
		`{"network_id":"n1","recursive":true,"directory":"Team/"}`,
	} {
		_, srv, err := runSkill(t, "find-nqe-query", libRoutes(), in)
		if !errors.Is(err, skills.ErrInvalidInput) || len(srv.Calls()) != 0 {
			t.Errorf("%s: refused before any call (err %v, calls %d)", in, err, len(srv.Calls()))
		}
	}
	_ = http.StatusOK
}

// A refusal (here a 403 on the first source) stops the search: the remaining sources are not requested (at most the few already in flight), and the
// answer is unknown. A long line with multi-byte characters is cut at a character, never inside one.
func TestFindNQEQuerySourceSearchStopsOnARefusal(t *testing.T) {
	var lib []any
	routes := map[string]fwdtest.Handler{"GET /api/nqe/repos/org/commits/head": fwdtest.Const(200, "c1")}
	// the FIRST source request to arrive is the one refused (whichever goroutine runs first), and every other waits for that 403 to have been sent, so
	// the order is deterministic and the test cannot hang on goroutine scheduling
	var first atomic.Bool
	refused := make(chan struct{})
	handler := func(*http.Request, []byte) (int, any) {
		if first.CompareAndSwap(false, true) {
			defer close(refused)
			return 403, map[string]any{"message": "forbidden"}
		}
		<-refused
		return 200, map[string]any{"sourceCode": "select 1"}
	}
	for i := 0; i < 120; i++ {
		id := fmt.Sprintf("Q_%03d", i)
		lib = append(lib, map[string]any{"queryId": id, "path": fmt.Sprintf("/Many/q%03d", i), "intent": "", "repository": "ORG"})
		routes["GET /api/nqe/queries/"+id+"/source-code"] = handler
	}
	routes["GET /api/nqe/queries"] = fwdtest.Const(200, lib)
	r, srv := mustRun(t, "find-nqe-query", routes, `{"network_id":"n1","source_matches":"zzz"}`)
	reads := 0
	for _, c := range srv.Calls() {
		if strings.HasSuffix(c.Path, "/source-code") {
			reads++
		}
	}
	if r.Status != result.Unknown || reads > 20 {
		t.Errorf("a refusal stops the search (reads %d of 120), unknown: %s %s", reads, r.Status, r.Finding)
	}
	long := strings.Repeat("é", 200) + " negated"
	r, _ = mustRun(t, "find-nqe-query", srcRoutes(map[string]string{"Q_1": long, "Q_2": "a", "Q_3": "b", "Q_4": "c", "Q_5": "d"}, nil), `{"network_id":"n1","source_matches":"negated"}`)
	if j := jsonOf(r); !strings.Contains(j, strings.Repeat("é", 160)+"…") || strings.Contains(j, "\ufffd") {
		t.Errorf("a long line is cut at 160 characters, not inside one")
	}
}

func TestFindNQEQueryRefusesIgnoredCombinations(t *testing.T) {
	for _, in := range []string{
		`{"network_id":"n1","list":true,"recursive":true,"question":"devices"}`,
		`{"network_id":"n1","recursive":true,"commit_id":"abc"}`,
		`{"network_id":"n1","intent":"x","commit_id":"abc"}`,
		`{"network_id":"n1","question":"devices","intent":"(bad"}`,
		`{"network_id":"n1","list":true,"question":"devices"}`,
	} {
		_, srv, err := runSkill(t, "find-nqe-query", libRoutes(), in)
		if !errors.Is(err, skills.ErrInvalidInput) || len(srv.Calls()) != 0 {
			t.Errorf("%s: refused before any call (%v, %d calls)", in, err, len(srv.Calls()))
		}
	}
}

// A library bigger than one page: the listing shows 200 by default and says where to continue, offset and limit page it, repository keeps only the
// organization's own (ORG) or the built-in (FWD) queries, and an offset past the end is unknown, not an empty page.
func bigLib(org, fwd int) []any {
	var lib []any
	for i := 0; i < org; i++ {
		lib = append(lib, map[string]any{"queryId": fmt.Sprintf("Q_%03d", i), "path": fmt.Sprintf("/Lib/o%03d", i), "intent": "", "repository": "ORG"})
	}
	for i := 0; i < fwd; i++ {
		lib = append(lib, map[string]any{"queryId": fmt.Sprintf("FQ_%03d", i), "path": fmt.Sprintf("/Lib/f%03d", i), "intent": "x", "repository": "FWD"})
	}
	return lib
}

func TestFindNQEQueryListingPagesAndFiltersByRepository(t *testing.T) {
	routes := map[string]fwdtest.Handler{"GET /api/nqe/queries": fwdtest.Const(200, bigLib(120, 130))}
	shown := func(r result.Result) int {
		q, _ := r.Evidence[0].Detail["queries"].([]map[string]any)
		return len(q)
	}
	r, _ := mustRun(t, "find-nqe-query", routes, `{"network_id":"n1","recursive":true}`)
	if shown(r) != 200 || r.Evidence[0].Detail["next_offset"] != 200 || !strings.Contains(strings.Join(r.Limits, " "), "ask again with offset 200") {
		t.Fatalf("250 queries: 200 shown, continue at 200: %d %v", shown(r), r.Evidence[0].Detail["next_offset"])
	}
	r, _ = mustRun(t, "find-nqe-query", routes, `{"network_id":"n1","recursive":true,"offset":200}`)
	if shown(r) != 50 || r.Evidence[0].Detail["next_offset"] != nil {
		t.Errorf("the second page holds the last 50 and no next: %d %v", shown(r), r.Evidence[0].Detail["next_offset"])
	}
	if r, _ = mustRun(t, "find-nqe-query", routes, `{"network_id":"n1","recursive":true,"limit":1000}`); shown(r) != 250 {
		t.Errorf("limit 1000 shows all 250: %d", shown(r))
	}
	r, _ = mustRun(t, "find-nqe-query", routes, `{"network_id":"n1","recursive":true,"repository":"org","limit":1000}`)
	if shown(r) != 120 || !strings.Contains(r.Finding, "120 queries") || strings.Contains(jsonOf(r), "FQ_") {
		t.Errorf("repository ORG keeps only the organization's 120: %d %s", shown(r), r.Finding)
	}
	if r, _ = mustRun(t, "find-nqe-query", routes, `{"network_id":"n1","recursive":true,"repository":"FWD","limit":1000}`); shown(r) != 130 {
		t.Errorf("repository FWD keeps the 130 built-in: %d", shown(r))
	}
	if r, _ = mustRun(t, "find-nqe-query", routes, `{"network_id":"n1","recursive":true,"offset":999}`); r.Status != result.Unknown || !strings.Contains(r.Finding, "past the end") {
		t.Errorf("an offset past the end is unknown: %s %s", r.Status, r.Finding)
	}
	// offset and limit together: rows 101 to 150
	r, _ = mustRun(t, "find-nqe-query", routes, `{"network_id":"n1","recursive":true,"offset":100,"limit":50}`)
	if q, _ := r.Evidence[0].Detail["queries"].([]map[string]any); len(q) != 50 || q[0]["path"] != "/Lib/f100" || r.Evidence[0].Detail["next_offset"] != 150 {
		t.Errorf("offset 100 limit 50 is rows 101-150 (path order: /Lib/f000-f129 then /Lib/o000-o119, so row 101 is f100), next 150: %v", r.Evidence[0].Detail["next_offset"])
	}
	// repository also narrows a question search
	qr, _ := mustRun(t, "find-nqe-query", map[string]fwdtest.Handler{"GET /api/nqe/queries": fwdtest.Const(200, []any{
		map[string]any{"queryId": "Q_1", "path": "/Mine/devices report", "intent": "list devices", "repository": "ORG"},
		map[string]any{"queryId": "FQ_1", "path": "/Fwd/devices report", "intent": "list devices", "repository": "FWD"}})},
		`{"network_id":"n1","question":"devices report","repository":"ORG"}`)
	if j := jsonOf(qr); !strings.Contains(j, "Q_1") || strings.Contains(j, "FQ_1") {
		t.Errorf("repository ORG narrows a question search: %s", j)
	}
	for _, in := range []string{
		`{"network_id":"n1","list":true,"repository":"ORG"}`,
		`{"network_id":"n1","list":true,"offset":10}`,
		`{"network_id":"n1","commit_id":"abc","offset":10}`,
		`{"network_id":"n1","query_id":"Q_1","repository":"ORG"}`,
		`{"network_id":"n1","query_id":"Q_1","offset":5,"recursive":true}`, // reading a query ignores both, even with recursive
		`{"network_id":"n1","path":"/Team/q","repository":"FWD","intent":"x"}`,
		`{"network_id":"n1","recursive":true,"repository":"MINE"}`,
		`{"network_id":"n1","recursive":true,"offset":-1}`,
		`{"network_id":"n1","question":"devices","offset":5}`,
		`{"network_id":"n1","source_matches":"x","repository":"FWD"}`,
	} {
		if _, srv, err := runSkill(t, "find-nqe-query", routes, in); !errors.Is(err, skills.ErrInvalidInput) || len(srv.Calls()) != 0 {
			t.Errorf("%s: refused before any call (%v, %d calls)", in, err, len(srv.Calls()))
		}
	}
}

// More than 300 organization sources: the first call reads 300 and says where the next window starts, offset moves the window over the qualifying
// queries in path order, and a window that finds nothing is unknown (the rest was not searched), never "none". limit raises how many matches are shown.
func TestFindNQEQuerySourceSearchWindowsOverALargeLibrary(t *testing.T) {
	routes := map[string]fwdtest.Handler{"GET /api/nqe/repos/org/commits/head": fwdtest.Const(200, "c1"), "GET /api/nqe/queries": fwdtest.Const(200, bigLib(305, 40))}
	for i := 0; i < 305; i++ {
		src := "select 1"
		if i >= 300 {
			src = "select needle" // only the last five carry the pattern
		}
		routes[fmt.Sprintf("GET /api/nqe/queries/Q_%03d/source-code", i)] = fwdtest.Const(200, map[string]any{"sourceCode": src})
	}
	r, _ := mustRun(t, "find-nqe-query", routes, `{"network_id":"n1","source_matches":"needle"}`)
	if r.Status != result.Unknown || r.Evidence[0].Detail["next_offset"] != 300 || !strings.Contains(strings.Join(r.Limits, " "), "ask again with offset 300") {
		t.Fatalf("the first window finds nothing and says where to continue: %s %s %v", r.Status, r.Finding, r.Evidence[0].Detail["next_offset"])
	}
	r, _ = mustRun(t, "find-nqe-query", routes, `{"network_id":"n1","source_matches":"needle","offset":300}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "5 of 5 query source(s)") || r.Evidence[0].Detail["next_offset"] != nil {
		t.Errorf("the last window finds the five and ends: %s %s", r.Status, r.Finding)
	}
	// the LAST window finds nothing for a pattern none of the 305 carries: the 300 before it were not searched in this call, so it is unknown, never "none"
	if r, _ = mustRun(t, "find-nqe-query", routes, `{"network_id":"n1","source_matches":"absent-everywhere","offset":300}`); r.Status != result.Unknown || strings.Contains(r.Finding, "None of") {
		t.Errorf("a later window with no match is unknown: %s %s", r.Status, r.Finding)
	}
	if r, _ = mustRun(t, "find-nqe-query", routes, `{"network_id":"n1","source_matches":"needle","offset":999}`); r.Status != result.Unknown || !strings.Contains(r.Finding, "past the end") {
		t.Errorf("an offset past the end is unknown: %s %s", r.Status, r.Finding)
	}
	r, _ = mustRun(t, "find-nqe-query", routes, `{"network_id":"n1","source_matches":"needle","offset":300,"limit":2}`)
	if q, _ := r.Evidence[0].Detail["queries"].([]map[string]any); len(q) != 2 || len(r.Omitted) == 0 {
		t.Errorf("limit 2 shows two matches and says the rest were omitted: %v", r.Omitted)
	}
}
