package skills_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

// Built-in collection commands can be disabled org-wide (Forward's disabledCommands, per device type, with a version range). Forward replaces the whole map on a patch, so the skill adds or removes single
// entries by reading the map, changing a copy and sending all of it back, re-reading right before the write. A dry run writes nothing; the result names the org-wide reach and the cautions.
type disabledWorld struct {
	cur     map[string][]map[string]any
	patched map[string][]map[string]any
}

func (w *disabledWorld) routes() map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		"GET /api/collection-settings": func(*http.Request, []byte) (int, any) { return 200, map[string]any{"disabledCommands": w.cur} },
		"PATCH /api/collection-settings": func(_ *http.Request, b []byte) (int, any) {
			var p struct {
				D map[string][]map[string]any `json:"disabledCommands"`
			}
			_ = json.Unmarshal(b, &p)
			w.patched = p.D
			w.cur = p.D
			return 200, map[string]any{"disabledCommands": w.cur}
		},
	}
}

func newDisabledWorld() *disabledWorld {
	return &disabledWorld{cur: map[string][]map[string]any{
		"f5_ssh": {{"command": "show sys foo", "version": map[string]any{"start": "1.0", "end": "2.0"}}},
	}}
}

func TestDisablingACommandAddsOneEntryAndResendsTheRestUnchanged(t *testing.T) {
	w := newDisabledWorld()
	in := `{"area":"collection_settings","action":"set","name":"organization","definition":{"disable_commands":[{"device_type":"panos_ssh","command":"request system external-list show .*"}]}`
	r, srv := mustRun(t, "edit-platform", w.routes(), in+`}`)
	if writes(srv) != 0 || w.patched != nil || r.Changes[0].Applied {
		t.Fatalf("dry run wrote: %v", w.patched)
	}
	all := strings.Join(r.Limits, " ")
	for _, want := range []string{"organization", "external-list", "COMMAND_DISABLED", "26.8.3", "custom command group"} {
		if !strings.Contains(strings.ToLower(all), strings.ToLower(want)) {
			t.Errorf("the dry run should mention %q: %v", want, r.Limits)
		}
	}
	r, _ = mustRun(t, "edit-platform", w.routes(), in+`,"apply":true}`)
	if r.Status != result.OK || len(w.patched["panos_ssh"]) != 1 || len(w.patched["f5_ssh"]) != 1 {
		t.Fatalf("%s %s patched=%v", r.Status, r.Finding, w.patched)
	}
	e := w.patched["panos_ssh"][0]
	v := e["version"].(map[string]any)
	if e["command"] != "request system external-list show .*" || v["start"] != "0" || v["end"] != "99999" {
		t.Errorf("all versions by default: %v", e)
	}
}

func TestEnablingACommandRemovesOnlyThatEntryAndAMissingOneIsRefused(t *testing.T) {
	w := newDisabledWorld()
	w.cur["panos_ssh"] = []map[string]any{{"command": "request system external-list show .*", "version": map[string]any{"start": "0", "end": "99999"}}, {"command": "show foo", "version": map[string]any{"start": "0", "end": "99999"}}}
	r, _ := mustRun(t, "edit-platform", w.routes(), `{"area":"collection_settings","action":"set","name":"organization","apply":true,"definition":{"enable_commands":[{"device_type":"panos_ssh","command":"request system external-list show .*"}]}}`)
	if r.Status != result.OK || len(w.patched["panos_ssh"]) != 1 || w.patched["panos_ssh"][0]["command"] != "show foo" || len(w.patched["f5_ssh"]) != 1 {
		t.Fatalf("%s %s %v", r.Status, r.Finding, w.patched)
	}
	w2 := newDisabledWorld()
	if _, _, err := runSkill(t, "edit-platform", w2.routes(), `{"area":"collection_settings","action":"set","name":"organization","apply":true,"definition":{"enable_commands":[{"device_type":"panos_ssh","command":"nothing"}]}}`); err == nil || w2.patched != nil {
		t.Errorf("enabling a command that is not disabled must be refused: %v", err)
	}
}

func TestDisablingTheSameCommandAgainIsAlreadyDoneAndBadInputIsRefused(t *testing.T) {
	w := newDisabledWorld()
	r, srv := mustRun(t, "edit-platform", w.routes(), `{"area":"collection_settings","action":"set","name":"organization","apply":true,"definition":{"disable_commands":[{"device_type":"f5_ssh","command":"show sys foo","version_start":"1.0","version_end":"2.0"}]}}`)
	if !strings.Contains(strings.ToLower(r.Finding), "already") || writes(srv) != 0 {
		t.Errorf("an identical entry changes nothing: %s", r.Finding)
	}
	for name, def := range map[string]string{
		"no device type":     `{"disable_commands":[{"command":"x"}]}`,
		"no command":         `{"disable_commands":[{"device_type":"panos_ssh"}]}`,
		"mixed with a limit": `{"disable_commands":[{"device_type":"panos_ssh","command":"x"}],"command_delay_ms":5}`,
		"bad regex":          `{"disable_commands":[{"device_type":"panos_ssh","command":"(unclosed"}]}`,
	} {
		w := newDisabledWorld()
		if _, _, err := runSkill(t, "edit-platform", w.routes(), `{"area":"collection_settings","action":"set","name":"organization","apply":true,"definition":`+def+`}`); err == nil || w.patched != nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
