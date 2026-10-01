package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
)

func edgeRoutes() map[string]fwdtest.Handler {
	rows := func(r []map[string]any) (int, any) { return 200, map[string]any{"items": r, "totalNumItems": len(r)} }
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/snapshots": fwdtest.Snapshots(fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")),
		"POST /api/nqe": func(_ *http.Request, body []byte) (int, any) {
			q := string(body)
			switch {
			case strings.Contains(q, "addr in sub.ipv4.addresses"):
				return rows([]map[string]any{{"device": "wan1", "iface": "po1", "sub": "po1.698", "vrf": "INET", "ip": "203.0.113.2", "prefixLength": 30}})
			case strings.Contains(q, "hop in entry.nextHops"):
				return rows([]map[string]any{{"device": "wan1", "vrf": "INET", "nextHop": "203.0.113.1", "egress": nil, "sub": nil, "hopType": "REMOTE"}})
			case strings.Contains(q, "protocol.bgp"):
				return rows([]map[string]any{{"device": "wan1", "vrf": "INET", "peer": "203.0.113.1", "peerAS": 65000, "localAS": 64512, "state": "ESTABLISHED", "peerDevice": nil}})
			}
			return 400, map[string]any{"message": "unexpected query"}
		},
	}
}

func TestNqeSynthesizeInternetPrintsALintCleanQuery(t *testing.T) {
	needCorpora(t)
	code, out, errs := call(t, []string{"nqe", "synthesize", "internet", "--network", "n1"}, "", edgeRoutes())
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	for _, want := range []string{"snapshot s1", `interfaceName: "po1.698"`, "vlan: 698", "@query"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if !strings.Contains(errs, "lint and the internet row-type check are clean") {
		t.Errorf("stderr: %s", errs)
	}
}

func TestNqeSynthesizeInternetRefusesWhatItCannotWrite(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
		msg  string
	}{
		{[]string{"nqe", "synthesize", "internet"}, usage, "usage"},
		{[]string{"nqe", "synthesize", "l3vpn", "--network", "n1"}, usage, "usage"},
		{[]string{"nqe", "synthesize", "internet", "--network", "n1", "--discovery", "none"}, 1, "--subnets"},
		{[]string{"nqe", "synthesize", "internet", "--network", "n1", "--device", "nope"}, 1, "no likely internet edge"},
	} {
		code, out, errs := call(t, tc.args, "", edgeRoutes())
		if code != tc.code || out != "" || !strings.Contains(errs, tc.msg) {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", tc.args, code, out, errs)
		}
	}
}
