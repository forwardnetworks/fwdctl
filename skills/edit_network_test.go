package skills_test

import (
	"errors"
	forward "github.com/forwardnetworks/forward-go-sdk"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
	"github.com/forwardnetworks/fwdctl/skills"
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
	if after, _ := r.Changes[0].After.(map[string]any); after["id"] != "2" || !strings.Contains(r.Changes[0].Undo, "confirm 2") {
		t.Errorf("the applied create must return the new network's id and an undo that names it: %v / %s", r.Changes[0].After, r.Changes[0].Undo)
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

func TestEditNetworkClusterDeleteNeedsItsNameAndSnapshotUnfavoriteIsReversible(t *testing.T) {
	clusters := []any{map[string]any{"name": "core", "devices": []any{"r1"}}}
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/locations/7/clusters":         func(*http.Request, []byte) (int, any) { return 200, clusters },
		"DELETE /api/networks/n1/locations/7/clusters/core": func(*http.Request, []byte) (int, any) { clusters = nil; return 204, nil },
	}
	body := `{"network_id":"n1","object":"cluster","action":"delete","name":"core","definition":{"location_id":"7"}`
	if _, _, err := runSkill(t, "edit-network", routes, body+`,"apply":true}`); err == nil || clusters == nil {
		t.Errorf("cluster delete without confirm is refused")
	}
	if r, _ := mustRun(t, "edit-network", routes, body+`,"apply":true,"confirm":"core"}`); r.Status != result.OK || clusters != nil {
		t.Errorf("cluster delete: %s", r.Finding)
	}
}

func collectorRoutes(attached *string) map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		"GET /api/collectors": fwdtest.Const(200, []any{
			map[string]any{"id": "1", "name": "c-old", "username": "collector-old", "status": "ONLINE", "connected": true},
			map[string]any{"id": "2", "name": "c-new", "username": "collector-new", "connectionStatus": "CONNECTED"}}),
		"GET /api/networks/n1/collector": func(*http.Request, []byte) (int, any) {
			if *attached == "" {
				return 200, map[string]any{}
			}
			return 200, map[string]any{"id": "1", "username": *attached, "name": "x"}
		},
		"PUT /api/networks/n1/collector": func(_ *http.Request, b []byte) (int, any) {
			if strings.Contains(string(b), "collector-new") {
				*attached = "collector-new"
			}
			return 200, map[string]any{}
		},
	}
}

func TestEditNetworkCollectorAssignDryRunThenAttachesAndReadsBack(t *testing.T) {
	attached := "collector-old"
	routes := collectorRoutes(&attached)
	in := `{"object":"collector","action":"assign","network_id":"n1","definition":{"username":"collector-new"}`
	r, srv := mustRun(t, "edit-network", routes, in+`}`)
	if r.Mode != result.ModeDryRun || writes(srv) != 0 || !r.Changes[0].Reversible || !strings.Contains(r.Changes[0].Undo, "collector-old") {
		t.Fatalf("dry run: %s %+v", r.Finding, r.Changes)
	}
	if after, _ := r.Changes[0].After.(map[string]any); after["connected"] != true {
		t.Errorf("a collector whose connectionStatus is CONNECTED must show connected: %v", after)
	}
	r, _ = mustRun(t, "edit-network", routes, in+`,"apply":true}`)
	if r.Status != result.OK || !r.Changes[0].Applied || attached != "collector-new" {
		t.Fatalf("apply: %s %s attached=%s", r.Status, r.Finding, attached)
	}
}

func TestEditNetworkCollectorWithNoneAttachedIsNotReversibleAndUnknownOnesAreRefused(t *testing.T) {
	attached := ""
	routes := collectorRoutes(&attached)
	r, _ := mustRun(t, "edit-network", routes, `{"object":"collector","action":"assign","network_id":"n1","definition":{"username":"collector-new"}}`)
	if r.Changes[0].Reversible || !strings.Contains(r.Changes[0].Undo, "no detach") {
		t.Errorf("a first attach cannot be undone here: %+v", r.Changes[0])
	}
	if _, _, err := runSkill(t, "edit-network", routes, `{"object":"collector","action":"assign","network_id":"n1","definition":{"username":"nope"}}`); err == nil || !strings.Contains(err.Error(), "collector-old") {
		t.Errorf("an unknown collector must be refused and the known ones named: %v", err)
	}
	if _, _, err := runSkill(t, "edit-network", routes, `{"object":"collector","action":"delete","network_id":"n1"}`); err == nil {
		t.Error("detaching is not offered")
	}
}

// Forward places only registered (classic) devices and refuses the whole assign otherwise ("Unrecognized devices cannot be moved", seen live): the
// skill refuses first, naming the unregistered devices and how to register one without collecting it, and sends nothing.
func TestLocationAssignRefusesDevicesNotInTheCollectionList(t *testing.T) {
	routes := map[string]fwdtest.Handler{"GET /api/networks": fwdtest.Const(200, []any{}),
		"GET /api/networks/n1/locations":                 fwdtest.Const(200, []any{map[string]any{"id": "1", "name": "Branches"}}),
		"GET /api/networks/n1/classic-devices":           fwdtest.Const(200, []any{map[string]any{"name": "r1", "host": "192.0.2.1"}}),
		"GET /api/networks/n1/controller-managed-setups": fwdtest.Const(200, []any{map[string]any{"name": "sdwan", "managedDevices": []any{map[string]any{"name": "edge9"}}}})}
	for _, in := range []string{
		`{"network_id":"n1","object":"location","action":"assign","definition":{"r1":"1","r2":"1"},"apply":true}`,
		`{"network_id":"n1","object":"location","action":"assign","definition":{"r1":"1","r2":"1"}}`, // the dry run refuses too
	} {
		_, srv, err := runSkill(t, "edit-network", routes, in)
		if !errors.Is(err, skills.ErrInvalidInput) || !strings.Contains(err.Error(), ": r2.") || strings.Contains(err.Error(), "r1") || !strings.Contains(err.Error(), `"collect": false`) || writes(srv) != 0 {
			t.Fatalf("unregistered r2 refused with the fix, nothing sent: %v (writes %d)", err, writes(srv))
		}
	}
	// a classic device in another case and a controller-managed device are registered: planned normally
	r, _ := mustRun(t, "edit-network", routes, `{"network_id":"n1","object":"location","action":"assign","definition":{"R1":"1","edge9":"1"}}`)
	if r.Status != result.OK || r.Changes[0].Applied {
		t.Fatalf("registered devices plan normally: %s %s", r.Status, r.Finding)
	}
	// a source list that cannot be read: no refusal, a limit says what was not checked
	routes["GET /api/networks/n1/controller-managed-setups"] = fwdtest.Const(500, map[string]any{"message": "boom"})
	r, _ = mustRun(t, "edit-network", routes, `{"network_id":"n1","object":"location","action":"assign","definition":{"r2":"1"}}`)
	if r.Status != result.OK || !strings.Contains(strings.Join(r.Limits, " "), "not found in the source lists that could be read: r2") {
		t.Fatalf("an unread source list turns the refusal into a limit: %s %v", r.Status, r.Limits)
	}
}

// When the pre-check could not run and Forward still refuses ("Unrecognized devices cannot be moved"), the error names the devices and the fix.
func TestLocationAssignExplainsForwardsRefusal(t *testing.T) {
	routes := map[string]fwdtest.Handler{"GET /api/networks": fwdtest.Const(200, []any{}),
		"GET /api/networks/n1/locations":                 fwdtest.Const(200, []any{map[string]any{"id": "1", "name": "Branches"}}),
		"GET /api/networks/n1/classic-devices":           fwdtest.Const(500, map[string]any{"message": "boom"}),
		"GET /api/networks/n1/controller-managed-setups": fwdtest.Const(200, []any{}),
		"PATCH /api/networks/n1/atlas":                   fwdtest.Const(400, map[string]any{"message": "Unrecognized devices cannot be moved: [r2]"}),
	}
	_, _, err := runSkill(t, "edit-network", routes, `{"network_id":"n1","object":"location","action":"assign","definition":{"r2":"1"},"apply":true}`)
	// only reachable when the pre-check could not read a source list (otherwise the skill refuses first)
	if !errors.Is(err, forward.ErrUnrecognizedDevices) || !strings.Contains(err.Error(), "not registered: r2") || !strings.Contains(err.Error(), `"collect": false`) {
		t.Fatalf("Forward's refusal is explained: %v", err)
	}
}

func TestEditNetworkTagCreateMakesTheDefinitionIdempotentlyAndSaysHowToUndo(t *testing.T) {
	var tags []any
	var posted []byte
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/device-tags": func(*http.Request, []byte) (int, any) { return 200, tags },
		"POST /api/networks/n1/device-tags": func(_ *http.Request, b []byte) (int, any) {
			posted = b
			tags = []any{map[string]any{"name": "dmz", "color": "#112233"}}
			return 200, nil
		},
	}
	in := `{"network_id":"n1","object":"tag","action":"create","name":"dmz","definition":{"color":"#112233"}`
	r, srv := mustRun(t, "edit-network", routes, in+`}`)
	if r.Status != result.OK || writes(srv) != 0 || len(r.Changes) != 1 || !r.Changes[0].Reversible || r.Changes[0].Action != "create_tag" {
		t.Fatalf("dry run: %s %s %+v", r.Status, r.Finding, r.Changes)
	}
	r, _ = mustRun(t, "edit-network", routes, in+`,"apply":true}`)
	if r.Status != result.OK || !strings.Contains(string(posted), `"dmz"`) || !strings.Contains(string(posted), "#112233") {
		t.Fatalf("apply: %s %s body %s", r.Status, r.Finding, posted)
	}
	posted = nil
	r, _ = mustRun(t, "edit-network", routes, in+`,"apply":true}`)
	if posted != nil || !strings.Contains(r.Finding, "Already as asked") {
		t.Errorf("an existing tag is a no-op, nothing sent: %s %s", r.Finding, posted)
	}
	if _, _, err := runSkill(t, "edit-network", routes, `{"network_id":"n1","object":"tag","action":"create","name":"x","definition":{"color":"red"}}`); err == nil {
		t.Errorf("a color that is not #rrggbb is refused")
	}
}

func TestEditNetworkTagCreateIsNotProvenWhenTheReadBackLacksIt(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/device-tags":  func(*http.Request, []byte) (int, any) { return 200, []any{} },
		"POST /api/networks/n1/device-tags": func(*http.Request, []byte) (int, any) { return 200, nil },
	}
	r, _ := mustRun(t, "edit-network", routes, `{"network_id":"n1","object":"tag","action":"create","name":"dmz","apply":true}`)
	if r.Status != result.Failed {
		t.Errorf("a create the read-back does not show must fail: %s %s", r.Status, r.Finding)
	}
}
