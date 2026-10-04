package skills_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
	"github.com/forwardnetworks/fwdctl/skills"
)

const (
	snapsPath = "GET /api/networks/n1/snapshots"
	pathsPath = "GET /api/networks/n1/paths"
)

func hop(name, in, out string) map[string]any {
	return map[string]any{"deviceName": name, "displayName": name, "ingressInterface": in, "egressInterface": out}
}

func path(fwdOutcome, sec string, hops ...map[string]any) map[string]any {
	if len(hops) == 0 {
		hops = []map[string]any{hop("r1", "e0", "e1"), hop("fw1", "e2", "")}
	}
	return map[string]any{"forwardingOutcome": fwdOutcome, "securityOutcome": sec, "hops": hops}
}

func pathsBody(timedOut bool, hits string, ps ...map[string]any) map[string]any {
	if ps == nil {
		ps = []map[string]any{}
	}
	return map[string]any{"timedOut": timedOut, "info": map[string]any{
		"totalHits": map[string]any{"type": hits, "value": len(ps)}, "paths": ps}}
}

func ready(id string) fwdtest.Handler {
	return fwdtest.Snapshots(fwdtest.Snap(id, "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z"))
}

const reachIn = `{"network_id":"n1","src_ip":"10.0.0.1","dst_ip":"10.0.1.1","protocol":"tcp","dst_port":"443"}`

func runSkill(t *testing.T, name string, routes map[string]fwdtest.Handler, input string) (result.Result, *fwdtest.Server, error) {
	t.Helper()
	sess, srv := fwdtest.New(t, routes)
	r, err := skills.Run(context.Background(), name, sess, json.RawMessage(input))
	return r, srv, err
}

func mustRun(t *testing.T, name string, routes map[string]fwdtest.Handler, input string) (result.Result, *fwdtest.Server) {
	t.Helper()
	r, srv, err := runSkill(t, name, routes, input)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if p := r.Validate(); len(p) != 0 {
		t.Fatalf("invalid result: %v", p)
	}
	return r, srv
}

func reach(t *testing.T, body map[string]any, snaps fwdtest.Handler, input string) (result.Result, *fwdtest.Server) {
	if snaps == nil {
		snaps = ready("s1")
	}
	return mustRun(t, "investigate-reachability", map[string]fwdtest.Handler{snapsPath: snaps, pathsPath: fwdtest.Const(200, body)}, input)
}

func TestReachabilityDeliveredIsOKAndCitesThePath(t *testing.T) {
	r, srv := reach(t, pathsBody(false, "EXACT", path("DELIVERED", "PERMITTED")), nil, reachIn)
	if r.Status != result.OK || r.Confidence != result.Deterministic {
		t.Fatalf("got %s/%s", r.Status, r.Confidence)
	}
	if r.Evidence[0].Detail["classification"] != "delivered" || *r.Context.SnapshotID != "s1" {
		t.Errorf("evidence/context wrong: %+v %v", r.Evidence[0].Detail, r.Context)
	}
	c := srv.Calls()[len(srv.Calls())-1]
	if c.Query["intent"] != "PREFER_DELIVERED" || c.Query["ipProto"] != "6" || c.Query["dstPort"] != "443" || c.Query["maxResults"] != "20" {
		t.Errorf("query = %v", c.Query)
	}
}

func TestReachabilitySecurityDeniedIsAFailureWithTheLastHop(t *testing.T) {
	r, _ := reach(t, pathsBody(false, "EXACT", path("DELIVERED", "DENIED", hop("r1", "e0", "e1"), hop("fw01", "ethernet1/3", ""))), nil, reachIn)
	if r.Status != result.Failed {
		t.Fatalf("status %s", r.Status)
	}
	last := r.Evidence[0].Detail["last_hop"].(map[string]any)
	if last["device"] != "fw01" || last["ingress_interface"] != "ethernet1/3" {
		t.Errorf("last hop %v", last)
	}
}

func TestReachabilityEachFailureOutcomeIsClassified(t *testing.T) {
	want := map[string]string{"BLACKHOLE": "missing_route", "DROPPED": "dropped", "INADMISSIBLE": "not_admitted",
		"LOOP": "routing_loop", "UNREACHABLE": "unreachable"}
	for outcome, class := range want {
		r, _ := reach(t, pathsBody(false, "EXACT", path(outcome, "PERMITTED")), nil, reachIn)
		if r.Status != result.Failed || r.Evidence[0].Detail["classification"] != class {
			t.Errorf("%s -> %s / %v", outcome, r.Status, r.Evidence[0].Detail["classification"])
		}
	}
}

func TestReachabilityOneDeliveredAmongFailuresIsOK(t *testing.T) {
	r, _ := reach(t, pathsBody(false, "EXACT", path("BLACKHOLE", "PERMITTED"), path("DELIVERED", "PERMITTED")), nil, reachIn)
	if r.Status != result.OK {
		t.Fatalf("status %s", r.Status)
	}
}

func TestReachabilityZeroPathsIsUnknownNeverOK(t *testing.T) {
	r, _ := reach(t, pathsBody(false, "EXACT"), nil, reachIn)
	if r.Status != result.Unknown || r.Confidence != result.NoBasis {
		t.Fatalf("got %s/%s", r.Status, r.Confidence)
	}
	found := false
	for _, l := range r.Limits {
		if strings.Contains(l, "returned no paths") {
			found = true
		}
	}
	if !found {
		t.Errorf("the reason (no paths were returned) is not stated: %v", r.Limits)
	}
}

func TestReachabilityTimeoutWithoutADeliveredPathIsUnknownNotFailed(t *testing.T) {
	r, _ := reach(t, pathsBody(true, "EXACT", path("BLACKHOLE", "PERMITTED")), nil, reachIn)
	if r.Status != result.Unknown {
		t.Fatalf("status %s", r.Status)
	}
}

func TestReachabilityIncompleteModelOnlyIsUnknown(t *testing.T) {
	r, _ := reach(t, pathsBody(false, "EXACT", path("DELIVERED_TO_INCORRECT_LOCATION", "PERMITTED")), nil, reachIn)
	if r.Status != result.Unknown || r.NextActions[0] != "investigate-collection-failure" {
		t.Fatalf("got %s %v", r.Status, r.NextActions)
	}
}

func TestReachabilityNoProcessedSnapshotSendsNoSearch(t *testing.T) {
	snaps := fwdtest.Snapshots(fwdtest.Snap("s3", "UNPROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z"))
	r, srv := reach(t, pathsBody(false, "EXACT", path("DELIVERED", "PERMITTED")), snaps, reachIn)
	if r.Status != result.Unknown || srv.Called("GET", "/api/networks/n1/paths") {
		t.Fatalf("status %s, searched=%v", r.Status, srv.Called("GET", "/api/networks/n1/paths"))
	}
}

func TestReachabilityNeverPicksAPredictedSnapshotAsTheLatest(t *testing.T) {
	snaps := fwdtest.Snapshots(
		fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z"),
		fwdtest.Snap("p1", "PROCESSED", "PREDICT", "2026-09-05T00:00:00.000Z"))
	r, srv := reach(t, pathsBody(false, "EXACT", path("DELIVERED", "PERMITTED")), snaps, reachIn)
	if *r.Context.SnapshotID != "s1" {
		t.Errorf("read from %s, want the collected s1", *r.Context.SnapshotID)
	}
	last := srv.Calls()[len(srv.Calls())-1]
	if last.Query["snapshotId"] != "s1" {
		t.Errorf("search snapshotId = %q", last.Query["snapshotId"])
	}
}

func TestReachabilityTruncationAndPredictedAreLimits(t *testing.T) {
	snaps := fwdtest.Snapshots(fwdtest.Snap("p1", "PROCESSED", "PREDICT", "2026-09-05T00:00:00.000Z"))
	r, _ := reach(t, pathsBody(false, "LOWER_BOUND", path("DELIVERED", "PERMITTED")), snaps,
		`{"network_id":"n1","dst_ip":"10.0.1.1","src_ip":"10.0.0.1","snapshot_id":"p1"}`)
	if r.Context.State != "predicted" || len(r.Limits) != 2 {
		t.Errorf("state %s limits %v", r.Context.State, r.Limits)
	}
}

func TestReachabilityAttachesTheOperationLog(t *testing.T) {
	r, _ := reach(t, pathsBody(false, "EXACT", path("DELIVERED", "PERMITTED")), nil, reachIn)
	found := false
	for _, o := range r.Operations {
		if o.Status == 200 {
			found = true
		}
	}
	if !found || len(r.Operations) < 2 {
		t.Errorf("operations %+v", r.Operations)
	}
}

func TestBadInputIsRejectedBeforeAnyCall(t *testing.T) {
	_, srv, err := runSkill(t, "investigate-reachability", map[string]fwdtest.Handler{}, `{"network_id":"n1"}`)
	if !errors.Is(err, skills.ErrInvalidInput) {
		t.Fatalf("err = %v", err)
	}
	if len(srv.Calls()) != 0 {
		t.Errorf("made %d calls", len(srv.Calls()))
	}
}

func TestAnInsecureSessionRecordsThatInEveryResult(t *testing.T) {
	sess, _ := fwdtest.New(t, map[string]fwdtest.Handler{snapsPath: ready("s1"),
		pathsPath: fwdtest.Const(200, pathsBody(false, "EXACT", path("DELIVERED", "PERMITTED")))})
	sess.SetInsecureForTest()
	r, err := skills.Run(context.Background(), "investigate-reachability", sess, json.RawMessage(reachIn))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "TLS certificate verification was disabled") {
		t.Errorf("limits %v", r.Limits)
	}
}

// The source finder traces each candidate to the control address: the ones that arrive come first, farther ones before nearer; a control nobody reaches is failed.
func TestFindTraceSourceRanksTheCandidatesWhoseControlTraceArrives(t *testing.T) {
	rows := []map[string]any{{"device": "edge-near"}, {"device": "core-far"}, {"device": "blackholed"}, {"device": "skipme"}}
	n := 0
	routes := map[string]fwdtest.Handler{
		snapsPath: ready("s1"),
		nqePath:   fwdtest.Const(200, map[string]any{"items": rows, "totalNumItems": len(rows)}),
		pathsPath: func(r *http.Request, _ []byte) (int, any) {
			n++
			switch r.URL.Query().Get("from") {
			case "edge-near":
				return 200, pathsBody(false, "EXACT", path("DELIVERED", "PERMITTED", hop("edge-near", "e0", "e1"), hop("edge1", "e2", "")))
			case "core-far":
				return 200, pathsBody(false, "EXACT", path("DELIVERED", "PERMITTED", hop("core-far", "a", "b"), hop("dist", "c", "d"), hop("edge1", "e2", "")))
			}
			return 200, pathsBody(false, "EXACT", path("BLACKHOLE", "PERMITTED", hop("blackholed", "e0", "")))
		},
	}
	r, _ := mustRun(t, "find-trace-source", routes, `{"network_id":"n1","target_ip":"203.0.113.1","exclude":["skipme"]}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "2 of 3") || !strings.Contains(r.Finding, "core-far") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	if n != 3 {
		t.Errorf("an excluded device must not be traced: %d traces", n)
	}
	b, _ := json.Marshal(r.Evidence)
	if !strings.Contains(string(b), `"delivered":false`) || !strings.Contains(strings.Join(r.Limits, " "), "not that it reaches the real destination") {
		t.Errorf("the blackholed candidate is listed and the control-only limit is stated: %s %v", b, r.Limits)
	}
	// nobody reaches the control
	routes[pathsPath] = fwdtest.Const(200, pathsBody(false, "EXACT", path("BLACKHOLE", "PERMITTED", hop("x", "e0", ""))))
	if r, _ := mustRun(t, "find-trace-source", routes, `{"network_id":"n1","target_ip":"203.0.113.1"}`); r.Status != result.Failed {
		t.Errorf("no good source is failed: %s", r.Status)
	}
	if _, _, err := runSkill(t, "find-trace-source", routes, `{"network_id":"n1","target_ip":"203.0.113.1","vrf":"bad vrf!"}`); err == nil {
		t.Error("a malformed VRF name must be refused")
	}
}

func TestReachabilityMissingPeerMarkerIsExplainedAndStaysUnknown(t *testing.T) {
	mp := hop("wan1.example.net-missing-peer", "wan1.example.net/po1000.698", "self")
	r, _ := reach(t, pathsBody(false, "EXACT", path("DELIVERED_TO_INCORRECT_LOCATION", "PERMITTED", hop("r1", "e0", "e1"), hop("wan1.example.net", "e2", "po1000.698"), mp)), nil, reachIn)
	if r.Status != result.Unknown {
		t.Fatalf("an incomplete model is never a pass: %s", r.Status)
	}
	for _, want := range []string{"leaves wan1.example.net po1000.698 toward a peer that is not modelled", "plan-synthetic-device", "inspect-edge"} {
		if !strings.Contains(r.Finding, want) {
			t.Errorf("finding lacks %q: %s", want, r.Finding)
		}
	}
	if r.Evidence[0].Detail["classification"] != "incomplete_model" {
		t.Errorf("classification changed: %v", r.Evidence[0].Detail["classification"])
	}
	m, _ := r.Evidence[0].Detail["missing_peer"].(map[string]any)
	if m["device"] != "wan1.example.net" || m["interface"] != "po1000.698" || m["vlan"] != 698 || m["hop_name"] != "wan1.example.net-missing-peer" {
		t.Errorf("missing_peer = %v", m)
	}
	acts := strings.Join(r.NextActions, " ")
	if !strings.Contains(acts, "plan-synthetic-device") || !strings.Contains(acts, "inspect-edge") {
		t.Errorf("next actions: %v", r.NextActions)
	}
	// no VLAN in the name: the field is omitted
	r, _ = reach(t, pathsBody(false, "EXACT", path("DELIVERED_TO_INCORRECT_LOCATION", "PERMITTED", hop("wan1-missing-peer", "wan1/po1000", "self"))), nil, reachIn)
	if m, _ := r.Evidence[0].Detail["missing_peer"].(map[string]any); m == nil || m["interface"] != "po1000" || m["vlan"] != nil {
		t.Errorf("no vlan expected: %v", m)
	}
}

func TestReachabilityOrdinaryHopNamesAreNotMissingPeers(t *testing.T) {
	for _, name := range []string{"peer-missing", "missing-peer", "missing-peer-01", "wan1"} {
		r, _ := reach(t, pathsBody(false, "EXACT", path("DELIVERED_TO_INCORRECT_LOCATION", "PERMITTED", hop("r1", "e0", "e1"), hop(name, "e2", ""))), nil, reachIn)
		if _, has := r.Evidence[0].Detail["missing_peer"]; has {
			t.Errorf("%s was taken for a Missing Peer", name)
		}
		if r.Finding != "The returned paths do not establish whether the flow is delivered" {
			t.Errorf("%s: finding changed: %s", name, r.Finding)
		}
	}
}

func TestReachabilityListsEachHopWithItsInterfacesAndReportsIdenticalPathsOnce(t *testing.T) {
	same := path("DROPPED", "PERMITTED", hop("r1", "e0", "e1"), hop("fw1", "e2", ""))
	other := path("DROPPED", "PERMITTED", hop("r1", "e0", "e9"), hop("fw2", "e2", ""))
	r, _ := reach(t, pathsBody(false, "EXACT", same, same, same, other), nil, reachIn)
	if len(r.Evidence) != 2 {
		t.Fatalf("three copies of one path and one different path are two evidence items, not four: %d", len(r.Evidence))
	}
	d := r.Evidence[0].Detail
	hops := d["hops"].([]map[string]any)
	if d["identical_paths"] != 3 || len(hops) != 2 || hops[0]["device"] != "r1" || hops[0]["ingress_interface"] != "e0" || hops[0]["egress_interface"] != "e1" || hops[1]["egress_interface"] != nil {
		t.Errorf("per-hop interfaces and the repeat count: %v", d)
	}
}
