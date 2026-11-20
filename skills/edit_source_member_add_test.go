package skills_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

// controller_setup update with add_managed_device or add_controller appends ONE member: the skill reads the live list and writes it back with the one member added; nothing is restated by hand. After the
// write it reads the setup again: each other member must be identical to what was read, and the added member must keep its collect flag.
type ctlAddWorld struct {
	setup              map[string]any
	classic            []any
	patched            []byte
	deleted            []string
	dropField          bool // the fake loses a field of an existing member in the write, like a whole-list write that drops what it does not know
	dropAdded          bool // the fake loses the added member's collect flag
	keepRemoved        bool // the fake accepts a delete but keeps the member
	mutateOther        bool // the fake changes another member while deleting
	patchFails         bool // the PATCH answers 400
	classicDeleteFails bool
	changeOnSecondRead bool // someone else adds a member after the plan has read the setup, before the apply re-reads it (the second list call)
	reads              int
}

func (w *ctlAddWorld) routes() map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/controller-managed-setups": func(*http.Request, []byte) (int, any) {
			w.reads++
			if w.changeOnSecondRead && w.reads == 2 {
				md := w.setup["managedDevices"].([]any)
				w.setup["managedDevices"] = append(md, map[string]any{"name": "intruder", "type": "viptela", "cliCredentialId": "L1"})
			}
			return 200, []any{w.setup}
		},
		"GET /api/networks/n1/classic-devices": func(*http.Request, []byte) (int, any) { return 200, w.classic },
		"DELETE /api/networks/n1/classic-devices/old1": func(*http.Request, []byte) (int, any) {
			if w.classicDeleteFails {
				return 500, map[string]any{"message": "boom"}
			}
			w.deleted = append(w.deleted, "old1")
			return 204, nil
		},
		"DELETE /api/networks/n1/controller-managed-setups/sdwan/managed-devices/edge2": func(*http.Request, []byte) (int, any) {
			w.deleted = append(w.deleted, "edge2")
			if !w.keepRemoved {
				md := w.setup["managedDevices"].([]any)
				w.setup["managedDevices"] = md[:1]
				if w.mutateOther {
					delete(md[0].(map[string]any), "largeRtt")
				}
			}
			return 204, nil
		},
		"DELETE /api/networks/n1/controller-managed-setups/sdwan/controllers/ctl": func(*http.Request, []byte) (int, any) {
			w.deleted = append(w.deleted, "ctl")
			return 204, nil
		},
		"PATCH /api/networks/n1/controller-managed-setups/sdwan": func(_ *http.Request, b []byte) (int, any) {
			if w.patchFails {
				return 400, map[string]any{"message": "Device name(s) already used: [x]"}
			}
			w.patched = b
			var in map[string]any
			_ = json.Unmarshal(b, &in)
			if ctls, ok := in["controllers"].([]any); ok {
				w.setup["controllers"] = ctls
			}
			if md, ok := in["managedDevices"].([]any); ok {
				for i, m := range md {
					mm := m.(map[string]any)
					if w.dropField && i == 0 {
						delete(mm, "largeRtt")
					}
					if w.dropAdded && i == len(md)-1 {
						delete(mm, "collect")
					}
				}
				w.setup["managedDevices"] = md
			}
			return 200, w.setup
		},
	}
}

func newCtlAddWorld() *ctlAddWorld {
	return &ctlAddWorld{
		setup: map[string]any{"name": "sdwan", "controllers": []any{map[string]any{"name": "ctl", "host": "192.0.2.10"}},
			"managedDevices": []any{
				map[string]any{"name": "edge1", "type": "viptela", "host": "192.0.2.21", "cliCredentialId": "L1", "collect": true, "largeRtt": true},
				map[string]any{"name": "edge2", "type": "viptela", "host": "192.0.2.22", "cliCredentialId": "L1", "collect": true},
			}},
		classic: []any{map[string]any{"name": "old1", "host": "192.0.2.31", "type": "viptela", "cliCredentialId": "L1"}},
	}
}

const addIn = `{"network_id":"n1","object":"controller_setup","action":"update","name":"sdwan","definition":{"add_managed_device":{"name":"edge3","type":"viptela","host":"192.0.2.23","cliCredentialId":"L1","collect":false}`

func TestAddManagedDeviceAppendsOneMemberAndReadsEveryOtherMemberBack(t *testing.T) {
	w := newCtlAddWorld()
	r, srv := mustRun(t, "edit-source", w.routes(), addIn+`}}`)
	if r.Mode != result.ModeDryRun || writes(srv) != 0 || w.patched != nil || !strings.Contains(r.Finding, "add managed device") || !strings.Contains(jsonOf(r), `"managed_devices":2`) {
		t.Fatalf("dry run writes nothing and says what it adds: %s %s", r.Mode, r.Finding)
	}
	r, _ = mustRun(t, "edit-source", w.routes(), addIn+`},"apply":true}`)
	if r.Status != result.OK {
		t.Fatalf("apply: %s %s", r.Status, r.Finding)
	}
	var sent map[string][]map[string]any
	_ = json.Unmarshal(w.patched, &sent)
	md := sent["managedDevices"]
	if len(md) != 3 || md[0]["name"] != "edge1" || md[0]["largeRtt"] != true || md[2]["name"] != "edge3" || md[2]["collect"] != false {
		t.Fatalf("the whole list is written with the existing members as read and the new one last, collect:false kept: %v", md)
	}
}

func TestAddManagedDeviceFailsWhenTheWriteChangedAnotherMemberOrDroppedCollect(t *testing.T) {
	w := newCtlAddWorld()
	w.dropField = true
	r, _ := mustRun(t, "edit-source", w.routes(), addIn+`},"apply":true}`)
	if r.Status != result.Failed || !strings.Contains(jsonOf(r), "members_that_changed_in_the_write") || !strings.Contains(jsonOf(r), "m:edge1") {
		t.Errorf("a field lost from an untouched member is reported: %s %s", r.Status, r.Finding)
	}
	w = newCtlAddWorld()
	w.dropAdded = true
	r, _ = mustRun(t, "edit-source", w.routes(), addIn+`},"apply":true}`)
	if r.Status != result.Failed || !strings.Contains(jsonOf(r), "added_member_collect_read_back") {
		t.Errorf("a dropped collect flag on the added member is reported: %s %s", r.Status, r.Finding)
	}
}

func TestAddMemberRefusesADuplicateNameAmongMembersAndClassicDevices(t *testing.T) {
	w := newCtlAddWorld()
	for _, in := range []string{
		`{"network_id":"n1","object":"controller_setup","action":"update","name":"sdwan","definition":{"add_managed_device":{"name":"EDGE1","cliCredentialId":"L1"}}}`,
		`{"network_id":"n1","object":"controller_setup","action":"update","name":"sdwan","definition":{"add_managed_device":{"name":"ctl","cliCredentialId":"L1"}}}`,
		`{"network_id":"n1","object":"controller_setup","action":"update","name":"sdwan","definition":{"add_managed_device":{"name":"old1","cliCredentialId":"L1"}}}`,
		`{"network_id":"n1","object":"controller_setup","action":"update","name":"sdwan","definition":{"add_managed_device":{"name":"edge9"}}}`,
		`{"network_id":"n1","object":"controller_setup","action":"update","name":"sdwan","definition":{"add_managed_device":{"name":"x","cliCredentialId":"L1"},"add_controller":{"name":"y"}}}`,
		`{"network_id":"n1","object":"controller_setup","action":"update","name":"sdwan","definition":{"add_managed_device":{"name":"edge9","cliCredentialId":"L1"},"collector_id":"C1"}}`,
		`{"network_id":"n1","object":"controller_setup","action":"update","name":"sdwan","definition":{"add_managed_device":{"name":"edge9","cliCredentialId":"L1"},"replace_classic":true}}`,
	} {
		if _, _, err := runSkill(t, "edit-source", w.routes(), in); err == nil {
			t.Errorf("must be refused: %s", in)
		}
	}
}

func TestAddMemberReplaceClassicDeletesTheClassicDeviceFirstAndNeedsConfirm(t *testing.T) {
	w := newCtlAddWorld()
	in := `{"network_id":"n1","object":"controller_setup","action":"update","name":"sdwan","definition":{"add_managed_device":{"name":"old1","type":"viptela","host":"192.0.2.31","cliCredentialId":"L1"},"replace_classic":true}`
	r, srv := mustRun(t, "edit-source", w.routes(), in+`}`)
	if writes(srv) != 0 || len(w.deleted) != 0 || !strings.Contains(r.Finding, "AFTER deleting the classic device") || !strings.Contains(r.Finding, `confirm="old1"`) || !strings.Contains(jsonOf(r), "192.0.2.31") {
		t.Fatalf("dry run shows the classic device it would delete: %s", r.Finding)
	}
	if _, _, err := runSkill(t, "edit-source", w.routes(), in+`,"apply":true}`); err == nil || len(w.deleted) != 0 {
		t.Errorf("apply without confirm is refused before anything is sent")
	}
	r, _ = mustRun(t, "edit-source", w.routes(), in+`,"apply":true,"confirm":"old1"}`)
	if r.Status != result.OK || len(w.deleted) != 1 || w.patched == nil {
		t.Fatalf("%s %s deleted=%v", r.Status, r.Finding, w.deleted)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "NOT atomic") {
		t.Errorf("limits must say it is not atomic: %v", r.Limits)
	}
}

func TestAddControllerAppendsOneController(t *testing.T) {
	w := newCtlAddWorld()
	in := `{"network_id":"n1","object":"controller_setup","action":"update","name":"sdwan","definition":{"add_controller":{"name":"ctl2","host":"192.0.2.11","cliCredentialId":"L1"}},"apply":true}`
	r, _ := mustRun(t, "edit-source", w.routes(), in)
	var sent map[string][]map[string]any
	_ = json.Unmarshal(w.patched, &sent)
	if r.Status != result.OK || len(sent["controllers"]) != 2 || sent["controllers"][1]["name"] != "ctl2" || sent["controllers"][0]["name"] != "ctl" {
		t.Errorf("controllers: %v (status %s %s)", sent["controllers"], r.Status, r.Finding)
	}
}

// A long member list is cut at 100 rows: the cut is declared as an omission, not only implied by devices_total.
func TestControllerSetupViewDeclaresWhenItsMemberListIsCapped(t *testing.T) {
	var members []any
	for i := 0; i < 130; i++ {
		members = append(members, map[string]any{"name": "e" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + strings.Repeat("y", i/26), "type": "viptela", "host": "192.0.2.1", "cliCredentialId": "L1"})
	}
	setup := map[string]any{"name": "sdwan", "controllers": []any{map[string]any{"name": "ctl", "host": "192.0.2.10"}}, "managedDevices": members}
	routes := map[string]fwdtest.Handler{
		snapsPath: ready("s1"),
		"GET /api/networks/n1/controller-managed-setups": fwdtest.Const(200, []any{setup}),
	}
	r, _ := mustRun(t, "inspect-collection", routes, `{"network_id":"n1","view":"config"}`)
	if !strings.Contains(jsonOf(r), `"devices_capped":true`) || !strings.Contains(jsonOf(r), `"devices_total":131`) || len(r.Omitted) == 0 {
		t.Errorf("the capped member list is declared: omitted=%v", r.Omitted)
	}
}

const rmIn = `{"network_id":"n1","object":"controller_setup","action":"update","name":"sdwan","definition":{"remove_managed_device":"edge2"}`

func TestRemoveManagedDeviceDeletesOneMemberWithConfirmAndChecksTheOthers(t *testing.T) {
	w := newCtlAddWorld()
	r, srv := mustRun(t, "edit-source", w.routes(), rmIn+`}`)
	if writes(srv) != 0 || len(w.deleted) != 0 || !strings.Contains(r.Finding, `confirm="edge2"`) || !strings.Contains(jsonOf(r), "192.0.2.22") {
		t.Fatalf("dry run shows the member and writes nothing: %s", r.Finding)
	}
	if _, _, err := runSkill(t, "edit-source", w.routes(), rmIn+`,"apply":true}`); err == nil || len(w.deleted) != 0 {
		t.Errorf("apply needs confirm")
	}
	r, _ = mustRun(t, "edit-source", w.routes(), rmIn+`,"apply":true,"confirm":"edge2"}`)
	if r.Status != result.OK || len(w.deleted) != 1 {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	w2 := newCtlAddWorld()
	w2.keepRemoved = true
	if r, _ := mustRun(t, "edit-source", w2.routes(), rmIn+`,"apply":true,"confirm":"edge2"}`); r.Status != result.Failed || !strings.Contains(jsonOf(r), "member_still_present") {
		t.Errorf("a delete Forward accepted but did not do is reported: %s", r.Status)
	}
}

func TestRemoveMemberRefusesWhatForwardWouldOrWhatDoesNotExist(t *testing.T) {
	w := newCtlAddWorld()
	for _, in := range []string{
		`{"network_id":"n1","object":"controller_setup","action":"update","name":"sdwan","definition":{"remove_managed_device":"nope"}}`,
		`{"network_id":"n1","object":"controller_setup","action":"update","name":"sdwan","definition":{"remove_controller":"ctl"}}`, // the only controller
		`{"network_id":"n1","object":"controller_setup","action":"update","name":"sdwan","definition":{"remove_managed_device":"edge2","remove_controller":"ctl"}}`,
		`{"network_id":"n1","object":"controller_setup","action":"update","name":"sdwan","definition":{"remove_managed_device":"edge2","collector_id":"C1"}}`,
	} {
		if _, _, err := runSkill(t, "edit-source", w.routes(), in); err == nil {
			t.Errorf("must be refused: %s", in)
		}
	}
}

func TestRemoveManagedDeviceReportsAnotherMemberThatChanged(t *testing.T) {
	w := newCtlAddWorld()
	w.mutateOther = true
	r, _ := mustRun(t, "edit-source", w.routes(), rmIn+`,"apply":true,"confirm":"edge2"}`)
	if r.Status != result.Failed || !strings.Contains(jsonOf(r), "members_that_changed") || !strings.Contains(jsonOf(r), "m:edge1") {
		t.Errorf("%s %s", r.Status, r.Finding)
	}
}

func TestReplaceClassicReportsTheDeletedClassicDeviceWhenTheAddFails(t *testing.T) {
	w := newCtlAddWorld()
	w.patchFails = true
	in := `{"network_id":"n1","object":"controller_setup","action":"update","name":"sdwan","definition":{"add_managed_device":{"name":"old1","type":"viptela","host":"192.0.2.31","cliCredentialId":"L1"},"replace_classic":true},"apply":true,"confirm":"old1"}`
	r, _ := mustRun(t, "edit-source", w.routes(), in)
	if r.Status != result.Failed || len(w.deleted) != 1 || !strings.Contains(r.Finding, "WAS deleted") || !strings.Contains(jsonOf(r), "classic_device_deleted") || !strings.Contains(jsonOf(r), "192.0.2.31") {
		t.Fatalf("the classic device's definition must be in the result: %s %s", r.Status, r.Finding)
	}
	if !r.Changes[0].Applied {
		t.Errorf("the delete was applied and the result must say so")
	}
	w2 := newCtlAddWorld()
	w2.classicDeleteFails = true
	_, _, err := runSkill(t, "edit-source", w2.routes(), in)
	if err == nil || w2.patched != nil {
		t.Errorf("a failed classic delete stops before the write: err=%v patched=%v", err, w2.patched != nil)
	}
}

func TestAddStopsWithoutWritingWhenTheSetupChangedAfterThePlan(t *testing.T) {
	w := newCtlAddWorld()
	w.changeOnSecondRead = true
	_, _, err := runSkill(t, "edit-source", w.routes(), addIn+`},"apply":true}`)
	if err == nil || !strings.Contains(err.Error(), "changed since it was read") || w.patched != nil {
		t.Errorf("a concurrent change is not overwritten: err=%v written=%v", err, w.patched != nil)
	}
}

func TestRemoveControllerWorksWithTwoAndTheLastManagedDeviceIsRefused(t *testing.T) {
	w := newCtlAddWorld()
	w.setup["controllers"] = []any{map[string]any{"name": "ctl", "host": "192.0.2.10"}, map[string]any{"name": "ctl2", "host": "192.0.2.11"}}
	routes := w.routes()
	routes["DELETE /api/networks/n1/controller-managed-setups/sdwan/controllers/ctl2"] = func(*http.Request, []byte) (int, any) {
		w.setup["controllers"] = w.setup["controllers"].([]any)[:1]
		w.deleted = append(w.deleted, "ctl2")
		return 204, nil
	}
	r, _ := mustRun(t, "edit-source", routes, `{"network_id":"n1","object":"controller_setup","action":"update","name":"sdwan","definition":{"remove_controller":"ctl2"},"apply":true,"confirm":"ctl2"}`)
	if r.Status != result.OK || len(w.deleted) != 1 {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	w2 := newCtlAddWorld()
	w2.setup["managedDevices"] = w2.setup["managedDevices"].([]any)[:1]
	if _, _, err := runSkill(t, "edit-source", w2.routes(), `{"network_id":"n1","object":"controller_setup","action":"update","name":"sdwan","definition":{"remove_managed_device":"edge1"}}`); err == nil {
		t.Errorf("removing the only managed device is refused")
	}
}

func TestAddControllerChecksItsCollectFlagAndMisuseIsRefused(t *testing.T) {
	w := newCtlAddWorld()
	r, _ := mustRun(t, "edit-source", w.routes(), `{"network_id":"n1","object":"controller_setup","action":"update","name":"sdwan","definition":{"add_controller":{"name":"ctl2","host":"192.0.2.11","cliCredentialId":"L1","collect":false}},"apply":true}`)
	if r.Status != result.OK {
		t.Errorf("a controller added with collect:false reads back false: %s %s", r.Status, r.Finding)
	}
	for _, in := range []string{
		`{"network_id":"n1","object":"controller_setup","action":"update","name":"sdwan","definition":{"replace_classic":true,"managedDevices":[]}}`,
		`{"network_id":"n1","object":"controller_setup","action":"update","name":"sdwan","definition":{"add_managed_device":{"name":"z","cliCredentialId":"L1"},"managed_device":"edge1","set":{"note":"x"}}}`,
	} {
		if _, _, err := runSkill(t, "edit-source", newCtlAddWorld().routes(), in); err == nil {
			t.Errorf("must be refused: %s", in)
		}
	}
}

// The classic device is deleted under the spelling Forward stores, not the one the caller typed.
func TestReplaceClassicDeletesTheClassicDeviceUnderItsStoredSpelling(t *testing.T) {
	w := newCtlAddWorld()
	w.classic = []any{map[string]any{"name": "OLD1", "host": "192.0.2.31", "type": "viptela", "cliCredentialId": "L1"}}
	routes := w.routes()
	deleted := ""
	routes["DELETE /api/networks/n1/classic-devices/OLD1"] = func(*http.Request, []byte) (int, any) { deleted = "OLD1"; return 204, nil }
	in := `{"network_id":"n1","object":"controller_setup","action":"update","name":"sdwan","definition":{"add_managed_device":{"name":"old1","type":"viptela","host":"192.0.2.31","cliCredentialId":"L1"},"replace_classic":true},"apply":true,"confirm":"old1"}`
	if r, _ := mustRun(t, "edit-source", routes, in); r.Status != result.OK || deleted != "OLD1" {
		t.Errorf("%s deleted=%q", r.Status, deleted)
	}
}
