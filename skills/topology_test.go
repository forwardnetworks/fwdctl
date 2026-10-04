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

// Links are collected: an empty answer may be a wrong name or an uncollected feature, so it stays unknown. Locations, tags and aliases are DEFINED by an administrator: a successful empty
// read is "none defined", said as such, so it cannot be confused with a failed read (which is an error).
func TestTopologyEmptyCollectedIsUnknownAndEmptyDefinedIsNoneDefined(t *testing.T) {
	r := topo(t, map[string]fwdtest.Handler{topoPath: fwdtest.Const(200, map[string]any{"links": []any{}})}, `{"network_id":"n1","kind":"links"}`)
	if r.Status != result.Unknown {
		t.Errorf("links: empty must be unknown, got %s", r.Status)
	}
	for _, tc := range []struct{ kind, path string }{{"locations", locPath}, {"tags", tagsPath}, {"aliases", aliasesPath}} {
		r := topo(t, map[string]fwdtest.Handler{tc.path: fwdtest.Const(200, []any{})}, fmt.Sprintf(`{"network_id":"n1","kind":%q}`, tc.kind))
		if r.Status != result.OK || !strings.Contains(r.Finding, "are defined (the read succeeded") {
			t.Errorf("%s: an empty defined list is 'none defined': %s %s", tc.kind, r.Status, r.Finding)
		}
	}
	// a failed read is an error, never an empty list
	routes := map[string]fwdtest.Handler{tagsPath: fwdtest.Const(500, map[string]any{"message": "boom"}), snapsPath: ready("s1")}
	if _, _, err := runSkill(t, "inspect-topology", routes, `{"network_id":"n1","kind":"tags"}`); err == nil {
		t.Errorf("a failed read must be an error, not none defined")
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

func TestTopologyLocationsCarryCoordinatesIdsPlacementAndTheUnlocatedCount(t *testing.T) {
	r := topo(t, map[string]fwdtest.Handler{
		locPath:                                fwdtest.Const(200, []any{map[string]any{"id": "7", "name": "Atlanta", "city": "Atlanta", "adminDivision": "GA", "country": "US", "lat": 33.7, "lng": -84.4, "deviceGlobs": []any{"atl-*"}}}),
		"GET /api/networks/n1/atlas":           fwdtest.Const(200, map[string]any{"locations": []any{map[string]any{"locationId": "7", "devices": []any{"a1"}, "anchoredDevices": []any{"a1-ctx"}, "dynamicMatchDevices": []any{"atl-sw1"}}}}),
		"GET /api/networks/n1/device-statuses": fwdtest.Const(200, []any{map[string]any{"deviceName": "a1"}, map[string]any{"deviceName": "atl-sw1"}, map[string]any{"deviceName": "lonely"}}),
	}, `{"network_id":"n1","kind":"locations"}`)
	d := r.Evidence[0].Detail
	row := d["rows"].([]map[string]any)[0]
	if r.Status != result.OK || row["id"] != "7" || row["admin_division"] != "GA" || row["lat"] != 33.7 || row["device_count"] != 3 {
		t.Fatalf("row: %v", row)
	}
	if d["devices_without_a_location"] != 1 || d["devices_with_a_location"] != 3 {
		t.Errorf("unlocated: %v", d)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "unpublished") {
		t.Errorf("the placement view is marked unpublished: %v", r.Limits)
	}
}

func TestTopologyLocationsNeverCountZeroUnlocatedFromAnEmptyDeviceList(t *testing.T) {
	r := topo(t, map[string]fwdtest.Handler{
		locPath:                                fwdtest.Const(200, []any{map[string]any{"id": "7", "name": "Atlanta"}}),
		"GET /api/networks/n1/atlas":           fwdtest.Const(200, map[string]any{"locations": []any{}}),
		"GET /api/networks/n1/device-statuses": fwdtest.Const(200, []any{}),
	}, `{"network_id":"n1","kind":"locations"}`)
	if _, has := r.Evidence[0].Detail["devices_without_a_location"]; has {
		t.Errorf("an empty device list proves nothing about unlocated devices: %v", r.Evidence[0].Detail)
	}
}
