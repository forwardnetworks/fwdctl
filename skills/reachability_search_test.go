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
