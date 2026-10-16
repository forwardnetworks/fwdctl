package skills_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

func orgSettingsRoutes(org map[string]any, patches *[]string) map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		"GET /api/collection-settings": func(*http.Request, []byte) (int, any) { return 200, org },
		"PATCH /api/collection-settings": func(_ *http.Request, b []byte) (int, any) {
			*patches = append(*patches, string(b))
			var p map[string]any
			_ = json.Unmarshal(b, &p)
			for k, v := range p {
				if v == nil {
					delete(org, k)
				} else {
					org[k] = v
				}
			}
			return 200, nil
		},
	}
}

// The organization settings the retry policy and per-device SNMP concurrency can be set, and any setting can be put back by CLEARING it (a JSON null, so Forward's default applies and reads back as unset), which is
// not the same as writing the default's number. The dry run shows the before values and the default each would return to.
func TestEditPlatformSetsRetryAndSnmpSettingsAndRestoresDefaultsByClearing(t *testing.T) {
	org := map[string]any{"collectionRetryDelayMillis": 30000, "perDeviceSnmpConcurrency": 1, "collectionRetries": 2}
	var patches []string
	routes := orgSettingsRoutes(org, &patches)
	base := `{"area":"collection_settings","action":"set","name":"organization","definition":`

	r, srv := mustRun(t, "edit-platform", routes, base+`{"restore_default":["collection_retry_delay_ms","per_device_snmp_concurrency"]}}`)
	b := jsonOf(r)
	if r.Mode != result.ModeDryRun || writes(srv) != 0 || !strings.Contains(b, "15000") || !strings.Contains(b, "30000") || !strings.Contains(b, "clear") {
		t.Fatalf("the dry run shows before, default and that it clears: %s %s", r.Mode, b)
	}
	r, _ = mustRun(t, "edit-platform", routes, base+`{"restore_default":["collection_retry_delay_ms","per_device_snmp_concurrency"]},"apply":true}`)
	if r.Status != result.OK || len(patches) != 1 || !strings.Contains(patches[0], `"collectionRetryDelayMillis":null`) || !strings.Contains(patches[0], `"perDeviceSnmpConcurrency":null`) {
		t.Fatalf("apply: %s %s patches %v", r.Status, r.Finding, patches)
	}
	if _, set := org["collectionRetryDelayMillis"]; set || org["collectionRetries"] != 2 {
		t.Errorf("only the named settings are cleared: %v", org)
	}

	patches = nil
	r, _ = mustRun(t, "edit-platform", routes, base+`{"collection_retry_delay_ms":20000,"per_device_snmp_concurrency":2,"collection_retries":3,"collection_max_retry_delay_ms":90000},"apply":true}`)
	if r.Status != result.OK || org["collectionRetryDelayMillis"] != float64(20000) || org["perDeviceSnmpConcurrency"] != float64(2) || org["collectionRetries"] != float64(3) {
		t.Errorf("set: %s %s %v", r.Status, r.Finding, org)
	}

	for name, def := range map[string]string{
		"unknown":       `{"restore_default":["nope"]}`,
		"set and clear": `{"collection_retries":1,"restore_default":["collection_retries"]}`,
	} {
		if _, _, err := runSkill(t, "edit-platform", routes, base+def+`}`); err == nil {
			t.Errorf("%s: must be refused", name)
		}
	}
}
