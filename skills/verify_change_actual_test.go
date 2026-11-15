package skills_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

// view actual compares, per edited device, base to predicted (what Predict said) with base to the after-change snapshot (what happened), next
// hops included: the same change on both sides is a match, a change only Predict made is predicted_only (failed), a change only the network made
// is actual_only (said, never hidden). It only reads.
func actualRoutes(tables map[string][]map[string]any) map[string]fwdtest.Handler {
	f := changeFix{}
	routes := f.routes()
	routes[snapsPath] = fwdtest.Snapshots(
		fwdtest.Snap("b1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z"),
		fwdtest.Snap("a1", "PROCESSED", "PREDICT", "2026-09-02T00:00:00.000Z"),
		fwdtest.Snap("c2", "PROCESSED", "COLLECTION", "2026-09-03T00:00:00.000Z"))
	routes["GET /api/networks/n1/change-sets/cs1/commits/head"] = fwdtest.Const(200, map[string]any{"id": "cs1", "commitId": "c", "deviceToChanges": map[string]any{"edge1": map[string]any{"commands": "x"}}})
	routes["GET /api/networks/n1/change-sets/cs1/draft"] = fwdtest.Const(200, map[string]any{"draft": nil})
	routes[nqePath] = func(r *http.Request, b []byte) (int, any) {
		rows := tables[r.URL.Query().Get("snapshotId")]
		items := make([]any, 0, len(rows))
		for _, x := range rows {
			items = append(items, x)
		}
		return 200, map[string]any{"items": items, "totalNumItems": len(items)}
	}
	return routes
}

func entry(prefix string, hops ...string) map[string]any {
	h := make([]any, 0, len(hops))
	for _, x := range hops {
		h = append(h, x)
	}
	return map[string]any{"vrf": "default", "prefix": prefix, "nextHops": h}
}

const actualIn = `{"network_id":"n1","view":"actual","change_set_id":"cs1","after_snapshot_id":"c2"}`

func TestVerifyChangeActualMatchesDiffersAndActualOnly(t *testing.T) {
	keep := entry("192.0.2.0/24", "198.51.100.1")
	// Predict moved 203.0.113.0/24 to a new next hop and removed 198.51.100.0/24; the network did the same: a match
	r, srv := mustRun(t, "verify-change", actualRoutes(map[string][]map[string]any{
		"b1": {keep, entry("203.0.113.0/24", "198.51.100.1"), entry("198.51.100.0/24", "198.51.100.1")},
		"a1": {keep, entry("203.0.113.0/24", "198.51.100.9")},
		"c2": {keep, entry("203.0.113.0/24", "198.51.100.9")},
	}), actualIn)
	if r.Status != result.OK || !strings.Contains(r.Finding, "2 match, 0 differ, 0 predicted only, 0 actual only") {
		t.Fatalf("match: %s %s", r.Status, r.Finding)
	}
	for _, c := range srv.Calls() {
		if c.Method != "GET" && c.Path != "/api/nqe" {
			t.Errorf("view actual only reads: %s %s", c.Method, c.Path)
		}
	}
	// the network went to a different next hop than predicted, and also removed a route Predict kept: failed, both named
	r, _ = mustRun(t, "verify-change", actualRoutes(map[string][]map[string]any{
		"b1": {keep, entry("203.0.113.0/24", "198.51.100.1")},
		"a1": {keep, entry("203.0.113.0/24", "198.51.100.9")},
		"c2": {entry("203.0.113.0/24", "198.51.100.7")},
	}), actualIn)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "0 match, 1 differ, 0 predicted only, 1 actual only") {
		t.Fatalf("differs: %s %s", r.Status, r.Finding)
	}
	// Predict changed nothing, the network changed a route: unknown (another cause or a prediction gap), never "matches"
	r, _ = mustRun(t, "verify-change", actualRoutes(map[string][]map[string]any{
		"b1": {keep}, "a1": {keep}, "c2": {keep, entry("203.0.113.0/24", "198.51.100.1")},
	}), actualIn)
	if r.Status != result.Unknown || strings.Contains(r.Finding, "matches") {
		t.Fatalf("actual only: %s %s", r.Status, r.Finding)
	}
	// Predict changed a route the network did not: failed
	r, _ = mustRun(t, "verify-change", actualRoutes(map[string][]map[string]any{
		"b1": {keep, entry("203.0.113.0/24", "198.51.100.1")}, "a1": {keep}, "c2": {keep, entry("203.0.113.0/24", "198.51.100.1")},
	}), actualIn)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "1 predicted only") {
		t.Fatalf("predicted only: %s %s", r.Status, r.Finding)
	}
	// a match plus a change Predict did not make: ok, and the finding names the actual_only rows
	// 203.0.113.0/24 removed on both (match); 192.0.2.0/24 moved next hop only on the network (actual_only)
	r, _ = mustRun(t, "verify-change", actualRoutes(map[string][]map[string]any{
		"b1": {keep, entry("203.0.113.0/24", "198.51.100.1")}, "a1": {keep}, "c2": {entry("192.0.2.0/24", "198.51.100.5")},
	}), actualIn)
	if r.Status != result.OK || !strings.Contains(r.Finding, "1 actual only") || !strings.Contains(r.Finding, "read them") {
		t.Fatalf("match plus actual only: %s %s", r.Status, r.Finding)
	}
	// nothing changed on either side: unknown, nothing about the change was compared
	r, _ = mustRun(t, "verify-change", actualRoutes(map[string][]map[string]any{"b1": {keep}, "a1": {keep}, "c2": {keep}}), actualIn)
	if r.Status != result.Unknown || !strings.Contains(r.Finding, "No forwarding entry changed") {
		t.Fatalf("no change: %s %s", r.Status, r.Finding)
	}
	// a device with no entries in one snapshot is not proven read: not compared, unknown
	r, _ = mustRun(t, "verify-change", actualRoutes(map[string][]map[string]any{"b1": {keep}, "a1": {keep}, "c2": {}}), actualIn)
	if r.Status != result.Unknown || !strings.Contains(strings.Join(r.Limits, " "), "not proven read") {
		t.Fatalf("unread device: %s %s %v", r.Status, r.Finding, r.Limits)
	}
}

func TestVerifyChangeActualRefusesTheWrongSnapshots(t *testing.T) {
	tables := map[string][]map[string]any{"b1": {entry("192.0.2.0/24", "198.51.100.1")}}
	for in, want := range map[string]string{
		`{"network_id":"n1","view":"actual","change_set_id":"cs1","after_snapshot_id":"b1"}`: "base snapshot",
		`{"network_id":"n1","view":"actual","change_set_id":"cs1","after_snapshot_id":"a1"}`: "predicted snapshot",
	} {
		r, _ := mustRun(t, "verify-change", actualRoutes(tables), in)
		if r.Status != result.Unknown || !strings.Contains(r.Finding, want) {
			t.Errorf("%s: %s %s", in, r.Status, r.Finding)
		}
	}
	if _, _, err := runSkill(t, "verify-change", actualRoutes(tables), `{"network_id":"n1","view":"actual","change_set_id":"cs1"}`); err == nil {
		t.Errorf("view actual needs after_snapshot_id")
	}
	if _, _, err := runSkill(t, "verify-change", actualRoutes(tables), `{"network_id":"n1","view":"actual","change_set_id":"cs1","after_snapshot_id":"c2","run_predict":true}`); err == nil {
		t.Errorf("view actual never runs a prediction: run_predict is refused")
	}
}

// A next hop is its address and egress interface: Predict moving a route to the same address out of another interface than the network did is
// "differs", not "match" (an interface-only move otherwise reads as unchanged).
func TestVerifyChangeActualComparesTheEgressInterface(t *testing.T) {
	hop := func(prefix, ip, iface string) map[string]any {
		return map[string]any{"vrf": "default", "prefix": prefix, "nextHops": []any{map[string]any{"ip": ip, "iface": iface}}}
	}
	keep := hop("192.0.2.0/24", "198.51.100.1", "et1")
	r, _ := mustRun(t, "verify-change", actualRoutes(map[string][]map[string]any{
		"b1": {keep, hop("203.0.113.0/24", "198.51.100.1", "et1")},
		"a1": {keep, hop("203.0.113.0/24", "198.51.100.1", "et2")},
		"c2": {keep, hop("203.0.113.0/24", "198.51.100.1", "et3")},
	}), actualIn)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "1 differ") || !strings.Contains(jsonOf(r), "198.51.100.1%et2") {
		t.Fatalf("an egress-interface difference is a difference: %s %s", r.Status, r.Finding)
	}
}

// When a snapshot carries no interface names (a predicted snapshot that leaves interfaceName empty would), next hops are compared on addresses
// alone: the same move on both sides is still a match, and a limit says interfaces were not compared.
func TestVerifyChangeActualFallsBackToAddressesWithoutInterfaces(t *testing.T) {
	withIf := func(prefix, ip, iface string) map[string]any {
		return map[string]any{"vrf": "default", "prefix": prefix, "nextHops": []any{map[string]any{"ip": ip, "iface": iface}}}
	}
	keep := withIf("192.0.2.0/24", "198.51.100.1", "et1")
	r, _ := mustRun(t, "verify-change", actualRoutes(map[string][]map[string]any{
		"b1": {keep, withIf("203.0.113.0/24", "198.51.100.1", "et1")},
		"a1": {withIf("192.0.2.0/24", "198.51.100.1", ""), withIf("203.0.113.0/24", "198.51.100.9", "")},
		"c2": {keep, withIf("203.0.113.0/24", "198.51.100.9", "et2")},
	}), actualIn)
	if r.Status != result.OK || !strings.Contains(r.Finding, "1 match, 0 differ") || !strings.Contains(strings.Join(r.Limits, " "), "addresses only") {
		t.Fatalf("addresses only when a snapshot has no interfaces: %s %s %v", r.Status, r.Finding, r.Limits)
	}
}
