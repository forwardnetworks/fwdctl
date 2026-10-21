package skills_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

func routingRoutes() map[string]fwdtest.Handler {
	node := func(dev string, rs int) map[string]any {
		return map[string]any{"device": dev, "vrf": "default", "routeSetId": rs, "asNumber": 65000 + rs, "protocol": "BGP"}
	}
	return map[string]fwdtest.Handler{
		snapsPath: ready("s1"),
		"POST /api/networks/n1/bgp-prefix-search": fwdtest.Const(200, map[string]any{
			"origin":           []any{map[string]any{"device": "edge1", "vrf": "default", "protocol": "BGP"}},
			"devicesByOutcome": map[string]any{"INSTALLED": []any{map[string]any{"device": "core1", "vrf": "default", "protocol": "BGP"}, map[string]any{"device": "core2", "vrf": "default", "protocol": "BGP"}}, "REJECTED": []any{map[string]any{"device": "dist1", "vrf": "default", "protocol": "BGP"}}},
		}),
		"POST /api/networks/n1/bgp-prefix-trace": fwdtest.Const(200, []any{[]any{node("edge1", 1), node("core1", 2)}}),
		"POST /api/networks/n1/bgp-prefix-details": func(_ *http.Request, b []byte) (int, any) {
			return 200, map[string]any{"prefix": "10.9.0.0/24", "device": "core1", "vrf": "default", "attributes": map[string]any{"asPath": []any{65001}, "localPref": 100}, "policies": map[string]any{"IMPORT": map[string]any{"rule": "permit"}}}
		},
		"GET /api/networks/n1/devices/core1": fwdtest.Const(200, map[string]any{"device": "core1", "vendor": "CISCO", "model": "n9k", "vrfs": []any{map[string]any{"vrf": "default",
			"recvAdvertisements":          []any{map[string]any{"nexthop": "192.0.2.1", "outcome": "INSTALLED", "originatedFrom": map[string]any{"device": "edge1", "vrf": "default"}}},
			"moreSpecificInstalledRoutes": []any{"10.9.0.0/25"}}}}),
		"GET /api/networks/n1/bgp-installed-prefixes": fwdtest.Const(200, map[string]any{"totalPrefixCount": 3, "nodes": []any{map[string]any{"device": "core1", "vendorOs": "cisco_nxos", "vrf": "default", "prefixes": []any{"10.9.0.0/24", "10.8.0.0/24"}}}}),
	}
}

// search answers "which devices accept, reject or lack this prefix, and where it comes from": counts per outcome with the devices, the origin, and what was read.
func TestRoutingSearchGivesOriginAndOutcomes(t *testing.T) {
	r, srv := mustRun(t, "inspect-bgp-neighbors", routingRoutes(), `{"network_id":"n1","routing":{"prefix":"10.9.0.0/24"}}`)
	b, l := jsonOf(r), strings.Join(r.Limits, " ")
	if r.Status != result.OK || writes(srv) != 1 { // the search is a POST that reads
		t.Fatalf("%s writes=%d %s", r.Status, writes(srv), b)
	}
	for _, want := range []string{`"origin"`, `edge1`, `"INSTALLED"`, `"REJECTED"`, `core2`, `"count":2`} {
		if !strings.Contains(b, want) {
			t.Errorf("missing %s in %s", want, b)
		}
	}
	for _, want := range []string{"preview", "BGP", "ADMIN"} {
		if !strings.Contains(l, want) {
			t.Errorf("limits miss %q: %s", want, l)
		}
	}
}

// trace fills in the origin and outcome from a search when only the router is given, and returns the hop paths; details takes a hop of that trace and returns its attributes and policies.
func TestRoutingTraceAndDetails(t *testing.T) {
	r, _ := mustRun(t, "inspect-bgp-neighbors", routingRoutes(), `{"network_id":"n1","routing":{"prefix":"10.9.0.0/24","view":"trace","router":{"device":"core1"}}}`)
	b := jsonOf(r)
	if r.Status != result.OK || !strings.Contains(b, `"paths"`) || !strings.Contains(b, "edge1") || !strings.Contains(b, "core1") || !strings.Contains(strings.Join(r.Limits, " "), "origin and outcome were taken from the search") {
		t.Fatalf("trace: %s %s", r.Status, b)
	}
	r, _ = mustRun(t, "inspect-bgp-neighbors", routingRoutes(), `{"network_id":"n1","routing":{"prefix":"10.9.0.0/24","view":"details","router":{"device":"core1"}}}`)
	b = jsonOf(r)
	if r.Status != result.OK || !strings.Contains(b, "localPref") || !strings.Contains(b, "IMPORT") {
		t.Fatalf("details: %s %s", r.Status, b)
	}
}

// device shows what a device received and installed for the prefix, per VRF; installed lists the prefixes installed on a device or on a vendor's devices.
func TestRoutingDeviceAndInstalled(t *testing.T) {
	r, _ := mustRun(t, "inspect-bgp-neighbors", routingRoutes(), `{"network_id":"n1","routing":{"prefix":"10.9.0.0/24","view":"device","device":"core1"}}`)
	if r.Status != result.OK || !strings.Contains(jsonOf(r), "recvAdvertisements") && !strings.Contains(jsonOf(r), "received_advertisements") {
		t.Fatalf("device: %s %s", r.Status, jsonOf(r))
	}
	if !strings.Contains(jsonOf(r), "10.9.0.0/25") {
		t.Errorf("more-specific installed routes: %s", jsonOf(r))
	}
	r, _ = mustRun(t, "inspect-bgp-neighbors", routingRoutes(), `{"network_id":"n1","routing":{"view":"installed","device":"core1"}}`)
	if r.Status != result.OK || !strings.Contains(jsonOf(r), "10.8.0.0/24") || !strings.Contains(jsonOf(r), `"total_prefix_count":3`) {
		t.Fatalf("installed: %s %s", r.Status, jsonOf(r))
	}
}

// A role that cannot use the routing app is told so by name; a node that does not resolve is unknown, not an error; bad input is refused by name.
func TestRoutingDeniedAndBadInput(t *testing.T) {
	routes := routingRoutes()
	routes["POST /api/networks/n1/bgp-prefix-search"] = fwdtest.Const(403, map[string]any{"message": "No permission for network operation BGP_PREFIX_SEARCH"})
	r, _ := mustRun(t, "inspect-bgp-neighbors", routes, `{"network_id":"n1","routing":{"prefix":"10.9.0.0/24"}}`)
	if r.Status != result.Unknown || !strings.Contains(r.Finding+strings.Join(r.Limits, " "), "BGP_PREFIX_SEARCH") {
		t.Errorf("denied: %s %s %v", r.Status, r.Finding, r.Limits)
	}
	routes = routingRoutes()
	routes["POST /api/networks/n1/bgp-prefix-details"] = fwdtest.Const(200, map[string]any{"error": "no such node"})
	r, _ = mustRun(t, "inspect-bgp-neighbors", routes, `{"network_id":"n1","routing":{"prefix":"10.9.0.0/24","view":"details","router":{"device":"core1"}}}`)
	if r.Status != result.Unknown {
		t.Errorf("lookup error is unknown: %s %s", r.Status, r.Finding)
	}
	for _, bad := range []string{`{"network_id":"n1","routing":{"view":"search"}}`, `{"network_id":"n1","routing":{"prefix":"not-a-prefix"}}`, `{"network_id":"n1","routing":{"prefix":"10.0.0.0/8","view":"poem"}}`, `{"network_id":"n1","routing":{"prefix":"10.0.0.0/8","view":"device"}}`, `{"network_id":"n1","advertised":{},"routing":{"prefix":"10.0.0.0/8"}}`} {
		if _, _, err := runSkill(t, "inspect-bgp-neighbors", routingRoutes(), bad); err == nil {
			t.Errorf("should be refused: %s", bad)
		}
	}
}
