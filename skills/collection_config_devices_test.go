package skills_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
)

// A device listing that is paged must give the same pages on every call: Forward returns the list in no fixed order, so the rows are sorted by name before they are windowed, and the result carries a digest of the
// whole list so two pages can be shown to come from the same list. `collector` limits the rows to the devices pinned to one collector (or `none`), and `names_only` returns them as compact [name, collector] pairs in
// pages large enough to read a whole network in a few calls.
func deviceListRoutes(n int) map[string]fwdtest.Handler {
	var devs []any
	for i := n - 1; i >= 0; i-- { // descending: the API's order is not the sorted order
		d := map[string]any{"name": fmt.Sprintf("d%03d", i), "host": "h"}
		switch {
		case i%3 == 0:
			d["collectorId"] = "C101290"
		case i%3 == 1:
			d["collectorId"] = "C101291"
		}
		devs = append(devs, d)
	}
	return map[string]fwdtest.Handler{"GET /api/networks/n1/classic-devices": fwdtest.Const(200, devs)}
}

func TestDevicePagesAreSortedByNameAndCarryADigestOfTheWholeList(t *testing.T) {
	r1, _ := mustRun(t, "inspect-collection-config", deviceListRoutes(30), `{"network_id":"n1","limit":10}`)
	r2, _ := mustRun(t, "inspect-collection-config", deviceListRoutes(30), `{"network_id":"n1","limit":10,"offset":10}`)
	a := r1.Evidence[0].Detail["devices"].([]map[string]any)
	b := r2.Evidence[0].Detail["devices"].([]map[string]any)
	if a[0]["name"] != "d000" || a[9]["name"] != "d009" || b[0]["name"] != "d010" {
		t.Fatalf("pages should be sorted by name: %v ... %v", a[0]["name"], b[0]["name"])
	}
	d1, d2 := r1.Evidence[0].Detail["device_list_digest"], r2.Evidence[0].Detail["device_list_digest"]
	if d1 == nil || d1 != d2 {
		t.Errorf("two pages of the same list carry the same digest: %v %v", d1, d2)
	}
}

func TestDeviceListCanBeFilteredByCollectorAndReadAsNamesOnly(t *testing.T) {
	r, _ := mustRun(t, "inspect-collection-config", deviceListRoutes(30), `{"network_id":"n1","collector":"none","names_only":true}`)
	pairs := r.Evidence[0].Detail["devices"].([][]string)
	if len(pairs) != 10 || pairs[0][0] != "d002" || pairs[0][1] != "" {
		t.Fatalf("unpinned devices as [name, collector]: %v", pairs)
	}
	if r.Evidence[0].Detail["devices_matching"] != 10 {
		t.Errorf("the count of matching devices: %v", r.Evidence[0].Detail["devices_matching"])
	}
	r, _ = mustRun(t, "inspect-collection-config", deviceListRoutes(30), `{"network_id":"n1","collector":"101291","names_only":true}`)
	pairs = r.Evidence[0].Detail["devices"].([][]string)
	if len(pairs) != 10 || pairs[0][1] != "C101291" {
		t.Errorf("a collector given as 101291 or C101291 matches the same devices: %v", pairs)
	}
	if _, _, err := runSkill(t, "inspect-collection", deviceListRoutes(3), `{"network_id":"n1","view":"status","collector":"none"}`); err == nil || !strings.Contains(err.Error(), "collector") {
		t.Errorf("collector belongs to view config: %v", err)
	}
}
