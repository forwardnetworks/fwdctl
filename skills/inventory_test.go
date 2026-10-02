package skills_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

// invNQE answers by the shape of the query: it returns rows for whichever query text contains the marker.
func invNQE(answers map[string]fwdtest.Handler) fwdtest.Handler {
	return func(r *http.Request, b []byte) (int, any) {
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		q, _ := body["query"].(string)
		for marker, h := range answers {
			if strings.Contains(q, marker) {
				return h(r, b)
			}
		}
		return 200, map[string]any{"items": []any{}, "totalNumItems": 0}
	}
}

func rowsOf(total int, rows ...map[string]any) fwdtest.Handler {
	return fwdtest.Const(200, map[string]any{"items": rows, "totalNumItems": total})
}

func inv(t *testing.T, answers map[string]fwdtest.Handler, snaps fwdtest.Handler, in string) (result.Result, *fwdtest.Server) {
	if snaps == nil {
		snaps = ready("s1")
	}
	return mustRun(t, "inspect-inventory", map[string]fwdtest.Handler{snapsPath: snaps, nqePath: invNQE(answers)}, in)
}

func lastNQEBody(srv *fwdtest.Server) map[string]any {
	var b map[string]any
	for _, c := range srv.Calls() {
		if c.Path == "/api/nqe" {
			b = c.Body
		}
	}
	return b
}

func TestInventoryEachKindSendsItsFiltersAsParametersNeverInTheQueryText(t *testing.T) {
	cases := []struct {
		kind, marker string
		in           string
		want         map[string]any
	}{
		{"devices", "platform.osVersion", `"device":"r1"`, map[string]any{"deviceName": "r1"}},
		{"interfaces", "interface.ethernet.speedMbps", `"device":"r1","name":"ge-0/0/0"`, map[string]any{"deviceName": "r1", "ifaceName": "ge-0/0/0"}},
		{"vlans", "device.vlans", `"name":"users"`, map[string]any{"deviceName": "", "vlanName": "users"}},
		{"vrfs", "device.networkInstances", `"device":"r1","name":"DEVZONE1"`, map[string]any{"deviceName": "r1", "vrfName": "DEVZONE1"}},
		{"hosts", "device.hosts", `"device":"sw1"`, map[string]any{"deviceName": "sw1", "hostName": ""}},
		{"cloud", "network.cloudAccounts", `"account":"AWS","name":"prod"`, map[string]any{"accountName": "AWS", "vpcName": "prod"}},
		{"cloud_routes", "vpc.routeTables", `"account":"AWS","name":"prod"`, map[string]any{"accountName": "AWS", "vpcName": "prod"}},
		{"cloud_security", "vpc.securityGroups", `"account":"AWS","name":"prod"`, map[string]any{"accountName": "AWS", "vpcName": "prod"}},
		{"cloud_gateways", "vpc.vpcPeerings", `"account":"AWS","name":"prod"`, map[string]any{"accountName": "AWS", "vpcName": "prod"}},
	}
	for _, c := range cases {
		_, srv := inv(t, map[string]fwdtest.Handler{c.marker: rowsOf(1, map[string]any{"x": 1})}, nil,
			`{"network_id":"n1","kind":"`+c.kind+`",`+c.in+`}`)
		body := lastNQEBody(srv)
		params, _ := body["parameters"].(map[string]any)
		for k, v := range c.want {
			if params[k] != v {
				t.Errorf("%s: parameter %s = %v, want %v", c.kind, k, params[k], v)
			}
		}
		q, _ := body["query"].(string)
		for _, v := range c.want {
			if s, _ := v.(string); s != "" && strings.Contains(q, s) {
				t.Errorf("%s: %q was spliced into the query text", c.kind, s)
			}
		}
	}
}

func TestInventoryRowsAreOKWithTheTotalAndTheWindow(t *testing.T) {
	r, _ := inv(t, map[string]fwdtest.Handler{"interface.ethernet": rowsOf(120, map[string]any{"Device": "r1", "Interface": "e0"}, map[string]any{"Device": "r1", "Interface": "e1"})}, nil,
		`{"network_id":"n1","kind":"interfaces","limit":2}`)
	if r.Status != result.OK || r.Confidence != result.Deterministic {
		t.Fatalf("got %s/%s", r.Status, r.Confidence)
	}
	d := r.Evidence[0].Detail
	if d["total"] != int64(120) || d["returned"] != 2 || len(d["rows"].([]map[string]any)) != 2 {
		t.Errorf("detail %v", d)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "rows 1-2 shown. Page with offset=2.") {
		t.Errorf("limits %v", r.Limits)
	}
}

func TestInventoryPagingSendsTheOffsetAndLimit(t *testing.T) {
	_, srv := inv(t, map[string]fwdtest.Handler{"device.hosts": rowsOf(600, map[string]any{"Host": "h"})}, nil,
		`{"network_id":"n1","kind":"hosts","limit":25,"offset":50}`)
	opts := lastNQEBody(srv)["queryOptions"].(map[string]any)
	if opts["limit"] != float64(25) || opts["offset"] != float64(50) {
		t.Errorf("options %v", opts)
	}
}

func TestInventoryTheLimitIsCapped(t *testing.T) {
	_, srv := inv(t, map[string]fwdtest.Handler{"device.hosts": rowsOf(1, map[string]any{"Host": "h"})}, nil,
		`{"network_id":"n1","kind":"hosts","limit":999999}`)
	if opts := lastNQEBody(srv)["queryOptions"].(map[string]any); opts["limit"] != float64(200) {
		t.Errorf("limit %v, want the cap of 200", opts["limit"])
	}
}

func TestInventoryZeroRowsIsUnknownNeverAnEmptyAnswer(t *testing.T) {
	r, _ := inv(t, nil, nil, `{"network_id":"n1","kind":"vlans","device":"atl-core-pe01"}`)
	if r.Status != result.Unknown || !strings.Contains(strings.Join(r.Limits, " "), "matched exactly") {
		t.Fatalf("got %s %v", r.Status, r.Limits)
	}
}

func TestInventoryAnOffsetPastTheEndIsSaidSo(t *testing.T) {
	r, _ := inv(t, map[string]fwdtest.Handler{"device.hosts": fwdtest.Const(200, map[string]any{"items": []any{}, "totalNumItems": 30})}, nil,
		`{"network_id":"n1","kind":"hosts","offset":500}`)
	if r.Status != result.Unknown || !strings.Contains(strings.Join(r.Limits, " "), "past the end of the 30") {
		t.Fatalf("got %s %v", r.Status, r.Limits)
	}
}

func TestInventorySummaryCombinesCountsVendorsAndTypes(t *testing.T) {
	r, _ := inv(t, map[string]fwdtest.Handler{
		"Cloud accounts":              rowsOf(1, map[string]any{"Devices": 151, "Interfaces": 2391, "VLANs": 869, "VRFs": 275, "Hosts": 587, "Cloud accounts": 3}),
		"platform.vendor as vendor":   rowsOf(2, map[string]any{"Vendor": "ARISTA", "Devices": 86}, map[string]any{"Vendor": "CISCO", "Devices": 32}),
		"platform.deviceType as type": rowsOf(1, map[string]any{"Type": "SWITCH", "Devices": 100}),
	}, nil, `{"network_id":"n1","kind":"summary"}`)
	if r.Status != result.OK || r.Finding != "151 devices across 2 vendors" {
		t.Fatalf("got %s: %s", r.Status, r.Finding)
	}
	d := r.Evidence[0].Detail
	if d["counts"].(map[string]any)["Interfaces"] != float64(2391) || len(d["by_vendor"].([]map[string]any)) != 2 || len(d["by_device_type"].([]map[string]any)) != 1 {
		t.Errorf("detail %v", d)
	}
	if r.Evidence[0].Source.SnapshotID == nil {
		t.Error("the summary does not cite its snapshot")
	}
}

func TestInventoryAnEmptyNetworkSummaryIsUnknownNotAllZeroes(t *testing.T) {
	r, _ := inv(t, map[string]fwdtest.Handler{"Cloud accounts": rowsOf(1, map[string]any{"Devices": 0, "Interfaces": 0})}, nil, `{"network_id":"n1","kind":"summary"}`)
	if r.Status != result.Unknown || !strings.Contains(r.Finding, "no devices") {
		t.Fatalf("got %s: %s", r.Status, r.Finding)
	}
}

func TestInventoryUnprocessedSnapshotRunsNothing(t *testing.T) {
	snaps := fwdtest.Snapshots(fwdtest.Snap("s1", "UNPROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z"))
	r, srv := inv(t, nil, snaps, `{"network_id":"n1","kind":"devices"}`)
	if r.Status != result.Unknown || srv.Called("POST", "/api/nqe") {
		t.Fatalf("status %s", r.Status)
	}
}

func TestInventoryAPredictedSnapshotIsFlagged(t *testing.T) {
	snaps := fwdtest.Snapshots(fwdtest.Snap("p1", "PROCESSED", "PREDICT", "2026-09-05T00:00:00.000Z"))
	r, _ := inv(t, map[string]fwdtest.Handler{"platform.osVersion": rowsOf(1, map[string]any{"Device": "r1"})}, snaps,
		`{"network_id":"n1","kind":"devices","snapshot_id":"p1"}`)
	if !strings.Contains(strings.Join(r.Limits, " "), "prediction, not collected state") {
		t.Errorf("limits %v", r.Limits)
	}
}

func TestInventoryABadKindIsRejectedBeforeAnyCall(t *testing.T) {
	_, srv, err := runSkill(t, "inspect-inventory", map[string]fwdtest.Handler{}, `{"network_id":"n1","kind":"bogus"}`)
	if err == nil || len(srv.Calls()) != 0 {
		t.Fatalf("err %v calls %d", err, len(srv.Calls()))
	}
}

func TestInventoryCloudAccountsShowsWhichAccountWasNotCollected(t *testing.T) {
	r, _ := inv(t, map[string]fwdtest.Handler{"Collected: account.collected": rowsOf(2,
		map[string]any{"Account": "a1", "Cloud": "AWS", "Collected": true, "VPCs": 3, "Instances": 40},
		map[string]any{"Account": "a2", "Cloud": "AWS", "Collected": false, "VPCs": 0, "Instances": 0})}, nil, `{"network_id":"n1","kind":"cloud_accounts"}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "1 account(s) were NOT collected") || !strings.Contains(strings.Join(r.Limits, " "), "not because the account is empty") {
		t.Fatalf("%s %s %v", r.Status, r.Finding, r.Limits)
	}
	if !strings.Contains(strings.Join(r.NextActions, " "), "investigate-collection-failure") {
		t.Errorf("next actions %v", r.NextActions)
	}
}

func TestInventoryDevicesNameIsACaseInsensitiveSubstringAndNoOtherKindTakesAFilterItIgnores(t *testing.T) {
	_, srv := inv(t, map[string]fwdtest.Handler{"platform.osVersion": rowsOf(1, map[string]any{"Device": "sjc-bldg2-fw01"})}, nil, `{"network_id":"n1","kind":"devices","name":"BLDG2-fw"}`)
	params, _ := lastNQEBody(srv)["parameters"].(map[string]any)
	if params["nameGlob"] != "*bldg2-fw*" || params["deviceName"] != "" {
		t.Errorf("name is a lower-cased substring glob parameter: %v", params)
	}
	for _, bad := range []string{
		`{"network_id":"n1","kind":"devices","name":"fw*"}`,        // a pattern, not a substring
		`{"network_id":"n1","kind":"summary","device":"r1"}`,       // summary takes no filter
		`{"network_id":"n1","kind":"cloud_accounts","name":"vpc"}`, // accounts take account only
		`{"network_id":"n1","kind":"routes","account":"a"}`,
	} {
		if _, srv, err := runSkill(t, "inspect-inventory", map[string]fwdtest.Handler{}, bad); err == nil || len(srv.Calls()) != 0 {
			t.Errorf("%s: a filter the kind does not take is refused before any call (err %v, calls %d)", bad, err, len(srv.Calls()))
		}
	}
}

func TestInventoryInterfacesReadsSVIAddressesAndVRFs(t *testing.T) {
	_, srv := inv(t, map[string]fwdtest.Handler{"Interface: interface.name": rowsOf(1, map[string]any{"Interface": "vlan101", "IPv4 addresses": []string{"10.20.101.2"}, "VRFs": []string{"ENG"}})}, nil, `{"network_id":"n1","kind":"interfaces"}`)
	q, _ := lastNQEBody(srv)["query"].(string)
	if !strings.Contains(q, "routedVlan") || !strings.Contains(q, "VRFs:") {
		t.Errorf("the interfaces query must read routed-VLAN (SVI) addresses and the VRFs: %s", q)
	}
}

func TestInventoryIPOwnerFindsAnSVIAndAnFHRPAddressWithTheirVRFAndSaysWhenAClassWasNotRead(t *testing.T) {
	routes := map[string]fwdtest.Handler{snapsPath: ready("s1"), nqePath: func(_ *http.Request, body []byte) (int, any) {
		q := string(body)
		rows := func(r ...map[string]any) (int, any) { return 200, map[string]any{"items": r, "totalNumItems": len(r)} }
		switch {
		case strings.Contains(q, "fhrpAddresses"):
			return rows(map[string]any{"device": "core1", "iface": "vlan101", "sub": "", "vrf": "ENG", "ip": "10.20.101.1", "prefixLength": 24})
		case strings.Contains(q, "routedVlan.ipv4.addresses"):
			return rows(map[string]any{"device": "core1", "iface": "vlan101", "sub": "", "vrf": "ENG", "ip": "10.20.101.2", "prefixLength": 24})
		case strings.Contains(q, "sub.ipv4.addresses"):
			return rows(map[string]any{"device": "core1", "iface": "e1", "sub": "e1", "vrf": "default", "ip": "10.0.0.1", "prefixLength": 24})
		}
		return 400, map[string]any{"message": "unexpected query"}
	}}
	r, _ := mustRun(t, "inspect-inventory", routes, `{"network_id":"n1","kind":"ip_owner","ips":["10.20.101.2","10.20.101.1"]}`)
	rowsOut := r.Evidence[0].Detail["addresses"].([]map[string]any)
	for i, wantKind := range []string{"routed VLAN (SVI) interface", "FHRP virtual address"} {
		owner := rowsOut[i]["owner"].([]map[string]any)[0]
		if owner["kind"] != wantKind || owner["vrf"] != "ENG" || owner["device"] != "core1" {
			t.Errorf("address %d owner: %v", i, owner)
		}
	}
	// the SVI class cannot be read: the limits say so, the answer is not a quiet "no owner"
	routes[nqePath] = func(_ *http.Request, body []byte) (int, any) {
		if strings.Contains(string(body), "sub.ipv4.addresses") {
			return 200, map[string]any{"items": []any{}, "totalNumItems": 0}
		}
		return 400, map[string]any{"message": "boom"}
	}
	r, _ = mustRun(t, "inspect-inventory", routes, `{"network_id":"n1","kind":"ip_owner","ips":["10.20.101.2"]}`)
	if !strings.Contains(strings.Join(r.Limits, " "), "SVI) interface addresses could not be read") {
		t.Errorf("limits %v", r.Limits)
	}
}

func TestInventoryRoutesSaysWhichVRFsHaveNoDefaultRoute(t *testing.T) {
	r, _ := inv(t, map[string]fwdtest.Handler{
		"hop in entry.nextHops": rowsOf(2, map[string]any{"Device": "core1", "VRF": "ENG", "Prefix": "10.20.101.0/24", "Protocol": "DIRECT_CONNECTED"}),
		"Default route":         rowsOf(2, map[string]any{"Device": "core1", "VRF": "default", "Routes": 40, "Default route": true}, map[string]any{"Device": "core1", "VRF": "ENG", "Routes": 3, "Default route": false}),
	}, nil, `{"network_id":"n1","kind":"routes","device":"core1"}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "1 of 2 VRF(s) listed have NO IPv4 default route") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	if _, ok := r.Evidence[0].Detail["default_route_by_vrf"]; !ok {
		t.Errorf("the per-VRF default route facts are in the evidence")
	}
}

func TestInventoryIGPNeighborsSaysItIsOSPFOnlyAndCloudKindsSendTheirQueries(t *testing.T) {
	r, _ := inv(t, map[string]fwdtest.Handler{"neighbor in area.neighbors": rowsOf(1, map[string]any{"Device": "core1", "Protocol": "OSPF", "Area": "0.0.0.0"})}, nil, `{"network_id":"n1","kind":"igp_neighbors"}`)
	if !strings.Contains(strings.Join(r.Limits, " "), "OSPF adjacencies only") {
		t.Errorf("limits %v", r.Limits)
	}
	for kind, marker := range map[string]string{"cloud_subnets": "vpc.subnets", "cloud_instances": "vpc.computeInstances"} {
		r, _ := inv(t, map[string]fwdtest.Handler{marker: rowsOf(1, map[string]any{"Account": "a", "VPC": "v"})}, nil, `{"network_id":"n1","kind":"`+kind+`","account":"a"}`)
		if r.Status != result.OK || !strings.Contains(strings.Join(r.Limits, " "), "not that every resource type was read") {
			t.Errorf("%s: %s %v", kind, r.Status, r.Limits)
		}
	}
}
