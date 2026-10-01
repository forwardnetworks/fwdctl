package skills_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

// epRoutes: network n1 has SNMP endpoints e1 and e2 on SNMP-10, and a CLI endpoint c1 on CLI-1. Creating a profile makes SNMP-99; a patch moves an endpoint.
func epRoutes(created *bool, moved map[string]string) map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/endpoints": func(*http.Request, []byte) (int, any) {
			pid := func(n string) string {
				if v, ok := moved[n]; ok {
					return v
				}
				return "SNMP-10"
			}
			return 200, []any{
				map[string]any{"type": "SNMP", "name": "e1", "host": "10.0.0.1", "profileId": pid("e1")},
				map[string]any{"type": "SNMP", "name": "e2", "host": "10.0.0.2", "profileId": pid("e2")},
				map[string]any{"type": "CLI", "name": "c1", "host": "10.0.0.3", "profileId": "CLI-1"}}
		},
		"GET /api/endpoint-profiles": func(*http.Request, []byte) (int, any) {
			ps := []any{
				map[string]any{"id": "SNMP-10", "name": "snmp-base", "type": "SNMP", "detectorOid": "1.3.6.1.2.1.1.2.0", "detectorPatterns": []string{"x"}, "customOids": []any{map[string]any{"name": "sysDescr", "oid": "1.3.6.1.2.1.1.1"}}},
				map[string]any{"id": "CLI-1", "name": "cli", "type": "CLI", "commandSets": []string{"UNIX"}}}
			if *created {
				ps = append(ps, map[string]any{"id": "SNMP-99", "name": "try", "type": "SNMP", "customOids": []any{map[string]any{"name": "sysDescr", "oid": "1.3.6.1.2.1.1.1"}, map[string]any{"name": "vendor_root", "oid": "1.3.6.1.4.1.10418.26"}}})
			}
			return 200, map[string]any{"profiles": ps}
		},
		"POST /api/endpoint-profiles": func(*http.Request, []byte) (int, any) {
			*created = true
			return 201, map[string]any{"id": "SNMP-99", "name": "try", "type": "SNMP", "customOids": []any{map[string]any{"name": "sysDescr", "oid": "1.3.6.1.2.1.1.1"}, map[string]any{"name": "vendor_root", "oid": "1.3.6.1.4.1.10418.26"}}}
		},
		"GET /api/endpoint-profiles/SNMP-99": fwdtest.Const(200, map[string]any{"id": "SNMP-99", "name": "try", "type": "SNMP", "customOids": []any{map[string]any{"name": "sysDescr", "oid": "1.3.6.1.2.1.1.1"}, map[string]any{"name": "vendor_root", "oid": "1.3.6.1.4.1.10418.26"}}}),
		"PATCH /api/networks/n1/endpoints/e1": func(_ *http.Request, body []byte) (int, any) {
			if strings.Contains(string(body), "SNMP-99") {
				moved["e1"] = "SNMP-99"
			}
			return 200, map[string]any{}
		},
	}
}

const epPlan = `{"network_id":"n1","create_profile":{"name":"try","copy_from":"SNMP-10","add_oids":[{"name":"vendor_root","oid":"1.3.6.1.4.1.10418.26"}]},"assign":{"endpoints":["e1"],"profile":"new"}`

func TestEditEndpointProfileDryRunShowsTheCreateAndTheMoveAndTheUndoWithoutWriting(t *testing.T) {
	created := false
	r, srv := mustRun(t, "edit-endpoint-profile", epRoutes(&created, map[string]string{}), epPlan+`}`)
	if r.Status != result.OK || r.Mode != result.ModeDryRun || len(r.Changes) != 2 || writes(srv) != 0 || created {
		t.Fatalf("%s %s changes=%d writes=%d", r.Status, r.Finding, len(r.Changes), writes(srv))
	}
	if r.Changes[0].Action != "create_endpoint_profile" || r.Changes[1].Before != "SNMP-10" || !strings.Contains(r.Finding, `assign {endpoints: e1, profile: "SNMP-10"}`) {
		t.Errorf("%+v %s", r.Changes, r.Finding)
	}
}

func TestEditEndpointProfileRefusesWhatCannotWork(t *testing.T) {
	created := false
	for name, in := range map[string]string{
		"type mismatch":      `{"network_id":"n1","assign":{"endpoints":["c1"],"profile":"SNMP-10"}}`,
		"unknown endpoint":   `{"network_id":"n1","assign":{"endpoints":["nope"],"profile":"SNMP-10"}}`,
		"copy from CLI":      `{"network_id":"n1","create_profile":{"name":"x","copy_from":"CLI-1","add_oids":[{"name":"a","oid":"1.2.3"}]}}`,
		"duplicate name":     `{"network_id":"n1","create_profile":{"name":"SNMP-BASE","copy_from":"SNMP-10","add_oids":[{"name":"a","oid":"1.2.3"}]}}`,
		"duplicate oid name": `{"network_id":"n1","create_profile":{"name":"x","copy_from":"SNMP-10","add_oids":[{"name":"sysdescr","oid":"1.2.3"}]}}`,
		"delete in use":      `{"network_id":"n1","delete_profile":"SNMP-10"}`,
	} {
		r, srv := mustRun(t, "edit-endpoint-profile", epRoutes(&created, map[string]string{}), in)
		if r.Status != result.Failed || writes(srv) != 0 || !strings.HasPrefix(r.Finding, "Refused") {
			t.Errorf("%s: %s %s", name, r.Status, r.Finding)
		}
	}
	if _, _, err := runSkill(t, "edit-endpoint-profile", epRoutes(&created, map[string]string{}), `{"network_id":"n1","create_profile":{"name":"x","copy_from":"SNMP-10","add_oids":[{"name":"a","oid":"not-an-oid"}]}}`); err == nil {
		t.Error("a non-numeric OID must be refused as invalid input")
	}
}

func TestEditEndpointProfileApplyCreatesAssignsAndReadsBack(t *testing.T) {
	created, moved := false, map[string]string{}
	r, srv := mustRun(t, "edit-endpoint-profile", epRoutes(&created, moved), epPlan+`,"apply":true}`)
	if r.Status != result.OK || !created || moved["e1"] != "SNMP-99" || !r.Changes[0].Applied || !r.Changes[1].Applied {
		t.Fatalf("%s %s created=%v moved=%v", r.Status, r.Finding, created, moved)
	}
	if writes(srv) != 2 {
		t.Errorf("one create and one assign, got %d writes", writes(srv))
	}
}
