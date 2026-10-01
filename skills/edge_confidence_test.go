package skills_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

// edgeCase is one synthetic handoff: a device with a default route to a public next hop and an eBGP session to an unmodelled peer.
type edgeCase struct {
	dev, vrf, nh string
	peerAS       int
	received     any
}

func confRoutes(cs []edgeCase) map[string]fwdtest.Handler {
	var hops, addrs, nbrs []map[string]any
	for _, c := range cs {
		local := c.nh[:strings.LastIndex(c.nh, ".")+1] + "2"
		hops = append(hops, map[string]any{"device": c.dev, "vrf": c.vrf, "nextHop": c.nh, "egress": "e9", "sub": "e9", "hopType": "REMOTE"})
		addrs = append(addrs, map[string]any{"device": c.dev, "iface": "e9", "sub": "e9", "vrf": c.vrf, "ip": local, "prefixLength": 30})
		nbrs = append(nbrs, map[string]any{"device": c.dev, "vrf": c.vrf, "peer": c.nh, "peerAS": c.peerAS, "localAS": 64512, "state": "ESTABLISHED", "peerDevice": nil, "received": c.received})
	}
	return edgeRoutes(hops, addrs, nbrs)
}

func groupRow(t *testing.T, r result.Result, vrf string, as int) map[string]any {
	t.Helper()
	d := r.Evidence[0].Detail
	for _, g := range d["likely_edge_groups"].([]map[string]any) {
		if g["vrf"] == vrf && fmt.Sprint(g["peer_as"]) == fmt.Sprint(as) {
			return g
		}
	}
	t.Fatalf("no group %s/%d in %v", vrf, as, d["likely_edge_groups"])
	return nil
}

func TestInspectEdgeGroupsLikelyEdgesByVRFAndPeerASWithConfidence(t *testing.T) {
	cs := []edgeCase{
		{"d1", "amer", "198.51.100.1", 64999, nil}, // negative control: private AS, received null
		{"d2", "amer", "198.51.101.1", 64999, nil},
		{"d3", "amer", "198.51.102.1", 64999, nil},
		{"d4", "corp-internet", "203.0.113.1", 4200000001, nil}, // internet-named but private AS and null received
		{"d5", "edge", "192.0.2.1", 64500, 1},                   // public AS sending one prefix
		{"d6", "core", "192.0.3.1", 64501, nil},                 // public AS only
		{"d7", "core", "192.0.4.1", 64501, 900000},              // public AS, a full table is not default-only
	}
	r, _ := mustRun(t, "inspect-edge", confRoutes(cs), `{"network_id":"n1","limit":2}`)
	if r.Status != result.OK {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	d := r.Evidence[0].Detail
	groups := d["likely_edge_groups"].([]map[string]any)
	if len(groups) != 4 || d["likely_internet_edges"] != 7 {
		t.Fatalf("7 likely edges fold into 4 (vrf, peer AS) groups even when limit hides rows: %d %v", len(groups), d["likely_internet_edges"])
	}
	// negative control: private AS + null received is LOW, however many edges and however it is named
	amer := groupRow(t, r, "amer", 64999)
	if amer["confidence"] != "low" || amer["likely_edges"] != 3 || amer["peer_as_class"] != "private" || amer["received_null_on"] != 3 {
		t.Errorf("private AS with null received must be low: %v", amer)
	}
	if g := groupRow(t, r, "corp-internet", 4200000001); g["confidence"] != "low" || g["vrf_hint"] != "internet" || g["peer_as_class"] != "private" {
		t.Errorf("a 32-bit private AS is private, and an internet-looking VRF name does not lift it above low: %v", g)
	}
	if g := groupRow(t, r, "edge", 64500); g["confidence"] != "high" || g["default_only"] != true || g["peer_as_class"] != "public" {
		t.Errorf("a public peer that sends one prefix is high: %v", g)
	}
	if g := groupRow(t, r, "core", 64501); g["confidence"] != "medium" || g["likely_edges"] != 2 {
		t.Errorf("a public AS alone is medium: %v", g)
	}
	// ranking: high first, the internet-named low group ahead of the plain low group
	order := []string{}
	for _, g := range groups {
		order = append(order, fmt.Sprint(g["vrf"]))
	}
	if strings.Join(order, ",") != "edge,core,corp-internet,amer" {
		t.Errorf("order %v", order)
	}
	// per-row confidence and the plain statements
	exits := d["exits"].([]map[string]any)
	if exits[0]["confidence"] != "high" || exits[0]["likely_internet_edge"] != true {
		t.Errorf("rows carry confidence, best first: %v", exits[0])
	}
	by := d["likely_internet_edges_by_confidence"].(map[string]int)
	if by["high"] != 1 || by["medium"] != 2 || by["low"] != 4 {
		t.Errorf("by confidence %v", by)
	}
	all := r.Finding + " | " + strings.Join(r.Limits, " | ")
	for _, want := range []string{"unreliable on public-addressed internal WANs", "received_prefixes is null on 5 of 7", "private peer AS with a null received count is always LOW"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in %s", want, all)
		}
	}
}

func TestInspectEdgeVRFHintAndASClassNeedWholeWords(t *testing.T) {
	cs := []edgeCase{
		{"d1", "amer", "198.51.100.1", 64500, nil},
		{"d2", "internal", "198.51.101.1", 64500, nil},
		{"d3", "dc-wan", "198.51.102.1", 64500, nil},
		{"d4", "guest-dmz", "198.51.103.1", 64500, nil},
		{"d5", "inet", "198.51.104.1", 64500, nil},
		{"d6", "x", "198.51.105.1", 65535, nil}, // 65535 is reserved, not private
	}
	r, _ := mustRun(t, "inspect-edge", confRoutes(cs), `{"network_id":"n1"}`)
	for vrf, want := range map[string]any{"amer": nil, "internal": nil, "dc-wan": "wan", "guest-dmz": "edge", "inet": "internet"} {
		if got := groupRow(t, r, vrf, 64500)["vrf_hint"]; got != want {
			t.Errorf("%s hint %v want %v", vrf, got, want)
		}
	}
	if g := groupRow(t, r, "inet", 64500); g["confidence"] != "high" {
		t.Errorf("public AS + internet VRF is high without received: %v", g)
	}
	if g := groupRow(t, r, "x", 65535); g["peer_as_class"] != "public" {
		t.Errorf("%v", g)
	}
}

func TestInspectEdgeOwnASSessionIsNotALikelyEdge(t *testing.T) {
	// iBGP to an unmodelled peer (peer AS = local AS) is not a likely edge, so it gets no confidence and no group
	r, _ := mustRun(t, "inspect-edge", confRoutes([]edgeCase{{"d1", "amer", "198.51.100.1", 64512, nil}}), `{"network_id":"n1"}`)
	d := r.Evidence[0].Detail
	if _, ok := d["likely_edge_groups"]; ok || strings.Contains(r.Finding, "confidence of the likely edges") {
		t.Errorf("%v | %s", d["likely_edge_groups"], r.Finding)
	}
}
