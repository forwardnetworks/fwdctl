package skills_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

// bulkRoutes answers paths-bulk with one result per query, in order. route picks the hops for a source and destination address.
func bulkRoutes(sent *[]map[string]any, route func(src, dst string) []string, outcome func(src, dst string) string) map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		snapsPath: ready("s1"),
		"POST /api/networks/n1/paths-bulk": func(r *http.Request, b []byte) (int, any) {
			var req struct {
				Queries []map[string]any `json:"queries"`
			}
			if err := json.Unmarshal(b, &req); err != nil || len(req.Queries) == 0 {
				// the SDK may send the bare list
				json.Unmarshal(b, &req.Queries)
			}
			*sent = req.Queries
			out := make([]any, 0, len(req.Queries))
			for _, q := range req.Queries {
				src, dst := q["srcIp"].(string), q["dstIp"].(string)
				var hops []map[string]any
				for _, d := range route(src, dst) {
					hops = append(hops, hop(d, "e0", "e1"))
				}
				oc := outcome(src, dst)
				if oc == "TIMEOUT" {
					out = append(out, pathsBody(true, "EXACT"))
					continue
				}
				out = append(out, pathsBody(false, "EXACT", path(oc, "PERMITTED", hops...)))
			}
			return 200, out
		},
	}
}

const matrixIn = `{"network_id":"n1","protocol":"tcp","dst_port":"443","matrix":{"endpoints":[{"label":"Site A","ip":"10.1.0.1"},{"label":"Site B","ip":"10.2.0.1"},{"label":"Site C","ip":"10.3.0.1"}],"transit_groups":[{"label":"Core","pattern":"^core"},{"label":"Edge","pattern":"^edge"}],"max_overall_seconds":120}`

// A full mesh is one bulk request with a query for every ordered pair; each cell says delivered, blocked or undetermined and which transit groups the path crossed; a pair whose two directions cross different groups is named as asymmetric.
func TestMatrixPairsBothDirectionsAndNamesTheAsymmetricPair(t *testing.T) {
	var sent []map[string]any
	routes := bulkRoutes(&sent, func(src, dst string) []string {
		if src == "10.1.0.1" && dst == "10.2.0.1" {
			return []string{"core1", "edge1"} // A to B crosses Core then Edge
		}
		if src == "10.2.0.1" && dst == "10.1.0.1" {
			return []string{"edge2"} // the reply crosses only Edge: asymmetric
		}
		return []string{"core1"}
	}, func(src, dst string) string {
		if dst == "10.3.0.1" && src == "10.2.0.1" {
			return "DROPPED"
		}
		return "DELIVERED"
	})
	r, _ := mustRun(t, "investigate-reachability", routes, matrixIn+`}`)
	b := jsonOf(r)
	if r.Status != result.OK || len(sent) != 6 {
		t.Fatalf("%s: %d queries sent, want 6 (3 endpoints, every ordered pair): %s", r.Status, len(sent), b)
	}
	for _, want := range []string{`"verdict":"delivered"`, `"verdict":"blocked"`, `"asymmetric_pairs"`, `"Site A"`, `"Site B"`, `"groups":["Core","Edge"]`, `"cells"`} {
		if !strings.Contains(b, want) {
			t.Errorf("missing %s in %s", want, b)
		}
	}
	if !strings.Contains(r.Finding, "1 pair") || !strings.Contains(r.Finding, "asymmetric") || !strings.Contains(r.Finding, "1 blocked") {
		t.Errorf("finding: %s", r.Finding)
	}
}

// A query that timed out is undetermined and counted as such, never as blocked or delivered; the limits name the bulk timeout rules.
func TestMatrixTimedOutCellsAreUndeterminedAndNamed(t *testing.T) {
	var sent []map[string]any
	routes := bulkRoutes(&sent, func(_, _ string) []string { return []string{"core1"} }, func(src, dst string) string {
		if src == "10.1.0.1" && dst == "10.3.0.1" {
			return "TIMEOUT"
		}
		return "DELIVERED"
	})
	r, _ := mustRun(t, "investigate-reachability", routes, matrixIn+`}`)
	b, l := jsonOf(r), strings.Join(r.Limits, " ")
	if !strings.Contains(b, `"verdict":"undetermined"`) || !strings.Contains(r.Finding, "1 undetermined") || !strings.Contains(l, "timed out") {
		t.Errorf("timeout: %s | %s | %s", r.Finding, l, b)
	}
}

// Bad input is refused by name before any request: too few endpoints, a duplicate, a bad address, or a matrix mixed with a single-flow input.
func TestMatrixBadInputIsRefused(t *testing.T) {
	for _, bad := range []string{
		`{"network_id":"n1","matrix":{"endpoints":[{"label":"A","ip":"10.0.0.1"}]}}`,
		`{"network_id":"n1","matrix":{"endpoints":[{"label":"A","ip":"10.0.0.1"},{"label":"A","ip":"10.0.0.2"}]}}`,
		`{"network_id":"n1","matrix":{"endpoints":[{"label":"A","ip":"10.0.0.1"},{"label":"B","ip":"not-an-ip"}]}}`,
		`{"network_id":"n1","src_ip":"10.0.0.1","matrix":{"endpoints":[{"label":"A","ip":"10.0.0.1"},{"label":"B","ip":"10.0.0.2"}]}}`,
		`{"network_id":"n1","matrix":{"endpoints":[{"label":"A","ip":"10.0.0.1"},{"label":"B","ip":"10.0.0.2"}],"transit_groups":[{"label":"X","pattern":"("}]}}`,
	} {
		var sent []map[string]any
		if _, _, err := runSkill(t, "investigate-reachability", bulkRoutes(&sent, func(_, _ string) []string { return nil }, func(_, _ string) string { return "DELIVERED" }), bad); err == nil {
			t.Errorf("should be refused: %s", bad)
		}
		if len(sent) != 0 {
			t.Errorf("a request was sent for refused input: %s", bad)
		}
	}
}
