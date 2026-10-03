package skills_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

func crossRoutes() map[string]fwdtest.Handler {
	row := func(dev, ip, vrf string) map[string]any { return map[string]any{"device": dev, "ip": ip, "vrf": vrf} }
	items := map[string][]map[string]any{
		"sA": {row("leaf1", "10.0.0.1", "red"), row("leaf2", "10.0.0.2", "red"), row("leaf3", "10.0.0.3", "blue")},
		"sB": {row("leaf1", "10.0.0.1", "red"), row("leaf2", "10.0.0.2", "green"), row("leaf4", "10.0.0.4", "blue")},
	}
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/snapshots": fwdtest.Snapshots(fwdtest.Snap("sA", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")),
		"GET /api/networks/n2/snapshots": fwdtest.Snapshots(fwdtest.Snap("sB", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")),
		nqePath: func(r *http.Request, _ []byte) (int, any) {
			rows := items[r.URL.Query().Get("snapshotId")]
			return 200, map[string]any{"items": rows, "totalNumItems": len(rows)}
		},
	}
}

func TestCompareAcrossNetworksDiffsByKeyAndIgnoresFieldsThatShouldDiffer(t *testing.T) {
	in := `{"network_id":"n1","before_snapshot_id":"sA","after_network_id":"n2","after_snapshot_id":"sB","query_id":"Q1"`
	// whole rows: one identical row, two only on each side
	r, _ := mustRun(t, "compare-nqe-results", crossRoutes(), in+`}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "2 row(s) only in A") || !strings.Contains(r.Finding, "2 only in B") {
		t.Fatalf("%s", r.Finding)
	}
	// keyed by device: leaf3 only in A, leaf4 only in B, leaf2's vrf changed
	r, _ = mustRun(t, "compare-nqe-results", crossRoutes(), in+`,"key":["device"]}`)
	b := jsonOf(r)
	if !strings.Contains(r.Finding, "1 row(s) only in A") || !strings.Contains(r.Finding, "1 with the same key and different fields") ||
		!strings.Contains(b, `"field":"vrf"`) || !strings.Contains(b, `"a":"red"`) || !strings.Contains(b, `"b":"green"`) {
		t.Fatalf("%s %s", r.Finding, b)
	}
	// ignoring vrf: leaf2 no longer differs
	r, _ = mustRun(t, "compare-nqe-results", crossRoutes(), in+`,"key":["device"],"ignore":["vrf"]}`)
	if !strings.Contains(r.Finding, "0 with the same key and different fields") {
		t.Fatalf("an ignored field is not compared: %s", r.Finding)
	}
	for _, bad := range []string{in + `,"key":["nope"]}`, in + `,"key":["vrf"],"ignore":["vrf"]}`, `{"network_id":"n1","before_snapshot_id":"sA","after_snapshot_id":"sA","query_id":"Q1","key":["device"]}`} {
		if _, _, err := runSkill(t, "compare-nqe-results", crossRoutes(), bad); err == nil {
			t.Errorf("%s must be refused", bad)
		}
	}
}
