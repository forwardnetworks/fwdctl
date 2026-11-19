package skills_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
	"github.com/forwardnetworks/fwdctl/skills"
)

const (
	csList   = "GET /api/networks/n1/change-sets"
	csPred   = "GET /api/networks/n1/change-sets/cs1/predicted-snapshots"
	connPath = "GET /api/diffs/b1/a1/subnet-connectivity"
)

var diffAreas = []string{"devices", "interfaces", "topology", "l2", "acl", "cloud-objects", "cloud-acl", "routing-loop/count"}

func conn(partial bool, pairs, isolated, connected int) map[string]any {
	return map[string]any{"isPartialResult": partial, "totalSubnetPairs": pairs, "evaluatedSubnetPairs": pairs,
		"newlyIsolatedSubnetPairs": isolated, "newlyConnectedSubnetPairs": connected, "modifiedSubnetPairs": 0}
}

func chk(id, name, status string, viol int) map[string]any {
	c := map[string]any{"id": id, "name": name, "status": status, "enabled": true}
	if viol > 0 {
		c["numViolations"] = viol
	}
	return c
}

type changeFix struct {
	predicted  []map[string]any
	before     []map[string]any
	after      []map[string]any
	conn       map[string]any
	paths      map[string]any
	afterSt    string
	counts     map[string]int
	incomplete map[string]bool
}

func (f changeFix) routes() map[string]fwdtest.Handler {
	afterState := f.afterSt
	if afterState == "" {
		afterState = "PROCESSED"
	}
	pred := f.predicted
	if pred == nil {
		pred = []map[string]any{{"id": "a1", "state": afterState, "processedAt": "2026-09-02T00:00:00.000Z", "processingTrigger": "PREDICT"}}
	}
	r := map[string]fwdtest.Handler{
		snapsPath: fwdtest.Snapshots(
			fwdtest.Snap("b1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z"),
			fwdtest.Snap("a1", afterState, "PREDICT", "2026-09-02T00:00:00.000Z")),
		csList:                         fwdtest.Const(200, map[string]any{"changeSets": []map[string]any{{"id": "cs1", "name": "cs", "networkId": "n1", "snapshotId": "b1"}}}),
		csPred:                         fwdtest.Const(200, map[string]any{"predictedSnapshots": pred}),
		connPath:                       fwdtest.Const(200, orConn(f.conn)),
		"GET /api/snapshots/b1/checks": fwdtest.Const(200, map[string]any{"checks": orEmpty(f.before)}),
		"GET /api/snapshots/a1/checks": fwdtest.Const(200, map[string]any{"checks": orEmpty(f.after)}),
		pathsPath:                      fwdtest.Const(200, orPaths(f.paths)),
	}
	for _, a := range diffAreas {
		r["GET /api/diffs/b1/a1/"+a] = fwdtest.Const(200, map[string]any{"count": f.counts[a], "complete": !f.incomplete[a]})
	}
	return r
}

func orConn(m map[string]any) map[string]any {
	if m == nil {
		return conn(false, 10, 0, 0)
	}
	return m
}
func orEmpty(m []map[string]any) []map[string]any {
	if m == nil {
		return []map[string]any{}
	}
	return m
}
func orPaths(m map[string]any) map[string]any {
	if m == nil {
		return pathsBody(false, "EXACT", path("DELIVERED", "PERMITTED"))
	}
	return m
}

const csInput = `{"network_id":"n1","change_set_id":"cs1","connectivity_timeout_seconds":1`

func flow(expect string) string {
	return `,"expectations":[{"src_ip":"10.0.0.1","dst_ip":"10.0.1.1","protocol":"tcp","dst_port":"443","expect":"` + expect + `"}]`
}

func verify(t *testing.T, f changeFix, extra string) (result.Result, *fwdtest.Server) {
	return mustRun(t, "verify-change", f.routes(), csInput+extra+"}")
}

func TestVerifyChangeExpectationMetAndNoRegressionIsOK(t *testing.T) {
	r, _ := verify(t, changeFix{}, flow("delivered"))
	if r.Status != result.OK || r.Confidence != result.Deterministic || r.Context.State != "predicted" {
		t.Fatalf("got %s/%s state %s: %s", r.Status, r.Confidence, r.Context.State, r.Finding)
	}
}

func TestVerifyChangeUnmetExpectationFails(t *testing.T) {
	r, _ := verify(t, changeFix{paths: pathsBody(false, "EXACT", path("DROPPED", "PERMITTED"))}, flow("delivered"))
	if r.Status != result.Failed || !strings.Contains(r.Finding, "expected delivered, observed blocked") || r.NextActions[0] != "investigate-reachability" {
		t.Fatalf("got %s: %s %v", r.Status, r.Finding, r.NextActions)
	}
}

func TestVerifyChangeBlockedExpectationHoldsWhenBlocked(t *testing.T) {
	r, _ := verify(t, changeFix{paths: pathsBody(false, "EXACT", path("DROPPED", "PERMITTED"))}, flow("blocked"))
	if r.Status != result.OK {
		t.Fatalf("status %s", r.Status)
	}
}

func TestVerifyChangeARegressedCheckFailsEvenWithoutExpectations(t *testing.T) {
	r, _ := verify(t, changeFix{before: []map[string]any{chk("c1", "no-telnet", "PASS", 0)}, after: []map[string]any{chk("c1", "no-telnet", "FAIL", 3)}}, "")
	if r.Status != result.Failed || r.Evidence[len(r.Evidence)-1].Type != result.EvPolicy {
		t.Fatalf("got %s: %s", r.Status, r.Finding)
	}
}

func TestVerifyChangeAnAlreadyFailingCheckIsNotARegression(t *testing.T) {
	r, _ := verify(t, changeFix{before: []map[string]any{chk("c1", "x", "FAIL", 3)}, after: []map[string]any{chk("c1", "x", "FAIL", 3)}}, flow("delivered"))
	if r.Status != result.OK {
		t.Fatalf("status %s: %s", r.Status, r.Finding)
	}
}

func TestVerifyChangeMoreViolationsOnAFailingCheckIsARegression(t *testing.T) {
	r, _ := verify(t, changeFix{before: []map[string]any{chk("c1", "x", "FAIL", 1)}, after: []map[string]any{chk("c1", "x", "FAIL", 4)}}, "")
	if r.Status != result.Failed {
		t.Fatalf("status %s", r.Status)
	}
}

func TestVerifyChangeNoExpectationsReportsImpactButDoesNotJudge(t *testing.T) {
	r, _ := verify(t, changeFix{conn: conn(false, 10, 2, 0)}, "")
	if r.Status != result.Unknown || !strings.Contains(strings.Join(r.Limits, " "), "not judged") ||
		!strings.Contains(r.Evidence[0].Summary, "2 newly isolated") {
		t.Fatalf("got %s %v %v", r.Status, r.Limits, r.NextActions)
	}
}

func TestVerifyChangeAnUndeterminedExpectationIsUnknownNotOK(t *testing.T) {
	r, _ := verify(t, changeFix{paths: pathsBody(false, "EXACT")}, flow("blocked"))
	if r.Status != result.Unknown {
		t.Fatalf("status %s", r.Status)
	}
}

func TestVerifyChangePartialConnectivityIsNotEvidence(t *testing.T) {
	r, _ := verify(t, changeFix{conn: conn(true, 0, 0, 0)}, "")
	if !strings.Contains(strings.Join(r.Limits, " "), "did not settle") {
		t.Errorf("limits %v", r.Limits)
	}
	for _, e := range r.Evidence {
		if e.Type == result.EvPredict {
			t.Error("unsettled connectivity was cited as evidence")
		}
	}
}

func TestVerifyChangeZeroPairsComparedIsUnmeasuredNotNoChange(t *testing.T) {
	r, _ := verify(t, changeFix{conn: conn(false, 0, 0, 0)}, "")
	if l := strings.Join(r.Limits, " "); !strings.Contains(l, "zero subnet pairs") || !strings.Contains(l, "unmeasured") || !strings.Contains(l, "never as a pass") {
		t.Errorf("limits %v", r.Limits)
	}
	for _, e := range r.Evidence {
		if e.Type == result.EvPredict {
			t.Error("zero pairs was cited as evidence")
		}
	}
}

func TestVerifyChangeNoPredictionIsUnknownAndPredictIsNotRun(t *testing.T) {
	r, srv := verify(t, changeFix{predicted: []map[string]any{}}, "")
	if r.Status != result.Unknown || !strings.Contains(strings.Join(r.Limits, " "), "run_predict") {
		t.Fatalf("got %s %v", r.Status, r.Limits)
	}
	for _, c := range srv.Calls() {
		if c.Method == "POST" {
			t.Errorf("Predict must be opt-in, but %s %s was sent", c.Method, c.Path)
		}
	}
}

func TestVerifyChangeTwoSnapshotMode(t *testing.T) {
	r, _ := mustRun(t, "verify-change", changeFix{}.routes(),
		`{"network_id":"n1","before_snapshot_id":"b1","after_snapshot_id":"a1","connectivity_timeout_seconds":1`+flow("delivered")+`}`)
	if r.Status != result.OK {
		t.Fatalf("status %s", r.Status)
	}
}

func TestVerifyChangeUnprocessedAfterSnapshotIsUnknownAndNothingIsSearched(t *testing.T) {
	r, srv := mustRun(t, "verify-change", changeFix{afterSt: "UNPROCESSED"}.routes(),
		`{"network_id":"n1","before_snapshot_id":"b1","after_snapshot_id":"a1"}`)
	if r.Status != result.Unknown || srv.Called("GET", "/api/networks/n1/paths") {
		t.Fatalf("status %s", r.Status)
	}
}

func TestVerifyChangeMissingSelectorIsAnErrorResult(t *testing.T) {
	r, _ := mustRun(t, "verify-change", map[string]fwdtest.Handler{}, `{"network_id":"n1"}`)
	if r.Status != result.Error {
		t.Fatalf("status %s", r.Status)
	}
}

func TestVerifyChangeAConnectivityFeatureSwitchedOffIsALimitNotAnError(t *testing.T) {
	f := changeFix{}
	routes := f.routes()
	routes[connPath] = fwdtest.Const(403, map[string]any{"message": "LOCATION_CONNECTIVITY_DIFFS is off for your organization"})
	r, _ := mustRun(t, "verify-change", routes, csInput+flow("delivered")+"}")
	if r.Status != result.OK || !strings.Contains(strings.Join(r.Limits, " "), "LOCATION_CONNECTIVITY_DIFFS is off") {
		t.Fatalf("got %s %v", r.Status, r.Limits)
	}
	for _, e := range r.Evidence {
		if e.Type == result.EvPredict {
			t.Error("a disabled comparison was cited as evidence")
		}
	}
}

func TestVerifyChangeAnUnrelated403OnConnectivityIsNotSwallowedAsAFeatureGate(t *testing.T) {
	routes := changeFix{}.routes()
	routes[connPath] = fwdtest.Const(403, map[string]any{"message": "you may not read this snapshot"})
	_, _, err := runSkill(t, "verify-change", routes, csInput+flow("delivered")+"}")
	if err == nil {
		t.Fatal("a real permission error was reported as an unmeasured limit")
	}
}

func TestVerifyChangeManyRegressionsAreAllCountedButOnlyTheWorstAreCited(t *testing.T) {
	var before, after []map[string]any
	for i := 1; i <= 40; i++ {
		id, name := fmt.Sprint(i), fmt.Sprintf("check-%02d", i)
		before = append(before, chk(id, name, "PASS", 0))
		after = append(after, chk(id, name, "FAIL", i))
	}
	r, _ := verify(t, changeFix{before: before, after: after}, "")
	if r.Status != result.Failed || !strings.HasSuffix(r.Finding, "and 35 more") {
		t.Fatalf("got %s: %s", r.Status, r.Finding)
	}
	cited := 0
	for _, e := range r.Evidence {
		if e.Type == result.EvPolicy {
			cited++
		}
	}
	if cited != 10 || !strings.Contains(strings.Join(r.Limits, " "), "40 checks regressed") {
		t.Errorf("cited %d, limits %v", cited, r.Limits)
	}
}

// view list answers "has this network used Predict": the change sets it holds (id, name, base snapshot, how many devices they edit), an empty list as a real answer (none), and an unlicensed organization as a licence limit.
func TestVerifyChangeListViewListsTheNetworksChangeSets(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/change-sets": fwdtest.Const(200, map[string]any{"changeSets": []any{
			map[string]any{"id": "cs1", "name": "add loopback", "networkId": "n1", "snapshotId": "s1"},
			map[string]any{"id": "cs2", "name": "remove acl", "networkId": "n1", "snapshotId": "s2"},
		}}),
		"GET /api/networks/n1/change-sets/cs1/commits/head": fwdtest.Const(200, map[string]any{"id": "cs1", "commitId": "c", "deviceToChanges": map[string]any{"r1": map[string]any{"commands": "a"}, "r2": map[string]any{"commands": "b"}}}),
		"GET /api/networks/n1/change-sets/cs1/draft":        fwdtest.Const(200, map[string]any{"draft": nil}),
		"GET /api/networks/n1/change-sets/cs2/commits/head": fwdtest.Const(404, map[string]any{"message": "none"}),
		"GET /api/networks/n1/change-sets/cs2/draft":        fwdtest.Const(200, map[string]any{"draft": nil}),
	}
	r, srv := mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"list"}`)
	b := jsonOf(r)
	if writes(srv) != 0 || r.Status != result.OK || !strings.Contains(r.Finding, "2 change set") || !strings.Contains(b, "add loopback") || !strings.Contains(b, `"devices_edited":2`) {
		t.Fatalf("%s %s %s", r.Status, r.Finding, b)
	}
	routes["GET /api/networks/n1/change-sets"] = fwdtest.Const(200, map[string]any{"changeSets": []any{}})
	r, _ = mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"list"}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "No change set") {
		t.Errorf("none is an answer: %s %s", r.Status, r.Finding)
	}
	routes["GET /api/networks/n1/change-sets"] = fwdtest.Const(403, map[string]any{"message": "Unlicensed operation: NetworkOperation.MANAGE_CHANGE_SETS"})
	r, _ = mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"list"}`)
	if r.Status != result.Unknown || !strings.Contains(strings.Join(r.Limits, " "), "licence limit") {
		t.Errorf("an unlicensed org: %s %v", r.Status, r.Limits)
	}
	if _, _, err := runSkill(t, "verify-change", routes, `{"network_id":"n1","view":"list","change_set_id":"cs1"}`); err == nil {
		t.Errorf("list takes no change_set_id")
	}
}

// view scope answers "which of this network can Predict model on this release": the license facet, the BGP families in config that Predict does not model (EVPN, VPLS, multicast, BGP-LS, SR policy, flowspec), the platforms on which
// propagation stops (not in the fully supported list), and whether OSPF is re-propagated (predict_model_ospf). It reads, and judges nothing about a change.
func TestVerifyChangeScopeViewJoinsLicenseFamiliesPlatformsAndFeatures(t *testing.T) {
	routes := cfgRoutes(
		map[string]any{"predict_modeling": true, "predict_model_ospf": false},
		map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{"predict_model_ospf": false})
	routes["GET /api/licenses"] = fwdtest.Const(200, map[string]any{"licenseTier": "NSP", "licenseStatus": "ACTIVE"})
	routes["GET /api/version"] = fwdtest.Const(200, map[string]any{"version": "26.9.4", "release": "26.9.4-02", "build": "b"})
	routes[snapsPath] = ready("s1")
	routes[nqePath] = invNQE(map[string]fwdtest.Handler{
		"router bgp": rowsOf(4,
			map[string]any{"device": "eos1", "os": "EOS", "model": "7050", "family": "ipv4"},
			map[string]any{"device": "eos1", "os": "EOS", "model": "7050", "family": "evpn"},
			map[string]any{"device": "xe1", "os": "IOS_XE", "model": "C9300", "family": "l2vpn vpls"},
			map[string]any{"device": "xe1", "os": "IOS_XE", "model": "C9300", "family": "ipv4 multicast"}),
		"group d as g by d.platform.os": rowsOf(3,
			map[string]any{"os": "ARISTA_EOS", "devices": 2},
			map[string]any{"os": "IOS_XE", "devices": 1},
			map[string]any{"os": "SRX", "devices": 4},
			map[string]any{"os": "MERAKI", "devices": 3}),
	})
	r, srv := mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"scope"}`)
	b := jsonOf(r)
	if writes(srv) != 0 || r.Status != result.OK {
		t.Fatalf("%s %s", r.Status, b)
	}
	var d map[string]any
	for _, e := range r.Evidence {
		if v, ok := e.Detail["scope"].(map[string]any); ok {
			d = v
		}
	}
	if d == nil {
		t.Fatalf("no scope detail: %s", b)
	}
	if d["build"] != "b" || d["version"] != "26.9.4-02" {
		t.Errorf("the scope carries the build id for the field report: %v %v", d["build"], d["version"])
	}
	// 26.9 has no predict_model_isis: the scope says it does not exist, not that it is unreadable
	if !strings.Contains(b, "does not exist in release 26.9") {
		t.Errorf("an absent flag says so: %s", b)
	}
	oos := d["out_of_scope_families"].([]map[string]any)
	got := map[string]string{}
	for _, x := range oos {
		got[fmt.Sprint(x["family"])] = fmt.Sprint(x["devices"])
	}
	if got["evpn"] != "1" || got["vpls"] != "1" || got["multicast"] != "1" || len(got) != 3 {
		t.Errorf("out-of-scope families: %v", got)
	}
	stops := d["propagation_stops"].([]map[string]any)
	if len(stops) != 1 || stops[0]["os"] != "MERAKI" || fmt.Sprint(stops[0]["devices"]) != "3" {
		t.Errorf("an unlisted platform stops propagation: %v", stops)
	}
	lic := d["license"].(map[string]any)
	if lic["predict"] != true {
		t.Errorf("license: %v", lic)
	}
	if d["ospf_repropagated"] != false || d["static_repropagated"] != false {
		t.Errorf("OSPF is off here and static is never re-propagated on this release: %v", d)
	}
	for _, want := range []string{"prediction-scope", "26.9", "read from"} {
		if !strings.Contains(strings.Join(r.Limits, " "), want) {
			t.Errorf("limits should mention %q: %v", want, r.Limits)
		}
	}
	if _, _, err := runSkill(t, "verify-change", routes, `{"network_id":"n1","view":"scope","change_set_id":"cs1"}`); err == nil {
		t.Errorf("scope takes no change_set_id")
	}
}

func TestScopeStatusIsFailedWhenTheLicenseHasNoPredictAndLeadsWithTheLicense(t *testing.T) {
	routes := cfgRoutes(map[string]any{"predict_modeling": true}, map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{})
	routes["GET /api/licenses"] = fwdtest.Const(200, map[string]any{"licenseTier": "NS", "licenseStatus": "ACTIVE"})
	routes[snapsPath] = ready("s1")
	routes[nqePath] = invNQE(map[string]fwdtest.Handler{})
	r, _ := mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"scope"}`)
	if r.Status != result.Failed || !strings.HasPrefix(r.Finding, "Predict fit: the license (tier NS) has no Predict facet") {
		t.Errorf("%s %s", r.Status, r.Finding)
	}
}

// The per-release truth of Predict's flags: scope prints the effective value of every predict_* flag the login can read, and for a flag it cannot read it falls back to the default of the release (Forward's OrgProperty.java, as read by a
// peer session) and says "default for this release, not read from the tenant". OSPF and static re-propagation come from the flags, not from prose.
func TestScopeReadsEveryPredictFlagAndFallsBackToTheReleaseDefault(t *testing.T) {
	routes := cfgRoutes(
		map[string]any{"predict_modeling": true, "predict_model_ospf": true, "predict_model_bgp": true},
		map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{})
	routes["GET /api/version"] = fwdtest.Const(200, map[string]any{"version": "26.10.0", "release": "26.10.0-02", "build": "b"})
	routes["GET /api/licenses"] = fwdtest.Const(200, map[string]any{"licenseTier": "NSP", "licenseStatus": "ACTIVE"})
	routes[snapsPath] = ready("s1")
	routes[nqePath] = invNQE(map[string]fwdtest.Handler{})
	r, _ := mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"scope"}`)
	var d map[string]any
	for _, e := range r.Evidence {
		if v, ok := e.Detail["scope"].(map[string]any); ok {
			d = v
		}
	}
	flags := map[string]map[string]any{}
	for _, f := range d["flags"].([]map[string]any) {
		flags[f["property"].(string)] = f
	}
	if f := flags["predict_model_ospf"]; f["value"] != true || f["source"] != "read from the tenant" {
		t.Errorf("a flag the login can read: %v", f)
	}
	if f := flags["predict_model_cross_protocol"]; f["value"] != true || !strings.Contains(fmt.Sprint(f["source"]), "default for release 26.10, not read from the tenant") {
		t.Errorf("26.10 turns cross-protocol on by default: %v", f)
	}
	if f := flags["predict_model_config"]; f["value"] != false || !strings.Contains(fmt.Sprint(f["source"]), "not read from the tenant") {
		t.Errorf("direct and static modelling is off by default on 26.10: %v", f)
	}
	if d["ospf_repropagated"] != true || d["static_repropagated"] != false {
		t.Errorf("OSPF from its flag, static from predict_model_config: %v %v", d["ospf_repropagated"], d["static_repropagated"])
	}
	if d["release"] != "26.10" || !strings.Contains(strings.Join(r.Limits, " "), "OrgProperty.java") {
		t.Errorf("release %v limits %v", d["release"], r.Limits)
	}
}

// view preflight is the demo checklist in one call: license facet, release, flags, platform support, out-of-scope families, whether received BGP advertisements are collected on the BGP devices (modify and withdraw silently do nothing
// without it), whether the base snapshot is a fresh collection, and the existing change sets.
func TestPreflightJoinsTheDemoChecklist(t *testing.T) {
	routes := cfgRoutes(map[string]any{"predict_modeling": true, "predict_model_ospf": false}, map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{})
	routes["GET /api/version"] = fwdtest.Const(200, map[string]any{"version": "26.9.4", "release": "26.9.4-02", "build": "b"})
	routes["GET /api/licenses"] = fwdtest.Const(200, map[string]any{"licenseTier": "NSP", "licenseStatus": "ACTIVE"})
	routes[snapsPath] = ready("s1")
	routes[nqePath] = invNQE(map[string]fwdtest.Handler{
		"router bgp": rowsOf(3,
			map[string]any{"device": "eos1", "os": "ARISTA_EOS", "model": "7050", "family": "evpn"},
			map[string]any{"device": "eos1", "os": "ARISTA_EOS", "model": "7050", "family": "ipv4"},
			map[string]any{"device": "eos2", "os": "ARISTA_EOS", "model": "7050", "family": "ipv4"}),
		"group d as g by d.platform.os": rowsOf(2, map[string]any{"os": "ARISTA_EOS", "devices": 2}, map[string]any{"os": "F5", "devices": 5}),
	})
	routes["GET /api/networks/n1/classic-devices"] = fwdtest.Const(200, []any{
		map[string]any{"name": "eos1", "host": "h", "collectBgpAdvertisements": true},
		map[string]any{"name": "eos2", "host": "h"},
	})
	routes["GET /api/networks/n1/change-sets"] = fwdtest.Const(200, map[string]any{"changeSets": []any{map[string]any{"id": "cs1", "name": "x", "networkId": "n1", "snapshotId": "s1"}}})
	r, srv := mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"preflight"}`)
	if writes(srv) != 0 || r.Status != result.OK {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	var d map[string]any
	for _, e := range r.Evidence {
		if v, ok := e.Detail["preflight"].(map[string]any); ok {
			d = v
		}
	}
	if d == nil {
		t.Fatalf("no preflight detail: %s", jsonOf(r))
	}
	if d["release"] != "26.9" {
		t.Errorf("release: %v", d["release"])
	}
	adv := d["bgp_advertisement_collection"].(map[string]any)
	if fmt.Sprint(adv["bgp_devices"]) != "2" || fmt.Sprint(adv["collecting_received_advertisements"]) != "1" || !strings.Contains(fmt.Sprint(adv["warning"]), "no warning is shown") {
		t.Errorf("received-advertisement coverage and the silent no-op warning: %v", adv)
	}
	if fmt.Sprint(d["existing_change_sets"]) != "1" {
		t.Errorf("existing change sets: %v", d["existing_change_sets"])
	}
	snap := d["base_snapshot"].(map[string]any)
	if snap["id"] != "s1" {
		t.Errorf("base snapshot: %v", snap)
	}
	if d["scope"] == nil {
		t.Errorf("the scope is part of the preflight: %v", d)
	}
	if !strings.HasPrefix(r.Finding, "Predict preflight:") {
		t.Errorf("finding %q", r.Finding)
	}
	advOf := func(r result.Result) map[string]any {
		for _, e := range r.Evidence {
			if v, ok := e.Detail["preflight"].(map[string]any); ok {
				return v["bgp_advertisement_collection"].(map[string]any)
			}
		}
		return nil
	}
	// the requirements read is refused: the limits say the list is missing, and no list is shown (not an empty one)
	routes["GET /api/networks/n1/bgp-collection-requirements"] = fwdtest.Const(403, map[string]any{"message": "forbidden"})
	r, _ = mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"preflight"}`)
	if adv := advOf(r); !strings.Contains(strings.Join(r.Limits, " "), "BGP collection requirements could not be read") || adv["required_by_network"] != nil {
		t.Errorf("a failed requirements read is said, not shown as none: %v %v", r.Limits, adv)
	}
	// no requirement group: the lists are empty, not null
	routes["GET /api/networks/n1/bgp-collection-requirements"] = fwdtest.Const(200, map[string]any{"requirements": []any{}})
	r, _ = mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"preflight"}`)
	if j := jsonOf(r); !strings.Contains(j, `"required_by_network":[]`) || !strings.Contains(j, `"required_not_collecting":[]`) {
		t.Errorf("no requirement is an empty list: %s", j)
	}
	// the network requires eos2, eos1 and edge9 (out of order, eos2 twice); eos1 collects, eos2 does not, edge9 is no classic device
	routes["GET /api/networks/n1/bgp-collection-requirements"] = fwdtest.Const(200, map[string]any{"requirements": []any{map[string]any{"devices": []any{"eos2", "eos1"}}, map[string]any{"devices": []any{"edge9", "eos2"}}}})
	r, _ = mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"preflight"}`)
	adv = advOf(r)
	if fmt.Sprint(adv["required_by_network"]) != "[edge9 eos1 eos2]" || fmt.Sprint(adv["required_not_collecting"]) != "[eos2]" || fmt.Sprint(adv["required_not_checked"]) != "[edge9]" {
		t.Errorf("required devices sorted once each, the one with collection off, and the one not checked: %v", adv)
	}
	lim := strings.Join(r.Limits, " ")
	if !strings.Contains(lim, "1 device(s) the network requires to collect BGP advertisements have collection off") || !strings.Contains(lim, "so whether they collect was not checked") {
		t.Errorf("both cases are limits: %v", r.Limits)
	}
	// the classic devices cannot be read: the required list is shown, the not-collecting list is not computed
	routes["GET /api/networks/n1/classic-devices"] = fwdtest.Const(403, map[string]any{"message": "forbidden"})
	r, _ = mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"preflight"}`)
	if adv := advOf(r); adv["required_by_network"] == nil || adv["required_not_collecting"] != nil {
		t.Errorf("without the classic devices, collection is not judged: %v", adv)
	}
}

// view paths is the GUI's "view interesting paths": for a snapshot (or a change set's predicted snapshot) it ranks the paths that go through firewalls or load balancers, and says what they pass through. It
// sorts by score itself (Forward joins its slices unordered), decodes each path query, and for a snapshot without advanced reachability it says so and names the skill that starts it.
func TestVerifyChangePathsViewRanksFirewallAndLoadBalancerPaths(t *testing.T) {
	enc := func(q string) string { return "/n1/s1/paths/" + base64.RawURLEncoding.EncodeToString([]byte(q)) }
	routes := map[string]fwdtest.Handler{
		snapsPath: ready("s1"),
		"GET /api/networks/n1/interesting-path-queries": func(r *http.Request, _ []byte) (int, any) {
			if r.URL.Query().Get("snapshotId") != "s1" {
				t.Errorf("snapshotId: %v", r.URL.Query())
			}
			switch r.URL.Query().Get("type") {
			case "FIREWALL":
				return 200, map[string]any{"interestingPathQueries": []any{
					map[string]any{"id": "a", "score": 7, "searchPath": enc(`{"srcIp":"10.0.0.1"}`), "samplePathGroup": map[string]any{"tags": []any{"IN_ACL"}, "numHops": 3, "flowType": "VALID"}},
					map[string]any{"id": "b", "score": 31, "searchPath": enc(`{"srcIp":"10.0.0.2"}`), "samplePathGroup": map[string]any{"tags": []any{"XLATE_DNAT", "IN_ACL"}, "numHops": 5, "flowType": "VALID"}},
				}, "duration": 1200}
			case "LOADBALANCER":
				return 200, map[string]any{"interestingPathQueries": []any{}}
			}
			t.Errorf("unexpected type %q", r.URL.Query().Get("type"))
			return 400, nil
		},
	}
	r, srv := mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"paths","connectivity_timeout_seconds":30}`)
	b := jsonOf(r)
	if r.Status != result.OK || writes(srv) != 0 {
		t.Fatalf("%s %s", r.Status, b)
	}
	// the higher score first, with its tags, hops and the decoded path query
	i31, i7 := strings.Index(b, `"score":31`), strings.Index(b, `"score":7`)
	if i31 < 0 || i7 < 0 || i31 > i7 || !strings.Contains(b, "XLATE_DNAT") || !strings.Contains(b, "10.0.0.2") || !strings.Contains(b, `"loadbalancer"`) {
		t.Errorf("ranking or content: %s", b)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "sampled") {
		t.Errorf("the result says the paths are sampled, not exhaustive: %v", r.Limits)
	}

	// a snapshot without advanced reachability: unknown, naming the way to start it
	routes["GET /api/networks/n1/interesting-path-queries"] = fwdtest.Const(400, map[string]any{"message": "Advanced reachability NOT_TRIGGERED for snapshot s1."})
	r, _ = mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"paths"}`)
	if r.Status != result.Unknown || !strings.Contains(strings.Join(r.NextActions, " "), "edit-advanced-reachability") {
		t.Errorf("not processed: %s %v", r.Status, r.NextActions)
	}
	// an unlicensed network
	routes["GET /api/networks/n1/interesting-path-queries"] = fwdtest.Const(403, map[string]any{"message": "unlicensed-operation"})
	r, _ = mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"paths"}`)
	if r.Status != result.Unknown || !strings.Contains(strings.Join(r.Limits, " "), "licen") {
		t.Errorf("unlicensed: %s %v", r.Status, r.Limits)
	}
}

// view list gives each change set's predicted snapshots (id and state, newest first) so a caller can go from a change set's name to the snapshot it produced without describing every set; a set whose predictions could not be read says so.
func TestVerifyChangeListViewCarriesEachChangeSetsPredictedSnapshots(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/change-sets": fwdtest.Const(200, map[string]any{"changeSets": []any{
			map[string]any{"id": "cs1", "name": "CHG-1", "snapshotId": "s1"},
			map[string]any{"id": "cs2", "name": "CHG-2", "snapshotId": "s1"},
		}}),
		"GET /api/networks/n1/change-sets/cs1/predicted-snapshots": fwdtest.Const(200, map[string]any{"predictedSnapshots": []any{
			map[string]any{"id": "p1", "state": "PROCESSED", "createdAt": "2026-10-01T00:00:00.000Z"},
			map[string]any{"id": "p2", "state": "PROCESSED", "createdAt": "2026-10-02T00:00:00.000Z"}}}),
		"GET /api/networks/n1/change-sets/cs2/predicted-snapshots": fwdtest.Const(500, map[string]any{"message": "boom"}),
	}
	r, _ := mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"list"}`)
	b := jsonOf(r)
	if r.Status != result.OK || !strings.Contains(b, `"id":"p2"`) || strings.Index(b, `"id":"p2"`) > strings.Index(b, `"id":"p1"`) || !strings.Contains(b, "predictions_unreadable") {
		t.Fatalf("predicted snapshots: %s %s", r.Status, b)
	}
}

// SD-WAN devices are named in the scope (and so in the preflight): Viptela devices by OS, and IOS-XE devices that collected SD-WAN (OMP) output, with whether the OMP flag is on. Predict does not recompute the SD-WAN overlay without it.
func TestScopeCountsSDWANDevicesAndSaysWhetherTheOMPFlagIsOn(t *testing.T) {
	build := func(omp any, xe int) map[string]fwdtest.Handler {
		eff := map[string]any{"predict_modeling": true}
		if omp != nil {
			eff["predict_model_omp"] = omp
		}
		routes := cfgRoutes(eff, map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{})
		routes["GET /api/licenses"] = fwdtest.Const(200, map[string]any{"licenseTier": "NSP", "licenseStatus": "ACTIVE"})
		routes["GET /api/version"] = fwdtest.Const(200, map[string]any{"version": "26.10.0", "release": "26.10.0-10", "build": "b"})
		routes[snapsPath] = ready("s1")
		routes[nqePath] = invNQE(map[string]fwdtest.Handler{
			"group d as g by d.platform.os": rowsOf(2,
				map[string]any{"os": "VIPTELA", "devices": 2},
				map[string]any{"os": "IOS_XE", "devices": 5}),
			"SDWAN_OMP_ROUTES": rowsOf(xe, rowN(xe)...),
		})
		return routes
	}
	scope := func(routes map[string]fwdtest.Handler, view string) map[string]any {
		r, _ := mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"`+view+`"}`)
		for _, e := range r.Evidence {
			if v, ok := e.Detail["scope"].(map[string]any); ok {
				return v
			}
			if pf, ok := e.Detail["preflight"].(map[string]any); ok {
				if v, ok := pf["scope"].(map[string]any); ok {
					return v
				}
			}
		}
		t.Fatalf("no scope detail in %s: %s", view, jsonOf(r))
		return nil
	}
	for _, view := range []string{"scope", "preflight"} {
		d := scope(build(false, 3), view)
		sd, ok := d["sdwan"].(map[string]any)
		if !ok || fmt.Sprint(sd["viptela_devices"]) != "2" || fmt.Sprint(sd["ios_xe_sdwan_devices"]) != "3" || fmt.Sprint(sd["devices"]) != "5" || sd["predict_model_omp"] != false {
			t.Fatalf("%s: sdwan detail %v", view, d["sdwan"])
		}
	}
	r, _ := mustRun(t, "verify-change", build(false, 3), `{"network_id":"n1","view":"scope"}`)
	if !strings.Contains(r.Finding, "5 SD-WAN device(s)") || !strings.Contains(r.Finding, "PREDICT_MODEL_OMP is off") {
		t.Errorf("finding: %s", r.Finding)
	}
	if strings.Contains(r.Finding, "on 26.9") || !strings.Contains(r.Finding, "26.10") {
		t.Errorf("the finding names the release read from the tenant, not a fixed one: %s", r.Finding)
	}
	if d := scope(build(false, 3), "scope"); d["predict_model_omp"] != false {
		t.Errorf("the scope carries the flag at the top level too, agreeing with sdwan: %v", d["predict_model_omp"])
	}
	r, _ = mustRun(t, "verify-change", build(true, 3), `{"network_id":"n1","view":"scope"}`)
	if !strings.Contains(r.Finding, "SD-WAN") || !strings.Contains(r.Finding, "PREDICT_MODEL_OMP is on") || !strings.Contains(r.Finding, "not validated") {
		t.Errorf("flag on: %s", r.Finding)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "SDWAN_OMP_ROUTES") {
		t.Errorf("the IOS-XE detection method is a limit: %v", r.Limits)
	}
	// no SD-WAN device, no note
	routes := build(nil, 0)
	routes[nqePath] = invNQE(map[string]fwdtest.Handler{"group d as g by d.platform.os": rowsOf(1, map[string]any{"os": "EOS", "devices": 2})})
	r, _ = mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"scope"}`)
	if strings.Contains(r.Finding, "SD-WAN") {
		t.Errorf("a network without SD-WAN devices says nothing about SD-WAN: %s", r.Finding)
	}
}

func rowN(n int) []map[string]any {
	out := make([]map[string]any, n)
	for i := range out {
		out[i] = map[string]any{"device": fmt.Sprintf("xe%d", i+1)}
	}
	return out
}

// View flows searches each flow on both snapshots and says what changed: the outcome, the paths only before and only after.
func TestVerifyChangeFlowsComparesEachFlowOnBothSnapshots(t *testing.T) {
	routes := changeFix{}.routes()
	routes[pathsPath] = func(r *http.Request, _ []byte) (int, any) {
		if r.URL.Query().Get("snapshotId") == "b1" {
			return 200, pathsBody(false, "EXACT", path("DELIVERED", "PERMITTED", hop("r1", "e0", "e1"), hop("r2", "e2", "")))
		}
		return 200, pathsBody(false, "EXACT", path("DROPPED", "", hop("r1", "e0", "")))
	}
	r, _ := mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"flows","before_snapshot_id":"b1","after_snapshot_id":"a1"`+flow("delivered")+`}`)
	if r.Status != result.OK || len(r.Evidence) != 1 {
		t.Fatalf("status %s, %d evidence: %s", r.Status, len(r.Evidence), r.Finding)
	}
	d := r.Evidence[0].Detail
	diff, _ := d["diff"].(map[string]any)
	ob, _ := diff["only_before"].([]map[string]any)
	oa, _ := diff["only_after"].([]map[string]any)
	if d["change"] != "outcome_changed" || d["verdict_before"] != "delivered" || d["verdict_after"] != "blocked" || len(ob) != 1 || len(oa) != 1 || diff["same"] != 0 {
		t.Errorf("flow diff = %v", jsonOf(r))
	}
	if !strings.Contains(r.Finding, "1 changed outcome") {
		t.Errorf("finding %q", r.Finding)
	}
	if _, _, err := runSkill(t, "verify-change", routes, `{"network_id":"n1","view":"flows","before_snapshot_id":"b1","after_snapshot_id":"a1"}`); err == nil {
		t.Errorf("flows without expectations was accepted")
	}
}

// expect is optional in the schema (view flows ignores it) but view verify still needs it, and refuses before any request.
func TestVerifyChangeVerifyStillNeedsExpect(t *testing.T) {
	_, srv, err := runSkill(t, "verify-change", changeFix{}.routes(), `{"network_id":"n1","before_snapshot_id":"b1","after_snapshot_id":"a1","expectations":[{"dst_ip":"10.0.1.1"}]}`)
	if err == nil || !strings.Contains(err.Error(), "expect is required") || len(srv.Calls()) != 0 {
		t.Fatalf("err %v, %d calls", err, len(srv.Calls()))
	}
}

// A network-limited session cannot read the whole property map (GET /api/config). The preflight then reads each Predict flag on its own, and a flag it reads is still "read from the tenant", spelled exactly
// so (a peer's runner accepts a flag only when every entry carries that source).
func TestScopeReadsThePredictFlagsOneAtATimeWhenTheWholeMapIsRefused(t *testing.T) {
	routes := cfgRoutes(nil, nil, nil, nil, nil) // every whole-map view answers 403
	routes["GET /api/version"] = fwdtest.Const(200, map[string]any{"version": "26.11.0", "release": "26.11.0-14", "build": "b"})
	routes["GET /api/licenses"] = fwdtest.Const(200, map[string]any{"licenseTier": "NSP", "licenseStatus": "ACTIVE"})
	routes[snapsPath] = ready("s1")
	routes[nqePath] = invNQE(map[string]fwdtest.Handler{})
	var asked []string
	one := func(name string, v any, status int) {
		routes["GET /api/config/"+name] = func(*http.Request, []byte) (int, any) {
			asked = append(asked, name)
			if status != 200 {
				return status, map[string]any{"message": "boom"}
			}
			return 200, map[string]any{strings.ToLower(name): v}
		}
	}
	one("PREDICT_MODELING", true, 200)
	one("PREDICT_MODEL_CONFIG", true, 200)
	one("PREDICT_MODEL_OSPF", false, 200)
	one("PREDICT_MODEL_BGP", nil, 400) // a flag that cannot be read falls back to the release default and says so
	r, _ := mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"scope"}`)
	var d map[string]any
	for _, e := range r.Evidence {
		if v, ok := e.Detail["scope"].(map[string]any); ok {
			d = v
		}
	}
	flags := map[string]map[string]any{}
	for _, f := range d["flags"].([]map[string]any) {
		flags[f["property"].(string)] = f
	}
	if f := flags["predict_model_config"]; f["value"] != true || f["source"] != "read from the tenant" {
		t.Errorf("a flag read on its own is read from the tenant, verbatim: %v", f)
	}
	if f := flags["predict_model_ospf"]; f["value"] != false || f["source"] != "read from the tenant" {
		t.Errorf("an off flag is read too: %v", f)
	}
	if f := flags["predict_model_bgp"]; !strings.Contains(fmt.Sprint(f["source"]), "not read from the tenant") {
		t.Errorf("a flag that could not be read is a release default, not the tenant's value: %v", f)
	}
	lim := strings.Join(r.Limits, " ")
	if !strings.Contains(lim, "each Predict flag was read on its own") || !strings.Contains(lim, "6 Predict flag(s) could not be read from the tenant (") || !strings.Contains(lim, "predict_model_bgp") {
		t.Errorf("limits: %v", r.Limits)
	}
	if len(asked) < 4 {
		t.Errorf("each flag is asked for on its own: %v", asked)
	}
	// when the whole map IS readable, no per-flag request is made
	asked = nil
	routes2 := cfgRoutes(map[string]any{"predict_modeling": true}, map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{})
	routes2["GET /api/version"] = routes["GET /api/version"]
	routes2["GET /api/licenses"] = routes["GET /api/licenses"]
	routes2[snapsPath], routes2[nqePath] = routes[snapsPath], routes[nqePath]
	routes2["GET /api/config/PREDICT_MODELING"] = routes["GET /api/config/PREDICT_MODELING"]
	mustRun(t, "verify-change", routes2, `{"network_id":"n1","view":"scope"}`)
	if len(asked) != 0 {
		t.Errorf("the whole map was readable: no single-property read is needed: %v", asked)
	}
}

func TestPredictFlagNamesAreTheGuardsAllowList(t *testing.T) {
	up := map[string]bool{}
	for _, n := range skills.PredictFlagOrder() {
		up[strings.ToUpper(n)] = true
	}
	have := map[string]bool{}
	for _, n := range fwd.PredictFlagProperties() {
		have[n] = true
	}
	for n := range up {
		if !have[n] {
			t.Errorf("the preflight reads %s, which the guard would refuse", n)
		}
	}
	for n := range have {
		if !up[n] {
			t.Errorf("the guard allows %s, which the preflight does not read: an allowance nothing uses", n)
		}
	}
}

// Every single-property read fails: the flags show the release default, the limit names them and why, and it does not claim they were read on their own.
func TestScopeSaysWhichPredictFlagsCouldNotBeReadOnTheirOwnEither(t *testing.T) {
	routes := cfgRoutes(nil, nil, nil, nil, nil)
	routes["GET /api/version"] = fwdtest.Const(200, map[string]any{"version": "26.11.0", "release": "26.11.0-14", "build": "b"})
	routes["GET /api/licenses"] = fwdtest.Const(200, map[string]any{"licenseTier": "NSP", "licenseStatus": "ACTIVE"})
	routes[snapsPath] = ready("s1")
	routes[nqePath] = invNQE(map[string]fwdtest.Handler{})
	for _, n := range fwd.PredictFlagProperties() {
		routes["GET /api/config/"+n] = fwdtest.Const(403, map[string]any{"message": "forbidden by test"})
	}
	r, _ := mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"scope"}`)
	lim := strings.Join(r.Limits, " ")
	if strings.Contains(lim, "each Predict flag was read on its own") || !strings.Contains(lim, "9 Predict flag(s) could not be read from the tenant (") || !strings.Contains(lim, "forbidden by test") || !strings.Contains(lim, "show the release default") {
		t.Errorf("limits: %v", r.Limits)
	}
	// a property this Forward does not define is not an error: its row says it does not exist
	routes["GET /api/config/PREDICT_MODEL_ISIS"] = fwdtest.Const(400, map[string]any{"message": "No enum constant com.forwardnetworks.cv.config.OrgProperty.PREDICT_MODEL_ISIS"})
	r, _ = mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"scope"}`)
	if lim := strings.Join(r.Limits, " "); !strings.Contains(lim, "8 Predict flag(s) could not be read") || strings.Contains(lim, "predict_model_isis") {
		t.Errorf("an unknown property is not counted as a failed read: %v", r.Limits)
	}
}

// With the network guard on, the whole map is refused by the guard itself and the single reads pass it: one run proves both.
func TestScopeUnderTheNetworkGuardReadsThePredictFlagsOneAtATime(t *testing.T) {
	routes := cfgRoutes(map[string]any{"predict_modeling": false}, nil, nil, nil, nil) // a whole-map read would say false: it must never be used
	routes["GET /api/version"] = fwdtest.Const(200, map[string]any{"version": "26.11.0", "release": "26.11.0-14", "build": "b"})
	routes["GET /api/licenses"] = fwdtest.Const(200, map[string]any{"licenseTier": "NSP", "licenseStatus": "ACTIVE"})
	routes["GET /api/networks/n1/snapshots"] = ready("s1")
	routes[nqePath] = invNQE(map[string]fwdtest.Handler{})
	routes["GET /api/config/PREDICT_MODEL_CONFIG"] = fwdtest.Const(200, map[string]any{"predict_model_config": true})
	sess, srv := fwdtest.NewWith(t, routes, func(c *fwd.Config) { c.AllowNetworks = []string{"n1"} })
	r, err := skills.Run(context.Background(), "verify-change", sess, json.RawMessage(`{"network_id":"n1","view":"scope"}`))
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	for _, e := range r.Evidence {
		if v, ok := e.Detail["scope"].(map[string]any); ok {
			d = v
		}
	}
	got := map[string]any{}
	for _, f := range d["flags"].([]map[string]any) {
		if f["property"] == "predict_model_config" {
			got = f
		}
	}
	if got["value"] != true || got["source"] != "read from the tenant" {
		t.Errorf("the flag is read on its own under the guard: %v", got)
	}
	for _, c := range srv.Calls() {
		if c.Path == "/api/config" {
			t.Errorf("the whole map must never reach Forward from a guarded session")
		}
	}
}

// A readable but empty property map is an answer (every flag takes its release default); it is not a refusal, so no per-flag read is made.
func TestScopeDoesNotReadFlagsOneAtATimeWhenTheWholeMapIsReadableButEmpty(t *testing.T) {
	routes := cfgRoutes(map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{})
	routes["GET /api/version"] = fwdtest.Const(200, map[string]any{"version": "26.11.0", "release": "26.11.0-14", "build": "b"})
	routes["GET /api/licenses"] = fwdtest.Const(200, map[string]any{"licenseTier": "NSP", "licenseStatus": "ACTIVE"})
	routes[snapsPath] = ready("s1")
	routes[nqePath] = invNQE(map[string]fwdtest.Handler{})
	asked := 0
	for _, n := range fwd.PredictFlagProperties() {
		routes["GET /api/config/"+n] = func(*http.Request, []byte) (int, any) { asked++; return 200, map[string]any{} }
	}
	r, _ := mustRun(t, "verify-change", routes, `{"network_id":"n1","view":"scope"}`)
	if asked != 0 || strings.Contains(strings.Join(r.Limits, " "), "read on its own") {
		t.Errorf("an empty map is not a refusal: %d per-flag reads, %v", asked, r.Limits)
	}
}
