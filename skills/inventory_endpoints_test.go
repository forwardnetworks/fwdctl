package skills_test

import (
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

// kind endpoints rolls the snapshot's network endpoints up by profile: how many there are, how many were collected, how many failed and where, plus the configured endpoints that are switched off. One
// read, not a page per fifty endpoints. `name` limits it to profiles whose name contains the text.
func inventoryEndpointRoutes(rows []any, configured []any) map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		snapsPath:                        ready("s1"),
		"GET /api/networks/n1/endpoints": fwdtest.Const(200, configured),
		nqePath:                          fwdtest.Const(200, map[string]any{"items": rows, "totalNumItems": len(rows)}),
	}
}

func epRow(name, profile, res string) map[string]any {
	return map[string]any{"Name": name, "Profile": profile, "Location": "lab", "Result": res}
}

func TestInventoryEndpointsRollsUpByProfileAndNamesTheFailures(t *testing.T) {
	rows := []any{epRow("h1", "linux", "completed"), epRow("h2", "linux", "completed"), epRow("h3", "linux", "collectionFailed"), epRow("p1", "pdu", "completed"), epRow("p2", "pdu", "processingFailed")}
	configured := []any{map[string]any{"type": "CLI", "name": "h1", "collect": true}, map[string]any{"type": "CLI", "name": "off1", "collect": false}}
	r, _ := mustRun(t, "inspect-inventory", inventoryEndpointRoutes(rows, configured), `{"network_id":"n1","kind":"endpoints"}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "5 endpoints") || !strings.Contains(r.Finding, "2 not collected") {
		t.Fatalf("%s %q", r.Status, r.Finding)
	}
	d := r.Evidence[0].Detail
	profiles, _ := d["profiles"].([]map[string]any)
	if len(profiles) != 2 || profiles[0]["profile"] != "linux" || profiles[0]["endpoints"] != 3 || profiles[0]["collected"] != 2 || profiles[0]["not_collected"] != 1 {
		t.Fatalf("profiles = %v", profiles)
	}
	if d["configured_collect_off"] != 1 {
		t.Errorf("configured_collect_off = %v", d["configured_collect_off"])
	}
	failed, _ := d["not_collected"].([]map[string]any)
	if len(failed) != 2 || failed[0]["name"] == "" || failed[0]["result"] == "" {
		t.Errorf("not_collected = %v", failed)
	}
	r, _ = mustRun(t, "inspect-inventory", inventoryEndpointRoutes(rows, configured), `{"network_id":"n1","kind":"endpoints","name":"PDU"}`)
	profiles, _ = r.Evidence[0].Detail["profiles"].([]map[string]any)
	if len(profiles) != 1 || profiles[0]["profile"] != "pdu" {
		t.Errorf("name filters profiles, case-insensitively: %v", profiles)
	}
}

func TestInventoryEndpointsNoneIsUnknownNotZero(t *testing.T) {
	r, _ := mustRun(t, "inspect-inventory", inventoryEndpointRoutes([]any{}, []any{}), `{"network_id":"n1","kind":"endpoints"}`)
	if r.Status != result.Unknown {
		t.Errorf("no endpoints in the snapshot is unknown: %s %s", r.Status, r.Finding)
	}
}
