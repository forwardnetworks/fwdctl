package skills_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

const (
	topoPath    = "GET /api/snapshots/s1/topology"
	locPath     = "GET /api/networks/n1/locations"
	tagsPath    = "GET /api/networks/n1/device-tags"
	aliasesPath = "GET /api/snapshots/s1/aliases"
)

func topo(t *testing.T, routes map[string]fwdtest.Handler, in string) result.Result {
	routes[snapsPath] = ready("s1")
	r, _ := mustRun(t, "inspect-topology", routes, in)
	return r
}

func links(n int) fwdtest.Handler {
	var ls []any
	for i := 0; i < n; i++ {
		ls = append(ls, map[string]any{"sourcePort": fmt.Sprintf("r%d et1", i), "targetPort": fmt.Sprintf("r%d et1", i+1)})
	}
	return fwdtest.Const(200, map[string]any{"links": ls})
}

func TestTopologyLinksFilterByDeviceAndPage(t *testing.T) {
	r := topo(t, map[string]fwdtest.Handler{topoPath: links(100)}, `{"network_id":"n1","kind":"links","device":"r5"}`)
	d := fmt.Sprint(r.Evidence[0].Detail)
	if r.Status != result.OK || !strings.Contains(d, "r5 et1") || !strings.Contains(d, "total:2") {
		t.Fatalf("a device is an end of exactly two links: %s", d)
	}
	r = topo(t, map[string]fwdtest.Handler{topoPath: links(100)}, `{"network_id":"n1","kind":"links","limit":10}`)
	if !strings.Contains(strings.Join(r.Limits, "|"), "Page with offset=10") {
		t.Fatalf("%v", r.Limits)
	}
}

// A device whose name is a prefix of another's must not match it: r1 is not r10.
func TestTopologyDeviceFilterMatchesWholeNames(t *testing.T) {
	ls := fwdtest.Const(200, map[string]any{"links": []any{
		map[string]any{"sourcePort": "r1 et1", "targetPort": "r2 et1"},
		map[string]any{"sourcePort": "r10 et1", "targetPort": "r11 et1"}}})
	r := topo(t, map[string]fwdtest.Handler{topoPath: ls}, `{"network_id":"n1","kind":"links","device":"r1"}`)
	d := fmt.Sprint(r.Evidence[0].Detail)
	if !strings.Contains(d, "total:1") || strings.Contains(d, "r10") {
		t.Fatalf("%s", d)
	}
}

func TestTopologyEmptyKindsAreUnknownNotNone(t *testing.T) {
	for _, tc := range []struct{ kind, path string }{{"links", topoPath}, {"locations", locPath}, {"tags", tagsPath}, {"aliases", aliasesPath}} {
		body := any([]any{})
		if tc.kind == "links" {
			body = map[string]any{"links": []any{}}
		}
		r := topo(t, map[string]fwdtest.Handler{tc.path: fwdtest.Const(200, body)}, fmt.Sprintf(`{"network_id":"n1","kind":%q}`, tc.kind))
		if r.Status != result.Unknown {
			t.Errorf("%s: empty must be unknown, got %s", tc.kind, r.Status)
		}
	}
}

func TestTopologyLocationsTagsAndAliasesReturnTheirRows(t *testing.T) {
	r := topo(t, map[string]fwdtest.Handler{locPath: fwdtest.Const(200, []any{map[string]any{"id": "1", "name": "Atlanta", "city": "Atlanta", "deviceGlobs": []any{"atl-*"}}})},
		`{"network_id":"n1","kind":"locations"}`)
	if r.Status != result.OK || !strings.Contains(fmt.Sprint(r.Evidence[0].Detail), "atl-*") {
		t.Errorf("locations: %+v", r.Evidence)
	}
	r = topo(t, map[string]fwdtest.Handler{tagsPath: fwdtest.Const(200, []any{map[string]any{"name": "Core", "devices": []any{"atl-core-pe01"}}, map[string]any{"name": "Edge", "devices": []any{"x"}}})},
		`{"network_id":"n1","kind":"tags","device":"atl-core-pe01"}`)
	d := fmt.Sprint(r.Evidence[0].Detail)
	if !strings.Contains(d, "Core") || strings.Contains(d, "Edge") {
		t.Errorf("tags filter: %s", d)
	}
	r = topo(t, map[string]fwdtest.Handler{aliasesPath: fwdtest.Const(200, []any{map[string]any{"name": "servers", "type": "HOSTS", "values": []any{"10.0.0.1"}}})},
		`{"network_id":"n1","kind":"aliases"}`)
	if r.Status != result.OK || !strings.Contains(fmt.Sprint(r.Evidence[0].Detail), "servers") {
		t.Errorf("aliases: %+v", r.Evidence)
	}
}
