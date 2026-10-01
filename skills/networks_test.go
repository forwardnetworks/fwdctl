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
