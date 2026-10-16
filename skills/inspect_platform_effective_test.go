package skills_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

// view effective answers "what is non-default?": every collection setting with its value, Forward's default, whether it was set, and whether it differs, for the organization and each collector. A timeout nobody set
// is absent from Forward's answer, so the view says in plain words that absent means default.
func TestEffectiveCollectionSettingsFlagWhatDiffersFromTheDefault(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		"GET /api/collection-settings":               fwdtest.Const(200, map[string]any{"maxCommandAuthZPerSecond": 1000, "perDeviceConcurrencyBoost": 2, "perDeviceSnmpConcurrency": 1, "collectionRetries": 2, "collectionRetryDelayMillis": 30000, "collectionMaxRetryDelayMillis": 60000}),
		"GET /api/collectors":                        fwdtest.Const(200, []any{map[string]any{"id": "C1", "name": "alpha"}, map[string]any{"id": "C2", "name": "bravo"}}),
		"GET /api/collectors/C1/collection-settings": fwdtest.Const(200, map[string]any{"concurrency": 1024}),
		"GET /api/collectors/C2/collection-settings": fwdtest.Const(200, map[string]any{}),
	}
	r, srv := mustRun(t, "inspect-platform", routes, `{"area":"collection_settings","view":"effective","limit":200}`)
	b := jsonOf(r)
	if r.Status != result.OK || writes(srv) != 0 {
		t.Fatalf("%s %s", r.Status, b)
	}
	var rows []map[string]any
	for _, x := range r.Evidence[0].Detail["rows"].([]map[string]any) {
		rows = append(rows, x)
	}
	find := func(scope, setting string) map[string]any {
		for _, m := range rows {
			if m["scope"] == scope && m["setting"] == setting {
				return m
			}
		}
		t.Fatalf("no row %s / %s in %s", scope, setting, b)
		return nil
	}
	if m := find("organization", "collectionRetryDelayMillis"); fmt.Sprint(m["value"]) != "30000" || fmt.Sprint(m["default"]) != "15000" || m["source"] != "set" || m["differs"] != true {
		t.Errorf("retry delay: %v", m)
	}
	if m := find("organization", "collectionRetries"); m["differs"] != false || m["source"] != "set" {
		t.Errorf("a value set to the default does not differ: %v", m)
	}
	if m := find("organization", "deviceCollectionTimeoutMinutes"); fmt.Sprint(m["value"]) != "180" || m["source"] != "default" || m["differs"] != false {
		t.Errorf("an absent timeout is the default: %v", m)
	}
	if m := find("organization", "perDeviceConcurrencyBoost"); fmt.Sprint(m["default"]) != "0" || m["differs"] != true {
		t.Errorf("boost: %v", m)
	}
	if m := find("collector alpha", "concurrency"); fmt.Sprint(m["value"]) != "1024" || fmt.Sprint(m["default"]) != "128" || m["differs"] != true {
		t.Errorf("collector concurrency: %v", m)
	}
	if m := find("collector bravo", "concurrency"); m["source"] != "default" || m["differs"] != false {
		t.Errorf("collector default: %v", m)
	}
	for _, want := range []string{"4 setting(s) differ", "absent", "support authority"} {
		if !strings.Contains(b, want) {
			t.Errorf("missing %q in %s", want, b)
		}
	}
	if _, _, err := runSkill(t, "inspect-platform", routes, `{"area":"credentials","network_id":"n1","view":"effective"}`); err == nil || !strings.Contains(err.Error(), "collection_settings") {
		t.Errorf("view effective belongs to collection_settings: %v", err)
	}
}
