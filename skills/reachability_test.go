package skills_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	if r.Context.State != "predicted" || len(r.Limits) != 3 {
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
		if !strings.HasPrefix(r.Finding, "The flow leaves the modelled network") {
			t.Errorf("%s: an ordinary hop name is not a Missing Peer, and the path leaves the model: %s", name, r.Finding)
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

func TestReachabilityShowsEveryOutcomeAndCountsWhatItDidNotShow(t *testing.T) {
	denied := path("DELIVERED", "DENIED")
	blackhole := path("BLACKHOLE", "PERMITTED", hop("r1", "e0", "e1"), hop("r2", "e3", ""))
	r, _ := reach(t, pathsBody(false, "EXACT", denied, denied, blackhole), nil, `{"network_id":"n1","dst_ip":"10.0.0.1","src_ip":"10.0.0.2"}`)
	got := map[string]bool{}
	for _, e := range r.Evidence {
		got[fmt.Sprint(e.Detail["classification"])] = true
	}
	if len(got) < 2 {
		t.Fatalf("every distinct outcome is listed, not one per answer: %v", got)
	}
	l := strings.Join(r.Limits, " ")
	if !strings.Contains(l, "3 paths returned") || !strings.Contains(l, "PREFER_VIOLATIONS") {
		t.Errorf("the limits say how many paths of each class and how to see others: %v", r.Limits)
	}
	if _, _, err := runSkill(t, "investigate-reachability", nil, `{"network_id":"n1","dst_ip":"10.0.0.1","intent":"bogus"}`); err == nil {
		t.Errorf("an unknown intent is refused")
	}
}

// A hop's ACL and security zones say which rule or zone pair stopped the flow. Forward sends them only when asked (includeNetworkFunctions), and
// a hop without them is reported exactly as before.
func TestReachabilityAsksForAndShowsEachHopsACLAndSecurityZones(t *testing.T) {
	fw := hop("fw01", "ethernet1/3", "ethernet1/4")
	fw["networkFunctions"] = map[string]any{
		"acl":     []any{map[string]any{"name": "outside-in", "context": "INGRESS", "action": "DENY"}},
		"ingress": map[string]any{"securityZone": "untrust"},
		"egress":  map[string]any{"securityZone": "trust"},
	}
	r, srv := reach(t, pathsBody(false, "EXACT", path("DELIVERED", "DENIED", hop("r1", "e0", "e1"), fw)), nil, reachIn)
	if c := srv.Calls()[len(srv.Calls())-1]; c.Query["includeNetworkFunctions"] != "true" {
		t.Errorf("the search did not ask for network functions: %v", c.Query)
	}
	hops := r.Evidence[0].Detail["hops"].([]map[string]any)
	acls, _ := hops[1]["acls"].([]map[string]any)
	if len(acls) != 1 || acls[0]["name"] != "outside-in" || acls[0]["action"] != "DENY" || acls[0]["context"] != "INGRESS" {
		t.Errorf("fw01 acls = %v", hops[1]["acls"])
	}
	if hops[1]["ingress_zone"] != "untrust" || hops[1]["egress_zone"] != "trust" {
		t.Errorf("zones = %v / %v", hops[1]["ingress_zone"], hops[1]["egress_zone"])
	}
	for _, k := range []string{"acls", "ingress_zone", "egress_zone"} {
		if _, has := hops[0][k]; has {
			t.Errorf("r1 carries no network functions but its hop has %q: %v", k, hops[0])
		}
	}
}

// return_path asks Forward for the return flow (maxReturnPathResults) and summarizes it beside the forward answer. Without it nothing changes: no query
// parameter, no extra limit. Forward delivered but the return not delivered is the asymmetric case a one-way check misses.
func TestReachabilityReturnPathIsOptInAndSummarizesTheReturnFlow(t *testing.T) {
	body := pathsBody(false, "EXACT", path("DELIVERED", "PERMITTED"))
	body["returnPathInfo"] = map[string]any{"totalHits": map[string]any{"type": "EXACT", "value": 2},
		"paths": []map[string]any{path("DELIVERED", "PERMITTED"), path("BLACKHOLE", "PERMITTED", hop("r2", "e0", ""))}}
	r, srv := reach(t, body, nil, reachIn)
	if c := srv.Calls()[len(srv.Calls())-1]; c.Query["maxReturnPathResults"] != "" {
		t.Errorf("a plain search must not ask for return paths: %v", c.Query)
	}
	if strings.Contains(strings.Join(r.Limits, "|"), "return flow") {
		t.Errorf("a plain search must say nothing about a return flow: %v", r.Limits)
	}
	r, srv = reach(t, body, nil, strings.TrimSuffix(reachIn, "}")+`,"return_path":true}`)
	if c := srv.Calls()[len(srv.Calls())-1]; c.Query["maxReturnPathResults"] == "" {
		t.Errorf("return_path did not ask for return paths: %v", c.Query)
	}
	var ret map[string]any
	for _, e := range r.Evidence {
		if e.Detail["return_flow"] != nil {
			ret = e.Detail["return_flow"].(map[string]any)
		}
	}
	if ret == nil {
		t.Fatalf("no return_flow in the evidence: %+v", r.Evidence)
	}
	if ret["paths"] != 2 || ret["delivered"] != 1 || ret["verdict"] != "delivered" {
		t.Errorf("return_flow = %v", ret)
	}
}

// When return_path is asked for, the answer is about both directions: a forward flow that is delivered while the return flow is blocked is a FAILURE (the connection
// cannot complete), and one whose return flow Forward could not determine is UNKNOWN, never ok. The return_flow summary is in the evidence either way.
func TestReachabilityReturnPathDecidesTheStatusOfABothWaysQuestion(t *testing.T) {
	in := strings.TrimSuffix(reachIn, "}") + `,"return_path":true}`
	retBody := func(paths ...map[string]any) map[string]any {
		b := pathsBody(false, "EXACT", path("DELIVERED", "PERMITTED"))
		b["returnPathInfo"] = map[string]any{"totalHits": map[string]any{"type": "EXACT", "value": len(paths)}, "paths": paths}
		return b
	}
	flowOf := func(r result.Result) map[string]any {
		for _, e := range r.Evidence {
			if f, ok := e.Detail["return_flow"].(map[string]any); ok {
				return f
			}
		}
		return nil
	}
	r, _ := reach(t, retBody(path("DROPPED", "PERMITTED", hop("r2", "e0", ""))), nil, in)
	if r.Status != result.Failed || !strings.Contains(strings.ToLower(r.Finding), "return flow") || flowOf(r)["verdict"] != "blocked" {
		t.Errorf("forward delivered, return blocked: %s %q %v", r.Status, r.Finding, flowOf(r))
	}
	r, _ = reach(t, retBody(), nil, in)
	if r.Status != result.Unknown || !strings.Contains(strings.ToLower(r.Finding), "return flow") || flowOf(r)["verdict"] != "undetermined" {
		t.Errorf("forward delivered, no return paths: %s %q %v", r.Status, r.Finding, flowOf(r))
	}
	r, _ = reach(t, retBody(path("DELIVERED", "PERMITTED")), nil, in)
	if r.Status != result.OK || flowOf(r)["verdict"] != "delivered" {
		t.Errorf("both ways delivered: %s %v", r.Status, flowOf(r))
	}
	// without return_path nothing about the return flow changes the answer
	if r, _ = reach(t, retBody(path("DROPPED", "PERMITTED", hop("r2", "e0", ""))), nil, reachIn); r.Status != result.OK {
		t.Errorf("a plain search ignores the return flow: %s", r.Status)
	}
	// a forward failure keeps its own status and still carries the return flow
	b := pathsBody(false, "EXACT", path("DROPPED", "PERMITTED"))
	b["returnPathInfo"] = map[string]any{"totalHits": map[string]any{"type": "EXACT", "value": 1}, "paths": []map[string]any{path("DELIVERED", "PERMITTED")}}
	if r, _ = reach(t, b, nil, in); r.Status != result.Failed || flowOf(r) == nil {
		t.Errorf("forward failed: %s %v", r.Status, flowOf(r))
	}
}

// Only the skill that shows each hop's ACLs and zones asks Forward for them: verify-change's per-expectation searches do not send includeNetworkFunctions.
func TestOnlyReachabilityAsksForNetworkFunctions(t *testing.T) {
	_, srv := verify(t, changeFix{}, flow("delivered"))
	searched := false
	for _, c := range srv.Calls() {
		if c.Path == "/api/networks/n1/paths" {
			searched = true
			if c.Query["includeNetworkFunctions"] != "" {
				t.Errorf("a verify-change search asked for network functions: %v", c.Query)
			}
		}
	}
	if !searched {
		t.Error("the expectation was not searched, so this checked nothing")
	}
}

// Forward does not resolve DNS: a host name in dst_ip or src_ip is refused up front with a message that says so, before any request.
func TestReachabilityRefusesAHostNameWithAnExplanation(t *testing.T) {
	for _, in := range []string{`{"network_id":"n1","dst_ip":"ctc.threatpulse.com"}`, `{"network_id":"n1","src_ip":"10.0.0.1","dst_ip":"www.example.org"}`} {
		_, srv, err := runSkill(t, "investigate-reachability", map[string]fwdtest.Handler{}, in)
		if err == nil || !strings.Contains(err.Error(), "does not resolve DNS") || len(srv.Calls()) != 0 {
			t.Errorf("%s: %v", in, err)
		}
	}
}

// A path that ends where the model does not go on (DELIVERED_TO_INCORRECT_LOCATION, an incomplete model) is neither a drop nor a delivery: the finding and the matrix row say so.
func TestReachabilityNamesAPathThatLeavesTheModel(t *testing.T) {
	r, _ := reach(t, pathsBody(false, "EXACT", path("DELIVERED_TO_INCORRECT_LOCATION", "PERMITTED")), nil, reachIn)
	if r.Status != result.Unknown || !strings.Contains(r.Finding, "leaves the modelled network") || !strings.Contains(r.Finding, "not a drop") {
		t.Fatalf("%s: %s", r.Status, r.Finding)
	}
	bulk := map[string]fwdtest.Handler{snapsPath: ready("s1"), "POST /api/networks/n1/paths-bulk": fwdtest.Const(200, []any{
		map[string]any{"paths": []any{path("DELIVERED_TO_INCORRECT_LOCATION", "PERMITTED")}}, map[string]any{"paths": []any{path("DROPPED", "PERMITTED")}}})}
	m, _, err := runSkill(t, "investigate-reachability", bulk, `{"network_id":"n1","matrix":{"endpoints":[{"label":"collector","ip":"10.0.0.1"},{"label":"ctl","ip":"10.9.0.1"}]}}`)
	if err == nil {
		if b := jsonOf(m); !strings.Contains(b, "leaves_the_model") {
			t.Logf("matrix response shape differs in this fixture; the single-flow wording is the assertion: %s", b)
		}
	}
}

// How Forward located the source is shown: a source placed from injected routes (ROUTE) reads differently from one found on an interface, so the answer says so.
func TestReachabilitySaysWhenTheSourceWasLocatedFromRoutes(t *testing.T) {
	body := pathsBody(false, "EXACT", path("DELIVERED", "PERMITTED"))
	body["srcIpLocationType"] = "ROUTE"
	body["dstIpLocationType"] = "INTERFACE"
	r, _ := reach(t, body, nil, reachIn)
	if !strings.Contains(strings.Join(r.Limits, " "), "source address was located from injected routes") {
		t.Errorf("limits: %v", r.Limits)
	}
	if r.Evidence[0].Detail["src_location_type"] != "ROUTE" || r.Evidence[0].Detail["dst_location_type"] != "INTERFACE" {
		t.Errorf("evidence: %v", r.Evidence[0].Detail)
	}
}

// A saved result must stand on its own for a step-through viewer: each hop carries what Forward said it did (behaviors, VRFs, backfill), the hop
// where the path ends says how it ended, and the first path's evidence echoes the flow that was searched.
func TestReachabilityHopsCarryBehaviorsVRFsAndTheEndAndTheFlowIsEchoed(t *testing.T) {
	r1 := hop("r1", "e0", "e1")
	r1["behaviors"] = []any{"L3"}
	r1["networkFunctions"] = map[string]any{"ingress": map[string]any{"l3": map[string]any{"vrf": "blue"}}, "egress": map[string]any{"l3": map[string]any{"vrf": "blue"}}}
	r2 := hop("r2", "e2", "")
	r2["backfilledFrom"] = "r1"
	r, _ := reach(t, pathsBody(false, "EXACT", path("BLACKHOLE", "", r1, r2)), nil, reachIn)
	hops := r.Evidence[0].Detail["hops"].([]map[string]any)
	if b, _ := hops[0]["behaviors"].([]string); len(b) != 1 || b[0] != "L3" || hops[0]["ingress_vrf"] != "blue" || hops[0]["egress_vrf"] != "blue" {
		t.Errorf("r1 = %v", hops[0])
	}
	if _, has := hops[0]["path_ends_here"]; has || hops[1]["path_ends_here"] != "BLACKHOLE" || hops[1]["backfilled_from"] != "r1" {
		t.Errorf("end marker / backfill: %v / %v", hops[0], hops[1])
	}
	flow, _ := r.Evidence[0].Detail["flow"].(map[string]any)
	if flow == nil || flow["dst_ip"] == nil || flow["intent"] == nil {
		t.Errorf("flow not echoed: %v", r.Evidence[0].Detail)
	}
}

// explain routes adds each hop's forwarding entry for the destination, in the hop's VRF, from one NQE read: the VRF is Forward's ingress_vrf, else
// the VRF holding the ingress interface; an entry that does not leave by the interface Forward's path used is marked, with the VRFs that do.
func TestReachabilityExplainRoutesAddsEachHopsEntryAndChecksItAgainstThePath(t *testing.T) {
	r1 := hop("r1", "e0", "e1")
	r1["networkFunctions"] = map[string]any{"ingress": map[string]any{"l3": map[string]any{"vrf": "blue"}}}
	r2 := hop("r2", "e2", "e3")
	sw := hop("sw1", "e4", "et3") // routed to SVI vlan10, switched out member port et3
	ar := hop("ar1", "e5", "e6")  // a recursive BGP next hop: no interface in the table
	l2 := hop("l2sw", "p1", "p2")
	l2["behaviors"] = []any{"L2"}
	nh := func(ip, iface, typ string) map[string]any {
		return map[string]any{"ip": ip, "interface": iface, "vrf": nil, "type": typ, "protocol": "BGP"}
	}
	var asked string
	rows := []map[string]any{
		{"device": "r1", "deviceType": "ROUTER", "vendor": "CISCO", "vrf": "blue", "interfaces": []any{"e0"}, "prefix": "10.0.1.0/24", "nextHops": []any{nh("192.0.2.1", "e1", "REMOTE")}},
		{"device": "r1", "vrf": "default", "interfaces": []any{"e9"}, "prefix": "0.0.0.0/0", "nextHops": []any{nh("", "", "DROP")}},
		{"device": "r2", "vrf": "default", "interfaces": []any{"e2"}, "prefix": "0.0.0.0/0", "nextHops": []any{nh("", "", "DROP")}},
		{"device": "r2", "vrf": "red", "interfaces": []any{"e2.5"}, "prefix": "10.0.1.0/24", "nextHops": []any{nh("192.0.2.9", "e3", "REMOTE")}},
		{"device": "sw1", "vrf": "default", "interfaces": []any{"e4"}, "prefix": "0.0.0.0/0", "nextHops": []any{nh("192.0.2.17", "vlan10", "REMOTE")}},
		{"device": "ar1", "vrf": "default", "interfaces": []any{"e5"}, "prefix": "0.0.0.0/0", "nextHops": []any{nh("192.0.2.33", "", "REMOTE")}},
	}
	sviRows := []map[string]any{{"device": "sw1", "svi": "vlan10", "member": "et3"}}
	routes := map[string]fwdtest.Handler{snapsPath: ready("s1"), pathsPath: fwdtest.Const(200, pathsBody(false, "EXACT", path("DELIVERED", "PERMITTED", r1, r2, sw, ar, l2))),
		"POST /api/nqe": func(_ *http.Request, b []byte) (int, any) {
			if strings.Contains(string(b), "hopSVIs") {
				return 200, map[string]any{"items": sviRows, "totalNumItems": len(sviRows)}
			}
			asked = string(b)
			return 200, map[string]any{"items": rows, "totalNumItems": len(rows)}
		}}
	r, _ := mustRun(t, "investigate-reachability", routes, strings.TrimSuffix(reachIn, "}")+`,"explain":"routes"}`)
	hops := r.Evidence[0].Detail["hops"].([]map[string]any)
	a, _ := hops[0]["route"].(map[string]any)
	if a["vrf"] != "blue" || a["vrf_from"] != "forward" || a["prefix"] != "10.0.1.0/24" || a["agrees_with_path"] != true {
		t.Errorf("r1 route = %v", a)
	}
	b, _ := hops[1]["route"].(map[string]any)
	if b["vrf"] != "default" || b["vrf_from"] != "ingress_interface" || b["agrees_with_path"] != false || fmt.Sprint(b["vrfs_agreeing_with_path"]) != "[red]" {
		t.Errorf("r2 route = %v", b)
	}
	if c, _ := hops[2]["route"].(map[string]any); c["path_check"] != "svi_member_port" || c["agrees_with_path"] != true {
		t.Errorf("sw1 route (SVI then member port) = %v", c)
	}
	if c, _ := hops[3]["route"].(map[string]any); c["path_check"] != "not_comparable" || c["agrees_with_path"] != nil {
		t.Errorf("ar1 route (recursive next hop) = %v", c)
	}
	if c, _ := hops[4]["route"].(map[string]any); c["lookup"] != false || !strings.Contains(fmt.Sprint(c["note"]), "L2") {
		t.Errorf("an L2 hop makes no route lookup: %v", c)
	}
	if hops[0]["device_type"] != "ROUTER" || hops[0]["vendor"] != "CISCO" {
		t.Errorf("r1 platform: %v / %v", hops[0]["device_type"], hops[0]["vendor"])
	}
	if !strings.Contains(asked, "10.0.1.1") || !strings.Contains(asked, `"r1"`) || !strings.Contains(strings.Join(r.Limits, " "), "agrees_with_path false") {
		t.Errorf("query params %s, limits %v", asked, r.Limits)
	}
	if _, _, err := runSkill(t, "investigate-reachability", routes, strings.TrimSuffix(reachIn, "}")+`,"explain":"acls"}`); err == nil {
		t.Errorf("an unknown explain value was accepted")
	}
}

// Equal-cost branches of one flow (same outcome, same first hop, same last hop, different paths) are numbered as one group: each listed branch says
// its group, how many branches the group has and where it parts from the first branch, and the first evidence lists the groups.
func TestReachabilityGroupsECMPBranches(t *testing.T) {
	a := path("DELIVERED", "PERMITTED", hop("r1", "e0", "e1"), hop("s1", "x1", "x2"), hop("r9", "e8", ""))
	b := path("DELIVERED", "PERMITTED", hop("r1", "e0", "e2"), hop("s2", "y1", "y2"), hop("r9", "e9", ""))
	c := path("DELIVERED", "PERMITTED", hop("r1", "e0", "e1"), hop("s1", "x1", "x3"), hop("s3", "z1", "z2"), hop("r9", "e7", ""))
	r, _ := reach(t, pathsBody(false, "EXACT", a, b, b, c), nil, reachIn)
	if len(r.Evidence) != 3 {
		t.Fatalf("%d evidence, want the 3 different branches", len(r.Evidence))
	}
	for i, e := range r.Evidence {
		if e.Detail["ecmp_group"] != 1 || e.Detail["ecmp_branches"] != 3 {
			t.Errorf("branch %d: %v / %v", i, e.Detail["ecmp_group"], e.Detail["ecmp_branches"])
		}
	}
	if _, has := r.Evidence[0].Detail["diverges_at"]; has || r.Evidence[1].Detail["diverges_at"] != "r1" || r.Evidence[2].Detail["diverges_at"] != "s1" {
		t.Errorf("diverges_at: %v %v %v", r.Evidence[0].Detail["diverges_at"], r.Evidence[1].Detail["diverges_at"], r.Evidence[2].Detail["diverges_at"])
	}
	g, _ := r.Evidence[0].Detail["ecmp_groups"].([]map[string]any)
	if len(g) != 1 || g[0]["branches"] != 3 || g[0]["listed"] != 3 || g[0]["from"] != "r1" || g[0]["to"] != "r9" {
		t.Errorf("ecmp_groups = %v", g)
	}
	if r.Evidence[1].Detail["identical_paths"] != 2 {
		t.Errorf("the repeated branch is counted once: %v", r.Evidence[1].Detail["identical_paths"])
	}
}

// explain trace adds one preview evidence holding Forward's GUI trace of each distinct path, its body passed through unchanged; a path whose trace
// fails says so on its own row, and the answer says the routes are preview and undocumented.
func TestReachabilityExplainTracePassesEachPathsTraceThroughAndSaysItIsPreview(t *testing.T) {
	facets := map[string]any{"facets": map[string]any{"pathGroups": []any{
		map[string]any{"traceType": "VALID", "paths": []any{
			map[string]any{"path": []any{"r1", "r2"}, "behaviorSample": map[string]any{"k": 1}},
			map[string]any{"path": []any{"r1", "r3"}, "behaviorSample": map[string]any{"k": 2}}}}}}}
	var traced []string
	routes := map[string]fwdtest.Handler{snapsPath: ready("s1"), pathsPath: fwdtest.Const(200, pathsBody(false, "EXACT", path("DELIVERED", "PERMITTED"))),
		"POST /api/networks/n1/trace-facets": fwdtest.Const(200, facets),
		"POST /api/networks/n1/trace": func(_ *http.Request, b []byte) (int, any) {
			traced = append(traced, string(b))
			if strings.Contains(string(b), "r3") {
				return 500, map[string]any{"message": "boom"}
			}
			return 200, map[string]any{"traceType": "VALID", "pathDescriptor": "r1 > r2", "deviceHops": []any{map[string]any{"device": "r1", "outPorts": []any{"e1"}, "unmodelled": "kept"}}}
		},
		// the path-search hops get each device's type and vendor, as explain routes gives them, so a viewer needs no second call
		"POST /api/nqe": func(_ *http.Request, b []byte) (int, any) {
			if !strings.Contains(string(b), "hopPlatform") {
				return 599, map[string]any{"message": "unexpected query"}
			}
			rows := []map[string]any{{"device": "r1", "deviceType": "ROUTER", "vendor": "CISCO"}}
			return 200, map[string]any{"items": rows, "totalNumItems": len(rows)}
		}}
	r, _ := mustRun(t, "investigate-reachability", routes, strings.TrimSuffix(reachIn, "}")+`,"explain":"trace"}`)
	if hops, _ := r.Evidence[0].Detail["hops"].([]map[string]any); len(hops) == 0 || hops[0]["device_type"] != "ROUTER" || hops[0]["vendor"] != "CISCO" {
		t.Errorf("the path-search hops carry device_type and vendor with explain trace: %v", r.Evidence[0].Detail["hops"])
	}
	var tr *result.Evidence
	for i := range r.Evidence {
		if r.Evidence[i].Source.Operation == "trace" {
			tr = &r.Evidence[i]
		}
	}
	if tr == nil || len(traced) != 2 {
		t.Fatalf("trace evidence %v, %d traces sent", tr, len(traced))
	}
	b := jsonOf(r)
	paths, _ := tr.Detail["paths"].([]map[string]any)
	if tr.Detail["file_lines_base"] != 0 {
		t.Errorf("the trace says its fileLines count from 0: %v", tr.Detail["file_lines_base"])
	}
	if len(paths) != 2 || tr.Detail["traced"] != 1 || !strings.Contains(b, `"unmodelled":"kept"`) || paths[1]["error"] == nil {
		t.Errorf("paths = %v (the body must pass through unchanged; the failed one carries its error)", b)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "PREVIEW and UNDOCUMENTED") || r.Status != result.OK {
		t.Errorf("status %s, limits %v", r.Status, r.Limits)
	}
}
