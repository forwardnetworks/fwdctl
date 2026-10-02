package skills_test

import (
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

func claimRoutes(extra map[string]fwdtest.Handler) map[string]fwdtest.Handler {
	r := modelRoutes()
	for k, v := range extra {
		r[k] = v
	}
	return r
}

func synConn(dev, port string, vlan any, extra map[string]any) map[string]any {
	m := map[string]any{"uplinkPort": map[string]any{"device": dev, "port": port}}
	if vlan != nil {
		m["vlan"] = vlan
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func TestInspectEdgeUnclaimedExitIsAMissingPeerToModel(t *testing.T) {
	r, _ := mustRun(t, "inspect-edge", claimRoutes(map[string]fwdtest.Handler{
		"GET /api/networks/n1/internet-node": fwdtest.Const(404, map[string]any{"message": "none"}),
	}), `{"network_id":"n1"}`)
	text := modelEv(t, r)
	for _, want := range []string{`"claimed_by":null`, `"exits_unclaimed":1`, `"exits_claimed":0`} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %s in %s", want, text)
		}
	}
	if !strings.Contains(r.Finding, "1 unclaimed") || !strings.Contains(r.Finding, "Missing Peer") {
		t.Errorf("finding: %s", r.Finding)
	}
	// kinds this Forward does not serve are a limit, not an error, and weaken "unclaimed"
	if !strings.Contains(strings.Join(r.Limits, "|"), "could not be read") {
		t.Errorf("unread kinds are stated: %v", r.Limits)
	}
}

func TestInspectEdgeClaimedByAnL3VPNQueryConnection(t *testing.T) {
	r, _ := mustRun(t, "inspect-edge", claimRoutes(map[string]fwdtest.Handler{
		"GET /api/networks/n1/internet-node": fwdtest.Const(200, map[string]any{"name": "internet", "connections": []any{synConn("other", "e9", nil, nil)}}),
		"GET /api/networks/n1/l3-vpns": fwdtest.Const(200, []any{map[string]any{"name": "dir", "connections": []any{},
			"queryId": "Q_1", "queryResult": map[string]any{"connections": []any{synConn("wan1", "po1", 698, map[string]any{"vrf": "INET"})}}}}),
	}), `{"network_id":"n1"}`)
	text := modelEv(t, r)
	for _, want := range []string{`"kind":"l3vpn"`, `"node":"dir"`, `"source":"query"`, `"uplink":"wan1 po1"`, `"vlan":698`, `"vrf":"INET"`, `"exits_claimed":1`, `"exits_unclaimed":0`} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %s in %s", want, text)
		}
	}
	if strings.Contains(text, "double_claim") {
		t.Errorf("one claimant is not a double claim: %s", text)
	}
	// the same uplink with another VLAN does not claim the subinterface
	r, _ = mustRun(t, "inspect-edge", claimRoutes(map[string]fwdtest.Handler{
		"GET /api/networks/n1/l3-vpns": fwdtest.Const(200, []any{map[string]any{"name": "dir", "connections": []any{synConn("wan1", "po1", 100, nil)}}}),
	}), `{"network_id":"n1"}`)
	if !strings.Contains(modelEv(t, r), `"exits_unclaimed":1`) {
		t.Errorf("a different VLAN must not claim: %s", modelEv(t, r))
	}
}

func TestInspectEdgeDoubleClaimIsReported(t *testing.T) {
	r, _ := mustRun(t, "inspect-edge", claimRoutes(map[string]fwdtest.Handler{
		"GET /api/networks/n1/internet-node": fwdtest.Const(200, map[string]any{"name": "internet", "connections": []any{
			map[string]any{"uplinkPort": map[string]any{"device": "wan1", "port": "po1"}, "gatewayPort": map[string]any{"device": "wan1", "port": "po1.698"}}}}),
		"GET /api/networks/n1/l3-vpns": fwdtest.Const(200, []any{map[string]any{"name": "dir", "connections": []any{synConn("wan1", "po1", 698, nil)}}}),
	}), `{"network_id":"n1"}`)
	if r.Status != result.OK {
		t.Fatalf("%s", r.Status)
	}
	text := modelEv(t, r)
	if !strings.Contains(text, `"double_claim":[`) || !strings.Contains(text, `"kind":"internet"`) || !strings.Contains(text, `"kind":"l3vpn"`) {
		t.Errorf("double claim lists both claimants: %s", text)
	}
	i, j := strings.Index(text, `"claimed_by":{"gateway":"wan1 po1.698"`), strings.Index(text, `"double_claim"`)
	if i < 0 || i > j {
		t.Errorf("claimed_by is the first claimant (the internet node): %s", text)
	}
	if !strings.Contains(strings.Join(r.Limits, "|"), "not documented") {
		t.Errorf("limit on the double claim: %v", r.Limits)
	}
}

func edgeTwoSnaps() fwdtest.Handler {
	return fwdtest.Snapshots(
		fwdtest.Snap("s2", "PROCESSED", "COLLECTION", "2026-09-02T00:00:00.000Z"),
		fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z"))
}

func claimedL3() map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		snapsPath:                            edgeTwoSnaps(),
		"GET /api/networks/n1/internet-node": fwdtest.Const(200, map[string]any{"name": "internet", "connections": []any{synConn("wan1", "po1", 698, nil)}}),
	}
}

// The nodes are read as configured now, but a snapshot used the node configuration valid at its creation: an older snapshot cannot be given a claim state.
func TestInspectEdgeOlderSnapshotClaimIsUnknown(t *testing.T) {
	r, _ := mustRun(t, "inspect-edge", claimRoutes(claimedL3()), `{"network_id":"n1","snapshot_id":"s1"}`)
	text := modelEv(t, r)
	for _, want := range []string{`"claimed_in_snapshot":"unknown"`, `"claimed_by_now":{`, `"claimed_by_basis":"nodes as configured now"`, `"exits_claimed_now":1`} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %s in %s", want, text)
		}
	}
	for _, bad := range []string{`"claimed_by":`, `"exits_claimed":`, `"exits_unclaimed":`} {
		if strings.Contains(text, bad) {
			t.Errorf("an older snapshot must not present %s as fact: %s", bad, text)
		}
	}
	if !strings.Contains(r.Finding, "unknown") || !strings.Contains(r.Finding, "not this snapshot") {
		t.Errorf("finding: %s", r.Finding)
	}
	l := strings.Join(r.Limits, "|")
	if !strings.Contains(l, "not the newest processed") || !strings.Contains(l, "does not expose which version") {
		t.Errorf("limits: %v", r.Limits)
	}
}

func TestInspectEdgeNewestSnapshotKeepsClaimedByWithBasisLabel(t *testing.T) {
	for _, in := range []string{`{"network_id":"n1"}`, `{"network_id":"n1","snapshot_id":"s2"}`} {
		r, _ := mustRun(t, "inspect-edge", claimRoutes(claimedL3()), in)
		text := modelEv(t, r)
		for _, want := range []string{`"claimed_by":{`, `"claimed_by_basis":"nodes as configured now"`, `"exits_claimed":1`} {
			if !strings.Contains(text, want) {
				t.Errorf("%s: missing %s in %s", in, want, text)
			}
		}
		if strings.Contains(text, "claimed_in_snapshot") || strings.Contains(text, "claimed_by_now") {
			t.Errorf("%s: the newest snapshot keeps claimed_by: %s", in, text)
		}
		if !strings.Contains(strings.Join(r.Limits, "|"), "does not expose which version") {
			t.Errorf("basis limit missing: %v", r.Limits)
		}
	}
}
