package skills_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

func renameRoutes(patch *map[string]any, renamed *bool) map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/classic-devices": func(*http.Request, []byte) (int, any) {
			name := "r1"
			if *renamed {
				name = "r2"
			}
			return 200, []any{map[string]any{"name": name, "host": "10.0.0.1"}, map[string]any{"name": "Taken", "host": "10.0.0.9"}}
		},
		"PATCH /api/networks/n1/classic-devices/r1": func(_ *http.Request, b []byte) (int, any) {
			_ = json.Unmarshal(b, patch)
			*renamed = true
			return 200, map[string]any{"name": "r2"}
		},
	}
}

// A classic device is renamed in place by PATCH: the old name is the key, the new name rides in the body. The dry run checks the characters and uniqueness (ignoring case) before any request.
func TestEditSourceClassicDeviceRenameInPlace(t *testing.T) {
	var patch map[string]any
	renamed := false
	in := `{"network_id":"n1","object":"classic_device","action":"update","name":"r1","definition":{"name":"r2"}`
	r, srv := mustRun(t, "edit-source", renameRoutes(&patch, &renamed), in+`}`)
	l := strings.Join(r.Limits, " ")
	if r.Mode != result.ModeDryRun || writes(srv) != 0 || !strings.Contains(r.Changes[0].Target, `rename device "r1" to "r2"`) || !strings.Contains(l, "next collection") || !strings.Contains(r.Changes[0].Undo, "rename it back") {
		t.Fatalf("dry run: %s %v %s", r.Finding, r.Changes, l)
	}
	r, _ = mustRun(t, "edit-source", renameRoutes(&patch, &renamed), in+`,"apply":true}`)
	if r.Status != result.OK || patch["name"] != "r2" {
		t.Fatalf("apply: %s %v", r.Finding, patch)
	}
	for name, bad := range map[string]string{"duplicate ignoring case": "taken", "invalid characters": "r 2!"} {
		if _, _, err := runSkill(t, "edit-source", renameRoutes(&patch, new(bool)), `{"network_id":"n1","object":"classic_device","action":"update","name":"r1","definition":{"name":"`+bad+`"}}`); err == nil {
			t.Errorf("%s must be refused before any request", name)
		}
	}
}

// The session options and thresholds reach the patch, and the values Forward answers 400 to are refused up front.
func TestEditSourceClassicDeviceNewPatchFieldsAreChecked(t *testing.T) {
	var patch map[string]any
	run := func(def string) error {
		_, _, err := runSkill(t, "edit-source", renameRoutes(&patch, new(bool)), `{"network_id":"n1","object":"classic_device","action":"update","name":"r1","apply":true,"definition":`+def+`}`)
		return err
	}
	if err := run(`{"paginationMode":"DISABLE_PAGINATION","terminalWidth":200,"prompt":"*confirm*","promptResponse":"y","highCpuUsage":0.9,"mediumCpuUsage":0.8,"bgpSubnetsToCollect":["10.0.0.0/8"],"collectBgpAdvertisements":true}`); err != nil {
		t.Fatal(err)
	}
	if patch["paginationMode"] != "DISABLE_PAGINATION" || patch["terminalWidth"] != 200.0 || patch["promptResponse"] != "y" || patch["highCpuUsage"] == nil || patch["bgpSubnetsToCollect"] == nil {
		t.Errorf("patch: %v", patch)
	}
	for name, bad := range map[string]string{
		"prompt without response":  `{"prompt":"x"}`,
		"width out of range":       `{"terminalWidth":20}`,
		"medium above high":        `{"highCpuUsage":0.8,"mediumCpuUsage":0.9}`,
		"high below range":         `{"highMemoryUsage":0.5}`,
		"zero connections":         `{"maxConnectionsPerMin":0}`,
		"unknown pagination value": `{"paginationMode":"MAYBE"}`,
	} {
		if err := run(bad); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
}
