package skills_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

func lk(a, b string) map[string]any { return map[string]any{"port1": a, "port2": b} }

// ovrRoutes: snapshot s1 holds 6 overrides (5 present, 1 absent); r9 does not exist, r2 has no et9, and only the r1-r2 pair is a link of the topology.
func ovrRoutes() map[string]fwdtest.Handler {
	rows := func(r []map[string]any) (int, any) { return 200, map[string]any{"items": r, "totalNumItems": len(r)} }
	return map[string]fwdtest.Handler{
		snapsPath: ready("s1"),
		"GET /api/snapshots/s1/topology/overrides": fwdtest.Const(200, map[string]any{
			"present": []any{lk("r2 et1", "r1 et1"), lk("r1 et2", "r3 et1"), lk("r1 et3", "r4 et1"), lk("r2 et9", "r5 et1"), lk("r9 et1", "r5 et2")},
			"absent":  []any{lk("r3 et2", "r4 et2")}}),
		topoPath: fwdtest.Const(200, map[string]any{"links": []any{map[string]any{"sourcePort": "r1 et1", "targetPort": "r2 et1"}, map[string]any{"sourcePort": "r2 et1", "targetPort": "r1 et1"}, map[string]any{"sourcePort": "r3 et2", "targetPort": "r4 et2"}}}),
		nqePath: func(_ *http.Request, body []byte) (int, any) {
			q := string(body)
			switch {
			case strings.Contains(q, "foreach iface in device.interfaces"):
				var out []map[string]any
				for _, p := range [][2]string{{"r1", "et1"}, {"r1", "et2"}, {"r1", "et3"}, {"r2", "et1"}, {"r3", "et1"}, {"r3", "et2"}, {"r4", "et1"}, {"r4", "et2"}, {"r5", "et1"}, {"r5", "et2"}} {
					out = append(out, map[string]any{"device": p[0], "iface": p[1]})
				}
				return rows(out)
			case strings.Contains(q, "select {device: device.name}"):
				var out []map[string]any
				for _, d := range []string{"r1", "r2", "r3", "r4", "r5"} {
					out = append(out, map[string]any{"device": d})
				}
				return rows(out)
			}
			return 400, map[string]any{"message": "unexpected query: " + q}
		},
	}
}

func ovrRowsOf(t *testing.T, r result.Result) []map[string]any {
	t.Helper()
	return r.Evidence[0].Detail["rows"].([]map[string]any)
}

func TestLinkOverridesViewSummarisesAndFlagsTheRowsOfThePage(t *testing.T) {
	r, _ := mustRun(t, "inspect-topology", ovrRoutes(), `{"network_id":"n1","kind":"link_overrides"}`)
	d := r.Evidence[0].Detail
	if r.Status != result.OK || d["total"] != 6 || d["present"] != 5 || d["absent"] != 1 || d["device_pairs"] != 6 || !strings.Contains(r.Finding, "snapshot s1") || !strings.Contains(r.Finding, "6 link override") {
		t.Fatalf("%s %s %v", r.Status, r.Finding, d)
	}
	top := d["top_devices"].([]map[string]any)
	if top[0]["device"] != "r1" || top[0]["overrides"] != 3 {
		t.Errorf("top devices: %v", top)
	}
	byPair := map[string]map[string]any{}
	for _, row := range ovrRowsOf(t, r) {
		byPair[row["port1"].(string)+"|"+row["port2"].(string)] = row
	}
	if len(byPair) != 6 {
		t.Fatalf("rows %d", len(byPair))
	}
	// a pair is normalised (port1 <= port2) whichever way it was written
	ok := byPair["r1 et1|r2 et1"]
	if ok == nil || ok["ports_exist"] != true || ok["link_in_topology"] != true || ok["state"] != "present" {
		t.Errorf("a pair that exists and is a link: %v", ok)
	}
	if row := byPair["r1 et2|r3 et1"]; row["ports_exist"] != true || row["link_in_topology"] != false {
		t.Errorf("ports exist but the link is not in the topology: %v", row)
	}
	if row := byPair["r2 et9|r5 et1"]; row["ports_exist"] != false || !strings.Contains(fmt.Sprint(row["missing_ports"]), "r2 et9 (no such interface)") {
		t.Errorf("a missing interface: %v", row)
	}
	if row := byPair["r5 et2|r9 et1"]; row["ports_exist"] != false || !strings.Contains(fmt.Sprint(row["missing_ports"]), "r9 et1 (no such device)") {
		t.Errorf("a missing device: %v", row)
	}
	if row := byPair["r3 et2|r4 et2"]; row["state"] != "absent" || row["link_in_topology"] != true {
		t.Errorf("an absent override whose link is still there: %v", row)
	}
	l := strings.Join(r.Limits, "|")
	for _, want := range []string{"no author, creation time or source", "deprecated for removal in release 26.11", "not exposed by the API", "computed for the rows shown only"} {
		if !strings.Contains(l, want) {
			t.Errorf("limit %q missing: %v", want, r.Limits)
		}
	}
}

func TestLinkOverridesViewPagesInAStableOrderAndFiltersByDevice(t *testing.T) {
	var first []string
	for _, off := range []int{0, 2, 4} {
		r, _ := mustRun(t, "inspect-topology", ovrRoutes(), fmt.Sprintf(`{"network_id":"n1","kind":"link_overrides","limit":2,"offset":%d}`, off))
		rows := ovrRowsOf(t, r)
		if len(rows) != 2 || r.Evidence[0].Detail["offset"] != off {
			t.Fatalf("offset %d: %v", off, rows)
		}
		for _, row := range rows {
			first = append(first, row["port1"].(string)+"|"+row["port2"].(string))
		}
		if off == 0 && !strings.Contains(strings.Join(r.Limits, "|"), "Page with offset=2") {
			t.Errorf("paging is stated: %v", r.Limits)
		}
	}
	seen := map[string]bool{}
	for _, k := range first {
		if seen[k] {
			t.Errorf("a row appeared on two pages: %s", k)
		}
		seen[k] = true
	}
	if len(seen) != 6 {
		t.Errorf("three pages hold all six: %v", first)
	}
	r, _ := mustRun(t, "inspect-topology", ovrRoutes(), `{"network_id":"n1","kind":"link_overrides","device":"r4"}`)
	if r.Evidence[0].Detail["total"] != 2 || r.Evidence[0].Detail["device"] != "r4" {
		t.Errorf("r4 is at one end of two overrides: %v", r.Evidence[0].Detail)
	}
	if r, _ = mustRun(t, "inspect-topology", ovrRoutes(), `{"network_id":"n1","kind":"link_overrides","device":"nope"}`); r.Status != result.Unknown {
		t.Errorf("no match is unknown, never 'no overrides': %s", r.Status)
	}
}

func TestExternalViewShowsTheOverrideSummaryAndOnlyTheFirstPage(t *testing.T) {
	var present []any
	for i := 0; i < 60; i++ {
		present = append(present, lk(fmt.Sprintf("a%02d et1", i), fmt.Sprintf("b%02d et1", i)))
	}
	r, _ := mustRun(t, "inspect-topology", map[string]fwdtest.Handler{
		snapsPath:                                  ready("s1"),
		"GET /api/networks/n1/internet-node":       fwdtest.Const(404, map[string]any{"message": "none"}),
		"GET /api/networks/n1/intranet-nodes":      fwdtest.Const(200, []any{}),
		"GET /api/networks/n1/l3-vpns":             fwdtest.Const(200, []any{}),
		"GET /api/snapshots/s1/topology/overrides": fwdtest.Const(200, map[string]any{"present": present, "absent": []any{}}),
		topoPath: fwdtest.Const(200, map[string]any{"links": []any{}}),
		nqePath:  fwdtest.Const(200, map[string]any{"items": []any{}, "totalNumItems": 0}),
	}, `{"network_id":"n1","kind":"external"}`)
	lo := r.Evidence[0].Detail["link_overrides"].(map[string]any)
	if lo["total"] != 60 || lo["returned"] != 25 || !strings.Contains(fmt.Sprint(lo["more"]), "kind link_overrides") {
		t.Errorf("60 overrides: summary counts all, 25 rows listed: total=%v returned=%v more=%v", lo["total"], lo["returned"], lo["more"])
	}
}

func TestCompareLinkOverridesReportsAddedRemovedAndChanged(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		snapsPath: fwdtest.Snapshots(fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z"), fwdtest.Snap("s2", "PROCESSED", "COLLECTION", "2026-09-02T00:00:00.000Z")),
		"GET /api/snapshots/s1/topology/overrides": fwdtest.Const(200, map[string]any{
			"present": []any{lk("a et1", "b et1"), lk("a et2", "c et1"), lk("d et1", "e et1")}, "absent": []any{lk("x et1", "y et1")}}),
		"GET /api/snapshots/s2/topology/overrides": fwdtest.Const(200, map[string]any{
			"present": []any{lk("b et1", "a et1"), lk("f et1", "g et1"), lk("h et1", "a et1"), lk("x et1", "y et1")}, "absent": []any{lk("d et1", "e et1")}}),
	}
	r, _ := mustRun(t, "compare-link-overrides", routes, `{"network_id":"n1","before_snapshot_id":"s1","after_snapshot_id":"s2"}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "snapshot s1 (4) to snapshot s2 (5): 2 added, 1 removed, 2 changed") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	d := r.Evidence[0].Detail
	c := d["counts"].(map[string]any)
	if c["unchanged"] != 1 || c["added"] != 2 || c["removed"] != 1 || c["changed"] != 2 || c["before_total"] != 4 || c["after_total"] != 5 {
		t.Errorf("counts %v", c)
	}
	text := fmt.Sprint(d["added"], d["removed"], d["changed"])
	for _, want := range []string{"f et1", "h et1", "a et2", "after:absent before:present", "after:present before:absent"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %s", want, text)
		}
	}
	if !strings.Contains(fmt.Sprint(d["devices_involved"]), "device:a") {
		t.Errorf("devices involved: %v", d["devices_involved"])
	}
	r, _ = mustRun(t, "compare-link-overrides", routes, `{"network_id":"n1","before_snapshot_id":"s1","after_snapshot_id":"s2","device":"h"}`)
	if c := r.Evidence[0].Detail["counts"].(map[string]any); c["added"] != 1 || c["removed"] != 0 {
		t.Errorf("device filter: %v", c)
	}
	r, _ = mustRun(t, "compare-link-overrides", routes, `{"network_id":"n1","before_snapshot_id":"s1","after_snapshot_id":"s1"}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "identical") {
		t.Errorf("same snapshot: %s", r.Finding)
	}
}

func TestCompareLinkOverridesIsUnknownWhenASnapshotIsMissingOrUnreadable(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		snapsPath: fwdtest.Snapshots(fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z"), fwdtest.Snap("s2", "PROCESSING", "COLLECTION", "2026-09-02T00:00:00.000Z")),
		"GET /api/snapshots/s1/topology/overrides": fwdtest.Const(200, map[string]any{"present": []any{}, "absent": []any{}}),
		"GET /api/snapshots/s2/topology/overrides": fwdtest.Const(409, map[string]any{"message": "processing"}),
	}
	for _, in := range []string{`{"network_id":"n1","before_snapshot_id":"s1","after_snapshot_id":"nope"}`, `{"network_id":"n1","before_snapshot_id":"s1","after_snapshot_id":"s2"}`} {
		if r, _ := mustRun(t, "compare-link-overrides", routes, in); r.Status != result.Unknown {
			t.Errorf("%s: %s %s", in, r.Status, r.Finding)
		}
	}
}

// The dry run of edit-link-overrides checks the links it adds against the snapshot, with the same derivation as the link_overrides view.
func TestEditLinkOverridesRefusesPortsTheSnapshotLacksUnlessForced(t *testing.T) {
	bad := `{"network_id":"n1","snapshot_id":"s1","add_present":[{"port1":"r1 et1","port2":"nope eth9"}]}`
	r, srv := mustRun(t, "edit-link-overrides", ovrRoutes(), bad)
	if r.Status != result.Failed || writes(srv) != 0 || !strings.Contains(r.Finding, "nope eth9 (no such device)") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	r, srv = mustRun(t, "edit-link-overrides", ovrRoutes(), strings.TrimSuffix(bad, "}")+`,"apply":true}`)
	if r.Status != result.Failed || writes(srv) != 0 {
		t.Fatalf("an applied edit naming a missing port must be refused without force: %s writes=%d", r.Status, writes(srv))
	}
	r, _ = mustRun(t, "edit-link-overrides", ovrRoutes(), strings.TrimSuffix(bad, "}")+`,"force":true}`)
	if r.Status != result.OK {
		t.Fatalf("force allows the dry run: %s %s", r.Status, r.Finding)
	}
}

func TestEditLinkOverridesDryRunShowsTheDeltaAndNotesADiscoveredLink(t *testing.T) {
	// r2 et1 - r1 et1 is already discovered; r1 et3 - r5 et2 is a new link between ports that exist
	in := `{"network_id":"n1","snapshot_id":"s1","add_present":[{"port1":"r2 et1","port2":"r1 et1"}, {"port1":"r5 et2","port2":"r1 et3"}]}`
	r, _ := mustRun(t, "edit-link-overrides", ovrRoutes(), in)
	d := r.Evidence[0].Detail
	if r.Status != result.OK || d["present_before"] != 5 && d["present_before"] != int(5) || !strings.Contains(r.Finding, "present 5 to 6") {
		t.Fatalf("%s %s %v", r.Status, r.Finding, d)
	}
	if _, listed := d["present_before"].([]any); listed {
		t.Error("the dry run must not dump the whole override list")
	}
}
