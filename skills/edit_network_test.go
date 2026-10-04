package skills_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

func TestEditNetworkCreateIsADryRunRefusesADuplicateAndReadsBack(t *testing.T) {
	nets := []any{map[string]any{"id": "1", "name": "prod"}}
	routes := map[string]fwdtest.Handler{
		"GET /api/networks": func(*http.Request, []byte) (int, any) { return 200, nets },
		"POST /api/networks": func(*http.Request, []byte) (int, any) {
			n := map[string]any{"id": "2", "name": "lab"}
			nets = append(nets, n)
			return 201, n
		},
	}
	r, srv := mustRun(t, "edit-network", routes, `{"object":"network","action":"create","name":"lab"}`)
	if r.Mode != result.ModeDryRun || writes(srv) != 0 || !r.Changes[0].Reversible {
		t.Fatalf("dry run: %s %+v", r.Finding, r.Changes)
	}
	r, _ = mustRun(t, "edit-network", routes, `{"object":"network","action":"create","name":"lab","apply":true}`)
	if r.Status != result.OK || !r.Changes[0].Applied || len(nets) != 2 {
		t.Fatalf("apply: %s %s", r.Status, r.Finding)
	}
	if _, _, err := runSkill(t, "edit-network", routes, `{"object":"network","action":"create","name":"PROD"}`); err == nil {
		t.Errorf("a name that exists (ignoring case) is refused")
	}
}

func TestEditNetworkTagDeleteNeedsTheTagNameAsConfirmAndIsIrreversible(t *testing.T) {
	tags := []any{map[string]any{"name": "Core", "color": "#112233", "devices": []any{"r1"}}}
	deleted := false
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/device-tags":         func(*http.Request, []byte) (int, any) { return 200, tags },
		"DELETE /api/networks/n1/device-tags/Core": func(*http.Request, []byte) (int, any) { deleted = true; tags = nil; return 204, nil },
	}
	r, srv := mustRun(t, "edit-network", routes, `{"network_id":"n1","object":"tag","action":"delete","name":"Core"}`)
	if writes(srv) != 0 || r.Changes[0].Reversible || !strings.Contains(r.Finding, `confirm="Core"`) {
		t.Fatalf("dry run: %s %+v", r.Finding, r.Changes)
	}
	if _, _, err := runSkill(t, "edit-network", routes, `{"network_id":"n1","object":"tag","action":"delete","name":"Core","apply":true}`); err == nil || deleted {
		t.Errorf("apply without confirm is refused before anything is sent")
	}
	r, _ = mustRun(t, "edit-network", routes, `{"network_id":"n1","object":"tag","action":"delete","name":"Core","apply":true,"confirm":"Core"}`)
	if r.Status != result.OK || !deleted {
		t.Errorf("apply: %s %s", r.Status, r.Finding)
	}
	if r, _ := mustRun(t, "edit-network", routes, `{"network_id":"n1","object":"tag","action":"delete","name":"Core"}`); r.Status != result.Unknown {
		t.Errorf("a tag that is not there is unknown: %s", r.Status)
	}
}

func TestEditNetworkRefusesWhatItShould(t *testing.T) {
	routes := map[string]fwdtest.Handler{"GET /api/networks": fwdtest.Const(200, []any{}), "GET /api/networks/n1/locations": fwdtest.Const(200, []any{map[string]any{"id": "1", "name": "Atlanta"}})}
	for _, bad := range []string{
		`{"object":"nope","action":"create"}`, `{"object":"network","action":"explode"}`, `{"object":"network","action":"create","network_id":"n1","name":"x"}`,
		`{"network_id":"n1","object":"location","action":"assign","definition":{"r1":"99"}}`, `{"network_id":"n1","object":"location","action":"create","definition":{"name":"Atlanta","lat":1,"lng":2}}`,
		`{"network_id":"n1","object":"location","action":"create","definition":{"name":"X","bogus":1}}`,
	} {
		if _, _, err := runSkill(t, "edit-network", routes, bad); err == nil {
			t.Errorf("%s must be refused", bad)
		}
	}
}

func TestEditNetworkLocationDeleteNeedsTheIDAndIsReadBack(t *testing.T) {
	locs := []any{map[string]any{"id": "7", "name": "Atlanta"}}
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/locations":      func(*http.Request, []byte) (int, any) { return 200, locs },
		"DELETE /api/networks/n1/locations/7": func(*http.Request, []byte) (int, any) { locs = nil; return 204, nil },
	}
	r, _ := mustRun(t, "edit-network", routes, `{"network_id":"n1","object":"location","action":"delete","name":"atlanta"}`)
	if r.Changes[0].Reversible || !strings.Contains(r.Finding, `confirm="7"`) {
		t.Fatalf("dry run: %s", r.Finding)
	}
	if _, _, err := runSkill(t, "edit-network", routes, `{"network_id":"n1","object":"location","action":"delete","name":"Atlanta","apply":true}`); err == nil || locs == nil {
		t.Errorf("apply without confirm is refused")
	}
	if r, _ := mustRun(t, "edit-network", routes, `{"network_id":"n1","object":"location","action":"delete","name":"Atlanta","apply":true,"confirm":"7"}`); r.Status != result.OK || locs != nil {
		t.Errorf("apply: %s", r.Finding)
	}
}
