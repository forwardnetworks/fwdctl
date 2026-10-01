package skills_test

import (
	"context"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/nqelint"
	"github.com/forwardnetworks/fwdctl/skills"
)

func synth(t *testing.T, extra map[string]fwdtest.Handler, o skills.SynthOptions) (*skills.SynthResult, error) {
	t.Helper()
	sess, _ := fwdtest.New(t, claimRoutes(extra))
	an, err := skills.AnalyzeEdge(context.Background(), sess, skills.EdgeQuery{NetworkID: "n1"})
	if err != nil {
		t.Fatal(err)
	}
	o.NetworkID = "n1"
	return skills.SynthesizeInternet(an, o)
}

func lintClean(t *testing.T, src string) {
	t.Helper()
	diags := nqelint.Lint(src)
	more, err := nqelint.CheckSyntheticRows(src, "internet")
	if err != nil {
		t.Fatal(err)
	}
	if all := append(diags, more...); len(all) > 0 {
		t.Fatalf("not clean: %+v\n%s", all, src)
	}
}

func TestSynthesizeInternetWritesOneRowPerEgressWithParentUplinkAndVLAN(t *testing.T) {
	needCorpora(t)
	r, err := synth(t, nil, skills.SynthOptions{})
	if err != nil {
		t.Fatal(err)
	}
	lintClean(t, r.Source)
	for _, want := range []string{
		`uplinkInterface: { deviceName: "wan1", interfaceName: "po1" }`, `gatewayInterface: { deviceName: "wan1", interfaceName: "po1.698" }`, `vlan: 698,`,
		`SubnetDiscoveryMethod.interfaceAddresses`, `subnets: emptySubnets`, `backdoorInterfaces: emptyInterfaces`, `@query`, "snapshot s1", "unowned next hop 203.0.113.1", "double claim", "likely internet edge",
	} {
		if !strings.Contains(r.Source, want) {
			t.Errorf("missing %q in\n%s", want, r.Source)
		}
	}
	if strings.Contains(r.Source, "vlan: 0") || len(r.Rows) != 1 {
		t.Errorf("rows %d\n%s", len(r.Rows), r.Source)
	}
}

func TestSynthesizeInternetDiscoveryChoices(t *testing.T) {
	needCorpora(t)
	r, err := synth(t, nil, skills.SynthOptions{Discovery: "ipRoutes"})
	if err != nil || !strings.Contains(r.Source, "SubnetDiscoveryMethod.ipRoutes({ advertisesDefaultRoute: false })") {
		t.Fatalf("%v\n%v", err, r)
	}
	lintClean(t, r.Source)
	r, err = synth(t, nil, skills.SynthOptions{Discovery: "bgpRoutes"})
	if err != nil {
		t.Fatal(err)
	}
	lintClean(t, r.Source)
	for _, want := range []string{`SubnetDiscoveryMethod.bgpRoutes({ peerIps: [ipAddress("203.0.113.1")] })`, "disagree by hundreds", "unreconciled"} {
		if !strings.Contains(r.Source, want) {
			t.Errorf("bgpRoutes: missing %q in\n%s", want, r.Source)
		}
	}
	if _, err = synth(t, nil, skills.SynthOptions{Discovery: "none"}); err == nil || !strings.Contains(err.Error(), "--subnets") {
		t.Errorf("none without subnets is refused: %v", err)
	}
	r, err = synth(t, nil, skills.SynthOptions{Discovery: "none", Subnets: []string{"198.51.100.0/24"}})
	if err != nil || !strings.Contains(r.Source, `subnets: [ipSubnet("198.51.100.0/24")]`) {
		t.Fatalf("%v\n%v", err, r)
	}
	lintClean(t, r.Source)
	if _, err = synth(t, nil, skills.SynthOptions{Discovery: "bogus"}); err == nil {
		t.Errorf("an unknown discovery is refused")
	}
}

func TestSynthesizeInternetNamesTheClaimants(t *testing.T) {
	needCorpora(t)
	r, err := synth(t, map[string]fwdtest.Handler{
		"GET /api/networks/n1/l3-vpns": fwdtest.Const(200, []any{map[string]any{"name": "dir", "connections": []any{synConn("wan1", "po1", 698, nil)}}}),
	}, skills.SynthOptions{})
	if err != nil || !strings.Contains(r.Source, `ALREADY CLAIMED by l3vpn "dir"`) {
		t.Fatalf("%v\n%v", err, r)
	}
	lintClean(t, r.Source)
}
