package skills_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

const networksPath = "GET /api/networks"

func nets(n int) []any {
	out := []any{}
	for i := 0; i < n; i++ {
		out = append(out, map[string]any{"id": fmt.Sprint(100 + i), "name": fmt.Sprintf("net-%02d", i), "orgId": "1"})
	}
	return out
}

func TestListNetworksIsAnAccountLevelResultWithNoNetworkID(t *testing.T) {
	r, _ := mustRun(t, "inspect-networks", map[string]fwdtest.Handler{networksPath: fwdtest.Const(200, nets(3))}, `{}`)
	if r.Status != result.OK || r.Context.Scope != "account" || r.Context.NetworkID != "" {
		t.Fatalf("%+v", r.Context)
	}
	if !strings.Contains(fmt.Sprint(r.Evidence[0].Detail), "net-01") {
		t.Fatalf("%+v", r.Evidence[0].Detail)
	}
}

func TestListNetworksFiltersPagesAndFlagsWorkspaces(t *testing.T) {
	list := append(nets(40), map[string]any{"id": "900", "name": "ws-copy", "orgId": "1", "parentId": "100"})
	r, _ := mustRun(t, "inspect-networks", map[string]fwdtest.Handler{networksPath: fwdtest.Const(200, list)}, `{"limit":10}`)
	l := strings.Join(r.Limits, "|")
	if !strings.Contains(l, "Page with offset=10") || !strings.Contains(l, "workspaces") {
		t.Fatalf("%v", r.Limits)
	}
	r, _ = mustRun(t, "inspect-networks", map[string]fwdtest.Handler{networksPath: fwdtest.Const(200, list)}, `{"name":"ws-"}`)
	d := fmt.Sprint(r.Evidence[0].Detail)
	if !strings.Contains(d, "ws-copy") || !strings.Contains(d, "workspace:true") || !strings.Contains(d, "parent_id:100") {
		t.Fatalf("%s", d)
	}
}

func TestListNetworksNoMatchIsUnknownNotNoNetworks(t *testing.T) {
	for _, in := range []string{`{"name":"zzz"}`, `{"offset":99}`} {
		r, _ := mustRun(t, "inspect-networks", map[string]fwdtest.Handler{networksPath: fwdtest.Const(200, nets(2))}, in)
		if r.Status != result.Unknown {
			t.Errorf("%s: %s", in, r.Status)
		}
	}
	r, _ := mustRun(t, "inspect-networks", map[string]fwdtest.Handler{networksPath: fwdtest.Const(200, []any{})}, `{}`)
	if r.Status != result.Unknown {
		t.Errorf("an empty account must be unknown, got %s", r.Status)
	}
}

// view workspaces summarises the change workspaces (networks with a parent): who made each, when, its retention, and how many snapshots it holds by trigger, for the page asked for (the snapshot reads are bounded by limit).
func TestListNetworksWorkspacesViewSummarisesEachWorkspace(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		networksPath: fwdtest.Const(200, []any{
			map[string]any{"id": "10", "name": "prod", "orgId": "1"},
			map[string]any{"id": "11", "name": "ws-a", "orgId": "1", "parentId": "10", "creator": "alice", "createdAt": "2026-09-01T00:00:00Z", "retentionDays": 14, "secondsToExpiry": 86400},
			map[string]any{"id": "12", "name": "ws-b", "orgId": "1", "parentId": "10", "creator": "bob", "createdAt": "2026-09-02T00:00:00Z"},
		}),
		"GET /api/networks/11/snapshots": fwdtest.Snapshots(
			func() map[string]any {
				s := fwdtest.Snap("a1", "PROCESSED", "IMPORT", "2026-09-01T01:00:00.000Z")
				s["processingTrigger"] = "IMPORT"
				s["totalDevices"] = 5
				return s
			}(),
			func() map[string]any {
				s := fwdtest.Snap("a2", "PROCESSED", "COLLECTION", "2026-09-01T02:00:00.000Z")
				s["processingTrigger"] = "COLLECTION"
				s["totalDevices"] = 5
				return s
			}()),
		"GET /api/networks/12/snapshots": fwdtest.Snapshots(),
	}
	r, srv := mustRun(t, "inspect-networks", routes, `{"view":"workspaces"}`)
	b := jsonOf(r)
	if writes(srv) != 0 || strings.Contains(b, `"name":"prod"`) || !strings.Contains(b, "ws-a") || !strings.Contains(b, "alice") || !strings.Contains(b, `"retention_days":14`) {
		t.Fatalf("%s", b)
	}
	if !strings.Contains(b, `"IMPORT":1`) || !strings.Contains(b, `"COLLECTION":1`) || !strings.Contains(b, `"snapshots":2`) {
		t.Errorf("snapshot counts by trigger: %s", b)
	}
	if !strings.Contains(r.Finding, "2 workspace") {
		t.Errorf("finding: %s", r.Finding)
	}
}

// view primary lists only the networks that are not change workspaces.
func TestNetworksViewPrimaryLeavesOutWorkspaces(t *testing.T) {
	routes := map[string]fwdtest.Handler{"GET /api/networks": fwdtest.Const(200, []any{
		map[string]any{"id": "1", "name": "prod"},
		map[string]any{"id": "2", "name": "ws", "parentId": "1"},
	})}
	r, _ := mustRun(t, "inspect-networks", routes, `{"view":"primary"}`)
	if b := jsonOf(r); !strings.Contains(b, `"name":"prod"`) || strings.Contains(b, `"name":"ws"`) {
		t.Errorf("only prod: %s", b)
	}
}

// view latest answers "what is each network's newest PROCESSED snapshot": a prediction, a snapshot still UNPROCESSED and a whole-second time
// next to a fractional one are the traps (a text compare of the times would pick the wrong one).
func TestListNetworksLatestViewPicksTheNewestProcessedCollectedSnapshot(t *testing.T) {
	processed := func(id, at string, devices int) map[string]any {
		s := fwdtest.Snap(id, "PROCESSED", "COLLECTION", at)
		s["totalDevices"] = devices
		return s
	}
	predicted := fwdtest.Snap("p9", "PROCESSED", "PREDICT", "2026-09-05T00:00:00Z")
	routes := map[string]fwdtest.Handler{
		networksPath: fwdtest.Const(200, []any{
			map[string]any{"id": "10", "name": "prod", "orgId": "1"},
			map[string]any{"id": "11", "name": "quiet", "orgId": "1"},
			map[string]any{"id": "12", "name": "broken", "orgId": "1"},
		}),
		"GET /api/networks/10/snapshots": fwdtest.Snapshots(
			processed("a1", "2026-09-01T01:00:00Z", 5),
			processed("e1", "2026-09-03T02:00:00Z", 6),
			processed("b1", "2026-09-03T02:00:00.500Z", 7),
			fwdtest.Snap("c1", "UNPROCESSED", "COLLECTION", "2026-09-04T00:00:00Z"),
			predicted,
		),
		"GET /api/networks/11/snapshots": fwdtest.Snapshots(fwdtest.Snap("u1", "UNPROCESSED", "COLLECTION", "2026-09-02T00:00:00Z")),
		"GET /api/networks/12/snapshots": fwdtest.Const(403, map[string]any{"message": "no access"}),
	}
	r, srv := mustRun(t, "inspect-networks", routes, `{"view":"latest"}`)
	b := jsonOf(r)
	if writes(srv) != 0 {
		t.Fatalf("a read must write nothing: %d", writes(srv))
	}
	if r.Status != result.Unknown {
		t.Errorf("one unreadable network must make the result unknown, got %s", r.Status)
	}
	if !strings.Contains(b, `"latest_processed_snapshot_id":"b1"`) || !strings.Contains(b, `"devices_in_latest_processed":7`) {
		t.Errorf("prod must report b1 (the fractional-second one is the newest; the prediction and the UNPROCESSED are not): %s", b)
	}
	if !strings.Contains(b, "none: no PROCESSED collected snapshot") {
		t.Errorf("quiet has only an UNPROCESSED snapshot and must say none: %s", b)
	}
	if !strings.Contains(b, "snapshots_error") {
		t.Errorf("broken must carry its read error, not an empty answer: %s", b)
	}
}
