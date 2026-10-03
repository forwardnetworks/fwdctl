package skills_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

// modelRoutes answers the model queries by what they select: interface addresses, default routes, BGP neighbors, Adj-RIB-Out counts.
func modelRoutes() map[string]fwdtest.Handler {
	rows := func(r []map[string]any) (int, any) { return 200, map[string]any{"items": r, "totalNumItems": len(r)} }
	return map[string]fwdtest.Handler{
		snapsPath: ready("s1"),
		nqePath: func(_ *http.Request, body []byte) (int, any) {
			q := string(body)
			switch {
			case strings.Contains(q, "addr in sub.ipv4.addresses"):
				return rows([]map[string]any{
					{"device": "wan1", "iface": "po1", "sub": "po1.698", "vrf": "INET", "ip": "203.0.113.2", "prefixLength": 30},
					{"device": "core1", "iface": "e1", "sub": "e1", "vrf": "default", "ip": "10.0.0.1", "prefixLength": 24},
					{"device": "dist1", "iface": "e2", "sub": "e2", "vrf": "default", "ip": "10.0.0.2", "prefixLength": 24},
				})
			case strings.Contains(q, "hop in entry.nextHops"):
				return rows([]map[string]any{
					{"device": "wan1", "vrf": "INET", "nextHop": "203.0.113.1", "egress": nil, "sub": nil, "hopType": "REMOTE"},    // unowned: the ISP
					{"device": "dist1", "vrf": "default", "nextHop": "10.0.0.1", "egress": "e2", "sub": "e2", "hopType": "REMOTE"}, // owned by core1
					{"device": "dist1", "vrf": "default", "nextHop": nil, "egress": nil, "sub": nil, "hopType": "DROP"},
				})
			case strings.Contains(q, "protocol.bgp"):
				return rows([]map[string]any{
					{"device": "wan1", "vrf": "INET", "peer": "203.0.113.1", "peerAS": 65000, "localAS": 64512, "state": "ESTABLISHED", "peerDevice": nil, "description": "ISP", "advertised": 477, "received": 1},
					{"device": "wan1", "vrf": "default", "peer": "10.0.0.1", "peerAS": 64512, "localAS": 64512, "state": "ESTABLISHED", "peerDevice": "core1", "description": "core", "advertised": 5, "received": 5},
				})
			case strings.Contains(q, "adjRibOutPost"):
				return rows([]map[string]any{
					{"device": "wan1", "afi": "IPV4_UNICAST", "peer": "203.0.113.1", "vrf": "INET", "routes": 480, "distinct": 478},
					{"device": "wan1", "afi": "IPV4_UNICAST", "peer": "203.0.113.1", "vrf": "OTHER", "routes": 1000, "distinct": 900},
					{"device": "wan1", "afi": "IPV6_UNICAST", "peer": "203.0.113.1", "vrf": "INET", "routes": 20, "distinct": 20},
					{"device": "wan1", "afi": "IPV4_UNICAST", "peer": "10.0.0.1", "vrf": nil, "routes": 5, "distinct": 5},
				})
			}
			return 400, map[string]any{"message": "unexpected query: " + q}
		},
	}
}

func modelEv(t *testing.T, r result.Result) string {
	t.Helper()
	b, _ := json.Marshal(r.Evidence)
	return string(b)
}

func TestInspectEdgeFlagsNextHopsNobodyOwnsAndKeepsOwnedOnesOut(t *testing.T) {
	r, _ := mustRun(t, "inspect-edge", modelRoutes(), `{"network_id":"n1"}`)
	text := modelEv(t, r)
	if r.Status != result.OK || !strings.Contains(r.Finding, "1 exit candidate") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	for _, want := range []string{`"next_hop":"203.0.113.1"`, `"vrf":"INET"`, `"owned_handoffs_count":1`, `"local_address":"wan1 203.0.113.2"`, `"egress":"wan1 po1.698"`} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %s in %s", want, text)
		}
	}
	if strings.Contains(text, `"next_hop":"10.0.0.1"`) {
		t.Errorf("an owned next hop is not an exit candidate: %s", text)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "not forwarding handoffs") {
		t.Errorf("the dropped next hop is counted: %v", r.Limits)
	}
	r, _ = mustRun(t, "inspect-edge", modelRoutes(), `{"network_id":"n1","include_owned":true}`)
	if !strings.Contains(modelEv(t, r), `"owned_handoffs"`) || !strings.Contains(modelEv(t, r), `"next_hop_owner"`) {
		t.Errorf("include_owned lists the handoffs to modelled devices with their owner")
	}
	if r, _ := mustRun(t, "inspect-edge", modelRoutes(), `{"network_id":"n1","vrf":"NOPE"}`); r.Status != result.Unknown {
		t.Errorf("no match is unknown: %s", r.Status)
	}
}

func TestInspectBGPNeighborsPutsTheUnmodelledPeerFirstWithItsAdjRibOut(t *testing.T) {
	r, _ := mustRun(t, "inspect-bgp-neighbors", modelRoutes(), `{"network_id":"n1"}`)
	text := modelEv(t, r)
	if r.Status != result.OK || !strings.Contains(r.Finding, "1 with a peer that is not a modelled device") || !strings.Contains(r.Finding, "snapshot s1)") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	if i, j := strings.Index(text, `"peer":"203.0.113.1"`), strings.Index(text, `"peer":"10.0.0.1"`); i < 0 || j < 0 || i > j {
		t.Errorf("the unmodelled peer comes first: %s", text)
	}
	for _, want := range []string{`"adj_rib_out_routes":480`, `"adj_rib_out_distinct_prefixes":478`, `"adj_rib_out_other_vrfs_routes":1020`, `"snapshot_id":"s1"`, `"peer_as":65000`, `"peer_device":null`, `"advertised_prefixes":477`} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %s in %s", want, text)
		}
	}
	r, _ = mustRun(t, "inspect-bgp-neighbors", modelRoutes(), `{"network_id":"n1","unmodelled_only":true,"peer_as":65000}`)
	if !strings.Contains(r.Finding, "1 BGP neighbor") {
		t.Errorf("filters: %s", r.Finding)
	}
	if r, _ := mustRun(t, "inspect-bgp-neighbors", modelRoutes(), `{"network_id":"n1","state":"IDLE"}`); r.Status != result.Unknown {
		t.Errorf("no match is unknown: %s", r.Status)
	}
}

func TestFindIPOwnerSaysOwnerSubnetOrNone(t *testing.T) {
	r, _ := mustRun(t, "find-ip-owner", modelRoutes(), `{"network_id":"n1","ips":["203.0.113.2","10.0.0.77","8.8.8.8"]}`)
	text := modelEv(t, r)
	if r.Status != result.OK || !strings.Contains(r.Finding, "1 of 3") || !strings.Contains(r.Finding, "(snapshot s1)") || !strings.Contains(text, `"snapshot_id":"s1"`) {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	for _, want := range []string{`"device":"wan1"`, `"subinterface":"po1.698"`, `"vrf":"INET"`, `"inside_connected_subnet":"10.0.0.0/24"`, `no modelled owner`} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %s in %s", want, text)
		}
	}
	for _, bad := range []string{`{"network_id":"n1","ips":["not-an-ip"]}`, `{"network_id":"n1","ips":["2001:db8::1"]}`, `{"network_id":"n1","ips":[]}`} {
		if _, _, err := runSkill(t, "find-ip-owner", modelRoutes(), bad); err == nil {
			t.Errorf("%s must be refused", bad)
		}
	}
}

func TestFindNQEQueryListsOneLevelOfTheLibrary(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		"GET /api/nqe/queries": fwdtest.Const(200, []any{
			map[string]any{"queryId": "FQ_1", "path": "/L3/BGP/Neighbors", "intent": "x", "repository": "FWD"},
			map[string]any{"queryId": "FQ_2", "path": "/L3/MTU", "intent": "y", "repository": "FWD"},
			map[string]any{"queryId": "Q_3", "path": "/Team/q", "intent": "z", "repository": "ORG"},
			map[string]any{"queryId": "Q_4", "path": "/rootq", "intent": "w", "repository": "ORG"},
		}),
	}
	r, _ := mustRun(t, "find-nqe-query", routes, `{"network_id":"n1","list":true}`)
	text := modelEv(t, r)
	for _, want := range []string{`"path":"/L3/"`, `"path":"/Team/"`, `"queries_below":2`, `"path":"/rootq"`} {
		if !strings.Contains(text, want) {
			t.Errorf("root listing missing %s in %s", want, text)
		}
	}
	r, _ = mustRun(t, "find-nqe-query", routes, `{"network_id":"n1","list":true,"directory":"/L3/"}`)
	if text = modelEv(t, r); !strings.Contains(text, `"path":"/L3/BGP/"`) || !strings.Contains(text, `"path":"/L3/MTU"`) {
		t.Errorf("one level of /L3/: %s", text)
	}
	if r, _ := mustRun(t, "find-nqe-query", routes, `{"network_id":"n1","list":true,"directory":"/Acme/"}`); r.Status != result.Unknown {
		t.Errorf("a directory with nothing under it is unknown: %s", r.Status)
	}
}

// edgeRoutes serves one default route per entry in hops, the given interface addresses and BGP neighbors.
func edgeRoutes(hops, addrs, nbrs []map[string]any) map[string]fwdtest.Handler {
	rows := func(r []map[string]any) (int, any) { return 200, map[string]any{"items": r, "totalNumItems": len(r)} }
	return map[string]fwdtest.Handler{
		snapsPath: ready("s1"),
		nqePath: func(_ *http.Request, body []byte) (int, any) {
			q := string(body)
			switch {
			case strings.Contains(q, "addr in sub.ipv4.addresses"):
				return rows(addrs)
			case strings.Contains(q, "hop in entry.nextHops"):
				return rows(hops)
			case strings.Contains(q, "protocol.bgp"):
				return rows(nbrs)
			}
			return 400, map[string]any{"message": "unexpected query: " + q}
		},
	}
}

func TestInspectEdgeBGPNeighborOnTheOtherSideProvesOwnership(t *testing.T) {
	// the next hop 198.51.100.1 is on no interface, but pe1 has a neighbor at 198.51.100.2 (our own address on the link) and names wan1 as the peer device
	routes := edgeRoutes(
		[]map[string]any{{"device": "wan1", "vrf": "INET", "nextHop": "198.51.100.1", "egress": "e9", "sub": "e9", "hopType": "REMOTE"}},
		[]map[string]any{{"device": "wan1", "iface": "e9", "sub": "e9", "vrf": "INET", "ip": "198.51.100.2", "prefixLength": 30}},
		[]map[string]any{{"device": "pe1", "vrf": "default", "peer": "198.51.100.2", "peerAS": 64512, "localAS": 64512, "state": "ESTABLISHED", "peerDevice": "wan1"}})
	r, _ := mustRun(t, "inspect-edge", routes, `{"network_id":"n1","include_owned":true}`)
	text := modelEv(t, r)
	if !strings.Contains(r.Finding, "Every default-route next hop (1) is owned") || !strings.Contains(r.Finding, "snapshot s1)") {
		t.Fatalf("a next hop a neighbor record proves owned is not an exit candidate: %s", r.Finding)
	}
	for _, want := range []string{`"owner_proof":"pe1 has a BGP neighbor at 198.51.100.2`, `"next_hop_owner":{"device":"pe1"}`, `"snapshot_id":"s1"`} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %s in %s", want, text)
		}
	}
}

func TestInspectEdgeLikelyInternetEdgeNeedsAPublicNextHop(t *testing.T) {
	addr := func(ip string) []map[string]any {
		return []map[string]any{{"device": "wan1", "iface": "e9", "sub": "e9", "vrf": "INET", "ip": ip, "prefixLength": 30}}
	}
	nbr := func(peer string) []map[string]any {
		return []map[string]any{{"device": "wan1", "vrf": "INET", "peer": peer, "peerAS": 65000, "localAS": 64512, "state": "ESTABLISHED", "peerDevice": nil}}
	}
	hop := func(nh string) []map[string]any {
		return []map[string]any{{"device": "wan1", "vrf": "INET", "nextHop": nh, "egress": "e9", "sub": "e9", "hopType": "REMOTE"}}
	}
	r, _ := mustRun(t, "inspect-edge", edgeRoutes(hop("198.51.100.1"), addr("198.51.100.2"), nbr("198.51.100.1")), `{"network_id":"n1"}`)
	if text := modelEv(t, r); !strings.Contains(text, `"likely_internet_edge":true`) || !strings.Contains(r.Finding, "1 likely internet edge") || !strings.Contains(text, `"likely_internet_edges":1`) {
		t.Errorf("a public unowned next hop with an eBGP session to an unmodelled peer is a likely edge: %s | %s", r.Finding, text)
	}
	for _, private := range []string{"100.64.0.1", "10.9.9.1"} {
		ip2 := private[:len(private)-1] + "2"
		r, _ = mustRun(t, "inspect-edge", edgeRoutes(hop(private), addr(ip2), nbr(private)), `{"network_id":"n1"}`)
		text := modelEv(t, r)
		if strings.Contains(text, `"likely_internet_edge":`) || !strings.Contains(r.Finding, "0 likely internet edge") || !strings.Contains(text, `"exit_candidates":1`) {
			t.Errorf("%s: a non-public next hop is an exit candidate but never a likely internet edge: %s | %s", private, r.Finding, text)
		}
	}
}

func TestInspectEdgeSaysEgressUnresolvedWhenNoSubnetHoldsTheNextHop(t *testing.T) {
	r, _ := mustRun(t, "inspect-edge", edgeRoutes(
		[]map[string]any{{"device": "wan1", "vrf": "INET", "nextHop": "198.51.100.1", "egress": nil, "sub": nil, "hopType": "REMOTE"}},
		[]map[string]any{{"device": "wan1", "iface": "e9", "sub": "e9", "vrf": "INET", "ip": "192.0.2.2", "prefixLength": 30}}, nil), `{"network_id":"n1"}`)
	if text := modelEv(t, r); !strings.Contains(text, `"egress_unresolved"`) {
		t.Errorf("no interface subnet holds the next hop: %s", text)
	}
	// and when one does, it is not said
	r, _ = mustRun(t, "inspect-edge", modelRoutes(), `{"network_id":"n1"}`)
	if text := modelEv(t, r); strings.Contains(text, `"egress_unresolved"`) {
		t.Errorf("the INET next hop has a subnet: %s", text)
	}
}

// publicRoutes answers the three reads find-public-addresses makes: addresses with device and interface types, security zones, management IPs.
func publicRoutes(zones bool) map[string]fwdtest.Handler {
	rows := func(r []map[string]any) (int, any) { return 200, map[string]any{"items": r, "totalNumItems": len(r)} }
	return map[string]fwdtest.Handler{
		snapsPath: ready("s1"),
		nqePath: func(_ *http.Request, body []byte) (int, any) {
			q := string(body)
			switch {
			case strings.Contains(q, "device.securityZones"):
				if !zones {
					return rows(nil)
				}
				return rows([]map[string]any{{"device": "fw1", "zone": "INSIDE", "iface": "po1", "sub": "po1.20"}})
			case strings.Contains(q, "iface.links"):
				return rows([]map[string]any{{"device": "FWX_AGY-EXT-DMZ", "iface": "po10", "peer": "sw1"}, {"device": "FWX_AGY-EXT-DMZ", "iface": "po10", "peer": "FWX_OTHER-EXT-DMZ"}})
			case strings.Contains(q, "select {device: device.name, deviceType: device.platform.deviceType}"):
				return rows([]map[string]any{{"device": "sw1", "deviceType": "SWITCH"}, {"device": "FWX_OTHER-EXT-DMZ", "deviceType": "FIREWALL"}})
			case strings.Contains(q, "managementIps"):
				return rows([]map[string]any{{"device": "sw1", "ip": "203.0.113.200"}, {"device": "sw1", "ip": "198.51.101.9"}})
			case strings.Contains(q, "addr in sub.ipv4.addresses"):
				a := func(dev, typ, iface, itype, sub, vrf, ip string, bits int, desc any) map[string]any {
					return map[string]any{"device": dev, "deviceType": typ, "iface": iface, "ifaceType": itype, "sub": sub, "vrf": vrf, "ip": ip, "prefixLength": bits, "description": desc}
				}
				return rows([]map[string]any{
					a("sw1", "SWITCH", "mgmt0", "IF_ETHERNET", "mgmt0", "management", "198.51.101.10", 32, nil),                   // management by VRF
					a("sw1", "SWITCH", "e1", "IF_ETHERNET", "e1", "default", "198.51.101.9", 32, nil),                             // management by management IP
					a("sw1", "SWITCH", "e2", "IF_ETHERNET", "e2", "default", "198.51.101.11", 32, "uplink"),                       // unknown
					a("sw1", "SWITCH", "lo0", "IF_LOOPBACK", "lo0", "default", "198.51.101.12", 32, nil),                          // loopback
					a("sw1", "SWITCH", "e3", "IF_ETHERNET", "e3", "default", "10.0.0.1", 24, nil),                                 // private: dropped
					a("sw1", "SWITCH", "e4", "IF_ETHERNET", "e4", "default", "100.64.0.1", 24, nil),                               // shared address space: dropped
					a("sw1", "SWITCH", "e5", "IF_ETHERNET", "e5", "default", "203.0.113.5", 24, nil),                              // documentation: dropped
					a("fw1", "FIREWALL", "po1", "IF_AGGREGATE", "po1.20", "default", "192.0.1.1", 29, "x"),                        // customer-facing by zone INSIDE
					a("fw1", "FIREWALL", "po2", "IF_AGGREGATE", "po2.30", "default", "192.0.1.9", 29, "outside"),                  // internet-facing by description
					a("fw1", "FIREWALL", "po3", "IF_AGGREGATE", "po3.40", "default", "192.0.1.17", 29, "untrust"),                 // untrust is not trust
					a("fw1", "FIREWALL", "po4", "IF_AGGREGATE", "po4.50", "default", "192.0.1.25", 29, "inside outside"),          // both: unknown
					a("fw1", "FIREWALL", "po5", "IF_AGGREGATE", "po5.60", "default", "192.0.1.33", 29, "aci-int-fwx"),             // unknown: no hint
					a("fw1", "FIREWALL", "po6", "IF_AGGREGATE", "po6.70", "Mgmt-vrf", "192.0.1.41", 29, nil),                      // management by VRF on a firewall
					a("FWX_AGY-EXT-DMZ", "FIREWALL", "po9", "IF_AGGREGATE", "po9.1", "default", "2.250.1.1", 29, "outside"),       // outside on an external context
					a("FWX_AGY-EXT-DMZ", "FIREWALL", "po10", "IF_AGGREGATE", "po10.2", "default", "2.250.1.9", 29, "aci-ext-fwx"), // no inside/outside word: unknown, qualified by context and link
					a("lb1", "LOAD_BALANCER", "1.1", "IF_ETHERNET", "1.1", "4", "192.0.3.77", 24, "inside"),                       // not a firewall: unknown even with the word
				})
			}
			return 400, map[string]any{"message": "unexpected query: " + q}
		},
	}
}

func TestFindPublicAddressesInfersRolesAndSaysWhy(t *testing.T) {
	r, _ := mustRun(t, "find-public-addresses", publicRoutes(true), `{"network_id":"n1"}`)
	text := modelEv(t, r)
	// 11 public rows: 198.51.101.9-12 (4), 192.0.1.x firewall rows (6) and the load balancer 192.0.3.77; 10/8, 100.64/10 and 203.0.113/24 are not public
	if r.Status != result.OK || !strings.Contains(r.Finding, "snapshot s1)") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	for _, want := range []string{`"public_addresses":13`, `"interface_addresses_read":16`, `"role":"customer_facing"`, `"role":"internet_facing"`, `"role":"loopback"`, `"role":"management"`,
		`security zone \"INSIDE\" says \"inside\"`, `interface description \"outside\" says \"outside\"`, `the address is one of the device's management IPs`, `VRF \"Mgmt-vrf\" is a management name`} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %s in %s", want, text)
		}
	}
	// untrust is internet-facing, not customer-facing; a firewall word on a load balancer proves nothing; both words is unknown
	m := map[string]map[string]any{}
	var d struct{ Addresses []map[string]any }
	ev, _ := json.Marshal(r.Evidence[0].Detail)
	_ = json.Unmarshal(ev, &d)
	for _, a := range d.Addresses {
		m[a["ip"].(string)] = a
	}
	for ip, role := range map[string]string{"192.0.1.17": "internet_facing", "192.0.3.77": "unknown", "192.0.1.25": "unknown", "192.0.1.33": "unknown", "198.51.101.11": "unknown", "198.51.101.12": "loopback", "198.51.101.10": "management"} {
		if m[ip]["role"] != role {
			t.Errorf("%s: role %v, want %s", ip, m[ip]["role"], role)
		}
	}
	for _, bad := range []string{"10.0.0.1", "100.64.0.1", "203.0.113.5"} {
		if _, ok := m[bad]; ok {
			t.Errorf("%s is not public", bad)
		}
	}
	// the candidate set is exact and never offers internet-facing or unknown addresses
	for _, want := range []string{`"management":{"addresses":3`, `"cidrs":["192.0.1.41/32","198.51.101.9/32","198.51.101.10/32"]`, `"customer_facing":{"addresses":1`} {
		if !strings.Contains(text, want) {
			t.Errorf("candidate set missing %s in %s", want, text)
		}
	}
	if !strings.Contains(text, `"candidate_exclude"`) || strings.Contains(text, `"internet_facing":{"addresses"`) || strings.Contains(text, `"unknown":{"addresses"`) {
		t.Errorf("candidate set: %s", text)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "candidate set for the person to review") || !strings.Contains(strings.Join(r.Limits, " "), "next processed snapshot") {
		t.Errorf("limits: %v", r.Limits)
	}
	for _, want := range []string{`the context name says external`, `"context_hint":"external"`, `"linked_to":"FIREWALL (another context), SWITCH"`, `"firewall_role_unknown"`, `needs review: do not exclude blindly`,
		`"ownership_not_verified":true`, `"prefix_groups"`, `"largest_aggregates"`, `"unknown_by_group"`, `"verify"`, `"interface_pattern":"poN"`, `"firewall_link_rows":2`} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %s in %s", want, text)
		}
	}
	if strings.Contains(text, `"candidate_exclude":{`) && strings.Contains(strings.Split(strings.Split(text, `"candidate_exclude":{`)[1], `"prefix_groups"`)[0], "192.0.1.9/") {
		t.Errorf("internet-facing space must not be in candidate_exclude")
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "does NOT check who owns a public prefix") {
		t.Errorf("limits: %v", r.Limits)
	}
	// no zones modelled: said
	r2, _ := mustRun(t, "find-public-addresses", publicRoutes(false), `{"network_id":"n1"}`)
	if !strings.Contains(strings.Join(r2.Limits, " "), "no security zone") {
		t.Errorf("an empty zone read is stated: %v", r2.Limits)
	}
}

func TestFindPublicAddressesFiltersAndPages(t *testing.T) {
	r, _ := mustRun(t, "find-public-addresses", publicRoutes(true), `{"network_id":"n1","limit":3,"offset":2}`)
	text := modelEv(t, r)
	if !strings.Contains(text, `"offset":2`) || !strings.Contains(strings.Join(r.Limits, " "), "Page with offset=5") || !strings.Contains(strings.Join(r.Limits, " "), "covers all 13") && !strings.Contains(strings.Join(r.Limits, " "), "cover all 13") {
		t.Errorf("paging: %v %s", r.Limits, text)
	}
	r, _ = mustRun(t, "find-public-addresses", publicRoutes(true), `{"network_id":"n1","role":"management"}`)
	if !strings.Contains(r.Finding, "3 public") || !strings.Contains(r.Finding, "3 management") {
		t.Errorf("role filter: %s", r.Finding)
	}
	r, _ = mustRun(t, "find-public-addresses", publicRoutes(true), `{"network_id":"n1","device":"FW1","vrf":"default"}`)
	if !strings.Contains(r.Finding, "5 public") {
		t.Errorf("device and vrf filters are case-insensitive: %s", r.Finding)
	}
	if r, _ := mustRun(t, "find-public-addresses", publicRoutes(true), `{"network_id":"n1","device":"nope"}`); r.Status != result.Unknown {
		t.Errorf("no match is unknown: %s", r.Status)
	}
	if r, _ := mustRun(t, "find-public-addresses", publicRoutes(true), `{"network_id":"n1","offset":500}`); r.Status != result.Unknown {
		t.Errorf("offset past the end is unknown: %s", r.Status)
	}
	for _, bad := range []string{`{"network_id":"n1","role":"guess"}`, `{"network_id":"n1","offset":-1}`, `{}`} {
		if _, _, err := runSkill(t, "find-public-addresses", publicRoutes(true), bad); err == nil {
			t.Errorf("%s must be refused", bad)
		}
	}
}

func TestBGPAdvertisedListsTheMinimalSetOutsideTheGivenBlocksAndCountsByContaining16(t *testing.T) {
	routes := modelRoutes()
	routes[nqePath] = func(_ *http.Request, body []byte) (int, any) {
		if !strings.Contains(string(body), `device.name == \"wan1\"`) {
			return 400, map[string]any{"message": "unexpected query: " + string(body)}
		}
		p := func(vrf any, peer, afi, prefix string) map[string]any {
			return map[string]any{"peer": peer, "afi": afi, "vrf": vrf, "prefix": prefix}
		}
		withAttrs := func(m map[string]any, nextHop, origin string, asPath []any) map[string]any {
			m["nextHop"], m["origin"], m["asPath"], m["active"] = nextHop, origin, asPath, true
			return m
		}
		items := []map[string]any{
			p("INET", "203.0.113.1", "IPV4_UNICAST", "204.64.0.0/14"),                                                                             // inside the internal block
			p("INET", "203.0.113.1", "IPV4_UNICAST", "204.65.1.0/24"),                                                                             // inside it, and inside the /14 above
			withAttrs(p("INET", "203.0.113.1", "IPV4_UNICAST", "198.51.100.0/24"), "10.9.9.9", "IGP", []any{float64(4259840021), float64(65001)}), // outside, learned from an internal peer
			p("INET", "203.0.113.1", "IPV4_UNICAST", "198.51.100.0/25"),                                                                           // inside the /24: dropped by aggregation
			withAttrs(p("INET", "203.0.113.1", "IPV4_UNICAST", "192.0.2.0/24"), "0.0.0.0", "INCOMPLETE", nil),                                     // outside, local
			p("INET", "203.0.113.1", "IPV4_UNICAST", "192.0.2.0/24"),                                                                              // duplicate
			p("INET", "203.0.113.1", "IPV6_UNICAST", "2001:db8::/32"),                                                                             // another family
			p("OTHER", "203.0.113.1", "IPV4_UNICAST", "8.8.8.0/24"),                                                                               // another VRF
			p("INET", "203.0.113.9", "IPV4_UNICAST", "1.1.1.0/24"),                                                                                // another peer
		}
		return 200, map[string]any{"items": items, "totalNumItems": len(items)}
	}
	r, _ := mustRun(t, "inspect-bgp-neighbors", routes, `{"network_id":"n1","device":"wan1","peer":"203.0.113.1","vrf":"INET","advertised":{"outside":["204.64.0.0/14"]}}`)
	d := r.Evidence[0].Detail
	if d["distinct_prefixes"] != 5 || d["after_dropping_covered"] != 3 || d["listed"] != 2 {
		t.Fatalf("5 distinct, 3 after aggregation (the /25 and the /24 inside the /14 go), 2 outside the /14 (the /14 and its /24 are inside): %v", d)
	}
	b := jsonOf(r)
	for _, want := range []string{`"prefix":"192.0.2.0/24"`, `"prefix":"198.51.100.0/24"`, `"block":"198.51.0.0/16"`} {
		if !strings.Contains(b, want) {
			t.Errorf("missing %s in %s", want, b)
		}
	}
	for _, not := range []string{"8.8.8.0", "1.1.1.0", "2001:db8", "204.65.1.0"} {
		if strings.Contains(b, `"prefix":"`+not) {
			t.Errorf("%s must not be listed", not)
		}
	}
	for _, want := range []string{`"origin_type":"local"`, `"origin_type":"learned"`, `"next_hop":"10.9.9.9"`, `"as_path":"4259840021 65001"`, `"learned_by_next_hop":[{"name":"10.9.9.9","prefixes":1}]`} {
		if !strings.Contains(b, want) {
			t.Errorf("per-prefix origin: missing %s in %s", want, b)
		}
	}
	if !strings.Contains(strings.Join(r.Limits, "|"), "other VRFs or address families") {
		t.Errorf("limits: %v", r.Limits)
	}
	for _, bad := range []string{`{"network_id":"n1","advertised":{}}`, `{"network_id":"n1","device":"wan1","peer":"nope","advertised":{}}`, `{"network_id":"n1","device":"wan1","peer":"203.0.113.1","advertised":{"outside":["x"]}}`} {
		if _, _, err := runSkill(t, "inspect-bgp-neighbors", routes, bad); err == nil {
			t.Errorf("%s must be refused", bad)
		}
	}
	// a peer or VRF with nothing is unknown, never "advertises nothing"
	if r, _ := mustRun(t, "inspect-bgp-neighbors", routes, `{"network_id":"n1","device":"wan1","peer":"203.0.113.77","advertised":{}}`); r.Status != result.Unknown {
		t.Errorf("no rows is not proof: %s", r.Status)
	}
}
