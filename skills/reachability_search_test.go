package skills_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
	"github.com/forwardnetworks/fwdctl/skills"
)

// The search form (through, leaving) sends one Search-page query: the entry place with the flow's headers, each through hop with its transit,
// and the leaving place with the packet as it leaves; it lists the distinct device paths by outcome with the total, and says it is preview.
func TestReachabilitySearchSendsTheQueryAndListsPathsByOutcome(t *testing.T) {
	var body map[string]any
	facets := map[string]any{"totalResults": map[string]any{"value": 12, "type": "EXACT"}, "facets": map[string]any{"pathGroups": []any{
		map[string]any{"traceType": "VALID", "paths": []any{map[string]any{"path": []any{"b1", "fw1", "e1"}}, map[string]any{"path": []any{"b1", "fw2", "e1"}}}},
		map[string]any{"traceType": "DROPPED", "paths": []any{map[string]any{"path": []any{"b1", "fw1"}}}}}}}
	routes := map[string]fwdtest.Handler{snapsPath: ready("s1"),
		"POST /api/networks/n1/trace-facets": func(_ *http.Request, b []byte) (int, any) { _ = json.Unmarshal(b, &body); return 200, facets }}
	r, srv := mustRun(t, "investigate-reachability", routes, `{"network_id":"n1","from":"b1","dst_ip":"203.0.113.0/24","protocol":"tcp","dst_port":"443",`+
		`"through":["fw1","egress:e1 eth9"],"leaving":{"at":"e1 eth9","dst_ip":"198.51.100.7"}}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "3 distinct device path(s) match: 1 DROPPED, 2 VALID (12 flows)") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	q := fmt.Sprint(body["filters"])
	for _, want := range []string{"DeviceFilter", "b1", "ipv4_dst:[203.0.113.0/24]", "ip_proto:[6]", "tp_dst:[443]", "transitType:through", "transitType:egress", "InterfaceFilter", "e1 eth9", "ipv4_dst:[198.51.100.7]"} {
		if !strings.Contains(q, want) {
			t.Errorf("the query carries %q: %s", want, q)
		}
	}
	if paths, _ := r.Evidence[0].Detail["paths"].([]map[string]any); len(paths) != 3 {
		t.Errorf("each distinct device path is listed: %v", r.Evidence[0].Detail)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "PREVIEW") {
		t.Errorf("the search form says it is preview: %v", r.Limits)
	}
	for _, c := range srv.Calls() {
		if c.Method != "GET" && c.Path != "/api/networks/n1/trace-facets" {
			t.Errorf("a search only reads (the Search-page query is a POST that changes nothing): %s %s", c.Method, c.Path)
		}
	}
}

// Bad input is refused before any request: a leaving side with no place, a header filter with no entry place, a host as a through hop, and the
// forms the search does not combine with.
func TestReachabilitySearchRefusesBadQueriesBeforeAnyRequest(t *testing.T) {
	for _, in := range []string{
		`{"network_id":"n1","from":"b1","leaving":{"dst_port":"443"}}`,
		`{"network_id":"n1","dst_ip":"203.0.113.9","through":["fw1"]}`,
		`{"network_id":"n1","from":"b1","through":["host:web"]}`,
		`{"network_id":"n1","from":"b1","through":["a b c"]}`,
		`{"network_id":"n1","from":"b1","through":["fw1"],"return_path":true}`,
		`{"network_id":"n1","from":"b1","through":["fw1"],"explain":"routes"}`,
		`{"network_id":"n1","through":["fw1"],"matrix":{"endpoints":[{"label":"a","ip":"192.0.2.1"},{"label":"b","ip":"192.0.2.2"}]}}`,
		`{"network_id":"n1","from":"b1","through":["1","2","3","4","5","6","7","8","9"]}`,
		`{"network_id":"n1","from":"b1","leaving":{"at":"198.51.100.0/24"}}`,
		`{"network_id":"n1","from":"b1","through":["192.0.2.1"]}`,
		`{"network_id":"n1","from":"b1","dst_ip":"web.example.invalid","through":["fw1"]}`,
	} {
		_, srv, err := runSkill(t, "investigate-reachability", map[string]fwdtest.Handler{snapsPath: ready("s1")}, in)
		if !errors.Is(err, skills.ErrInvalidInput) || len(srv.Calls()) != 0 {
			t.Errorf("%s: refused before any call (err %v, calls %d)", in, err, len(srv.Calls()))
		}
	}
}

// An ambiguous address answers with the places, not paths: unknown, with the candidates. No match is said plainly with how to check the names;
// no match after Forward stopped early is unknown, never "none".
func TestReachabilitySearchAmbiguousEmptyAndCutShort(t *testing.T) {
	answer := map[string]any{}
	routes := map[string]fwdtest.Handler{snapsPath: ready("s1"), "POST /api/networks/n1/trace-facets": func(*http.Request, []byte) (int, any) { return 200, answer }}
	in := `{"network_id":"n1","src_ip":"192.0.2.10","through":["fw1"]}`
	answer = map[string]any{"possibleIpLocations": map[string]any{"from": map[string]any{"type": "HOST", "locations": []any{map[string]any{"device": "a1", "subnet": "192.0.2.0/24"}, map[string]any{"device": "a2", "subnet": "192.0.2.0/24"}}}}}
	r, _ := mustRun(t, "investigate-reachability", routes, in)
	if r.Status != result.Unknown || r.Evidence[0].Detail["possible_locations"] == nil {
		t.Errorf("ambiguous: %s %s %v", r.Status, r.Finding, r.Evidence)
	}
	answer = map[string]any{"totalResults": map[string]any{"value": 0, "type": "EXACT"}, "facets": map[string]any{"pathGroups": []any{}}}
	r, _ = mustRun(t, "investigate-reachability", routes, in)
	if r.Status != result.OK || !strings.HasPrefix(r.Finding, "No path matches") || !strings.Contains(strings.Join(r.Limits, " "), "inspect-inventory") {
		t.Errorf("no match: %s %s %v", r.Status, r.Finding, r.Limits)
	}
	answer = map[string]any{"timedOut": true, "facets": map[string]any{"pathGroups": []any{}}}
	r, _ = mustRun(t, "investigate-reachability", routes, in)
	if r.Status != result.Unknown {
		t.Errorf("no match after a timeout is unknown: %s %s", r.Status, r.Finding)
	}
}

// Paths found while Forward reports only a lower bound (or no total): the paths are listed, and the limits and finding say the list may not be
// complete. No paths and no total is unknown, never "no path matches". The source address rides as a header when from names the place.
func TestReachabilitySearchSaysWhenTheListMayBeIncomplete(t *testing.T) {
	var body map[string]any
	answer := map[string]any{"totalResults": map[string]any{"value": 5000, "type": "LOWER_BOUND"}, "facets": map[string]any{"pathGroups": []any{
		map[string]any{"traceType": "VALID", "paths": []any{map[string]any{"path": []any{"b1", "fw1"}}}}}}}
	routes := map[string]fwdtest.Handler{snapsPath: ready("s1"), "POST /api/networks/n1/trace-facets": func(_ *http.Request, b []byte) (int, any) { _ = json.Unmarshal(b, &body); return 200, answer }}
	r, _ := mustRun(t, "investigate-reachability", routes, `{"network_id":"n1","from":"b1","src_ip":"192.0.2.10","through":["fw1"]}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "at least 5000 flows") || !strings.Contains(strings.Join(r.Limits, " "), "may not be all the paths") {
		t.Errorf("a lower bound is said: %s %s %v", r.Status, r.Finding, r.Limits)
	}
	if q := fmt.Sprint(body["filters"]); !strings.Contains(q, "ipv4_src:[192.0.2.10]") || !strings.Contains(q, "DeviceFilter") {
		t.Errorf("from is the place and src_ip a header: %s", q)
	}
	answer = map[string]any{"facets": map[string]any{"pathGroups": []any{}}}
	if r, _ = mustRun(t, "investigate-reachability", routes, `{"network_id":"n1","from":"b1","through":["fw1"]}`); r.Status != result.Unknown {
		t.Errorf("no paths and no total is unknown: %s %s", r.Status, r.Finding)
	}
}

// explain trace on the search form traces each path found with the SAME query (through hops and leaving side included), so the trace is of
// the path as searched; a trace that fails is marked on its row and the search answer stands.
func TestReachabilitySearchExplainTraceUsesTheSameQuery(t *testing.T) {
	facets := map[string]any{"totalResults": map[string]any{"value": 2, "type": "EXACT"}, "facets": map[string]any{"pathGroups": []any{
		map[string]any{"traceType": "VALID", "paths": []any{map[string]any{"path": []any{"b1", "fw1", "e1"}, "behaviorSample": map[string]any{"k": 1}},
			map[string]any{"path": []any{"b1", "fw2", "e1"}, "behaviorSample": map[string]any{"k": 2}}}}}}}
	var traced []string
	routes := map[string]fwdtest.Handler{snapsPath: ready("s1"),
		"POST /api/networks/n1/trace-facets": fwdtest.Const(200, facets),
		"POST /api/networks/n1/trace": func(_ *http.Request, b []byte) (int, any) {
			traced = append(traced, string(b))
			if strings.Contains(string(b), "fw2") {
				return 500, map[string]any{"message": "boom"}
			}
			return 200, map[string]any{"traceType": "VALID", "pathDescriptor": "b1 > fw1 > e1", "deviceHops": []any{map[string]any{"device": "b1"}}}
		}}
	r, _ := mustRun(t, "investigate-reachability", routes, `{"network_id":"n1","from":"b1","through":["fw1"],"explain":"trace"}`)
	if r.Status != result.OK || len(traced) != 2 {
		t.Fatalf("%s %s, traced %d", r.Status, r.Finding, len(traced))
	}
	for _, b := range traced {
		if !strings.Contains(b, `"transitType":"through"`) || !strings.Contains(b, "fw1") {
			t.Errorf("each trace carries the search's chain: %s", b)
		}
	}
	var tr map[string]any
	for _, e := range r.Evidence {
		if e.Source.Operation == "trace" {
			tr = e.Detail
		}
	}
	paths, _ := tr["paths"].([]map[string]any)
	if len(paths) != 2 || paths[0]["trace"] == nil || paths[0]["trace_type"] != "VALID" || paths[1]["error"] == nil || !strings.Contains(strings.Join(r.Limits, " "), "1 path trace(s) could not be read") {
		t.Errorf("one traced, one marked failed: %v %v", tr, r.Limits)
	}
	if _, _, err := runSkill(t, "investigate-reachability", routes, `{"network_id":"n1","from":"b1","through":["fw1"],"explain":"routes"}`); !errors.Is(err, skills.ErrInvalidInput) {
		t.Errorf("explain routes does not combine with the search form: %v", err)
	}
}

// A refused trace (403) stops tracing: no trace evidence, a limit says why, and the search answer stands.
func TestReachabilitySearchExplainTraceRefusedStopsTracing(t *testing.T) {
	facets := map[string]any{"totalResults": map[string]any{"value": 1, "type": "EXACT"}, "facets": map[string]any{"pathGroups": []any{
		map[string]any{"traceType": "VALID", "paths": []any{map[string]any{"path": []any{"b1", "fw1"}}}}}}}
	routes := map[string]fwdtest.Handler{snapsPath: ready("s1"), "POST /api/networks/n1/trace-facets": fwdtest.Const(200, facets),
		"POST /api/networks/n1/trace": fwdtest.Const(403, map[string]any{"message": "forbidden"})}
	r, _ := mustRun(t, "investigate-reachability", routes, `{"network_id":"n1","from":"b1","through":["fw1"],"explain":"trace"}`)
	if r.Status != result.OK || len(r.Evidence) != 1 || !strings.Contains(strings.Join(r.Limits, " "), "the trace was refused") {
		t.Errorf("a refused trace stops tracing and leaves the search answer: %s %d %v", r.Status, len(r.Evidence), r.Limits)
	}
}

// With from, Forward takes src_ip as the packet header only: an empty answer for a search that gave both says so.
func TestReachabilityEmptyAnswerWithFromAndSrcIPExplainsWhatSrcIPMeans(t *testing.T) {
	answer := map[string]any{"totalResults": map[string]any{"value": 0, "type": "EXACT"}, "facets": map[string]any{"pathGroups": []any{}}}
	routes := map[string]fwdtest.Handler{snapsPath: ready("s1"), "POST /api/networks/n1/trace-facets": func(*http.Request, []byte) (int, any) { return 200, answer },
		pathsPath: fwdtest.Const(200, pathsBody(false, "EXACT"))}
	for name, in := range map[string]string{
		"search form": `{"network_id":"n1","from":"vd1","src_ip":"192.0.2.10","dst_ip":"198.51.100.1","through":["ingress:vd1 p200.1904"]}`,
		"plain":       `{"network_id":"n1","from":"vd1","src_ip":"192.0.2.10","dst_ip":"198.51.100.1"}`,
	} {
		r, _ := mustRun(t, "investigate-reachability", routes, in)
		if !strings.Contains(strings.Join(r.Limits, " "), "src_ip as the packet's source header only") {
			t.Errorf("%s: %v", name, r.Limits)
		}
	}
	r0, _ := mustRun(t, "investigate-reachability", routes, `{"network_id":"n1","from":"vd1","dst_ip":"198.51.100.1"}`)
	if strings.Contains(strings.Join(r0.Limits, " "), "source header only") {
		t.Errorf("from alone has no src_ip to explain: %v", r0.Limits)
	}
	r, _ := mustRun(t, "investigate-reachability", routes, `{"network_id":"n1","src_ip":"192.0.2.10","dst_ip":"198.51.100.1"}`)
	if strings.Contains(strings.Join(r.Limits, " "), "source header only") {
		t.Errorf("src_ip alone is resolved to a location: no note: %v", r.Limits)
	}
}

// An empty search with src_ip and no from says where Forward located the source: a source on the internet node cannot enter by a partner link, which a through hop on that link then never matches.
func TestReachabilityEmptySearchSaysWhereTheSourceWasLocated(t *testing.T) {
	empty := map[string]any{"totalResults": map[string]any{"value": 0, "type": "EXACT"}, "facets": map[string]any{"pathGroups": []any{}}}
	located := func(kind string) map[string]any {
		b := pathsBody(false, "EXACT")
		b["srcIpLocationType"] = kind
		return b
	}
	routes := func(kind string) map[string]fwdtest.Handler {
		return map[string]fwdtest.Handler{snapsPath: ready("s1"), "POST /api/networks/n1/trace-facets": fwdtest.Const(200, empty), pathsPath: fwdtest.Const(200, located(kind))}
	}
	in := `{"network_id":"n1","src_ip":"192.0.2.10","dst_ip":"198.51.100.1","through":["ingress:vd1 p200.1904"]}`
	r, _ := mustRun(t, "investigate-reachability", routes("INTERNET"), in)
	l := strings.Join(r.Limits, " | ")
	if !strings.Contains(l, "source address was treated as an internet address") || !strings.Contains(l, "located on the INTERNET node") || !strings.Contains(l, "partner or private link") {
		t.Errorf("%v", r.Limits)
	}
	r, _ = mustRun(t, "investigate-reachability", routes("INTERFACE"), in)
	if strings.Contains(strings.Join(r.Limits, " | "), "located on the INTERNET node") {
		t.Errorf("a source located on an interface gets no internet note: %v", r.Limits)
	}
	// leaving alone (no through hop): the internet location is said, but no through-hop explanation
	r, _ = mustRun(t, "investigate-reachability", routes("INTERNET"), `{"network_id":"n1","src_ip":"192.0.2.10","dst_ip":"198.51.100.1","leaving":{"at":"vd1 p200.1904"}}`)
	l2 := strings.Join(r.Limits, " | ")
	if !strings.Contains(l2, "source address was treated as an internet address") || strings.Contains(l2, "a through hop that enters by any other interface") {
		t.Errorf("no through hop, no through explanation: %v", r.Limits)
	}
	// with from, the source is a header: no location is read
	r, _ = mustRun(t, "investigate-reachability", routes("INTERNET"), `{"network_id":"n1","from":"vd1","src_ip":"192.0.2.10","dst_ip":"198.51.100.1","through":["ingress:vd1 p200.1904"]}`)
	if strings.Contains(strings.Join(r.Limits, " | "), "located on the INTERNET node") {
		t.Errorf("with from the source is not located: %v", r.Limits)
	}
	// the plain search failing is said, and the empty answer stays
	bad := routes("INTERNET")
	bad[pathsPath] = fwdtest.Const(500, map[string]any{"message": "boom"})
	r, _ = mustRun(t, "investigate-reachability", bad, in)
	if !strings.HasPrefix(r.Finding, "No path matches") || !strings.Contains(strings.Join(r.Limits, " | "), "could not be read") {
		t.Errorf("%s %v", r.Finding, r.Limits)
	}
}
