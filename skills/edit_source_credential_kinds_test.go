package skills_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
)

// A CLI credential's kind is one of Forward's four (LOGIN, PRIVILEGED_MODE, EXPERT_MODE, SHELL; omitted means LOGIN), and the fields that only mean something for one kind are refused on the others,
// so a typo or a field on the wrong kind fails at the dry run, not as a 400 from Forward.
func TestEditSourceCLICredentialKindsAndTheFieldsEachTakes(t *testing.T) {
	routes := map[string]fwdtest.Handler{}
	f := t.TempDir() + "/s"
	for name, c := range map[string]struct{ def, want string }{
		"unknown kind":               {`{"type":"CLI","name":"x","username":"u","kind":"ENABLE"}`, "LOGIN, PRIVILEGED_MODE, EXPERT_MODE, SHELL"},
		"login without username":     {`{"type":"CLI","name":"x"}`, "username"},
		"shell without username":     {`{"type":"CLI","name":"x","kind":"SHELL"}`, "username"},
		"privilege level on a login": {`{"type":"CLI","name":"x","username":"u","privilegeLevel":15}`, "privilegeLevel"},
		"privileged id on enable":    {`{"type":"CLI","name":"x","kind":"PRIVILEGED_MODE","privilegedModePasswordId":"p1"}`, "privilegedModePasswordId"},
	} {
		_, _, err := runSkill(t, "edit-source", routes, `{"network_id":"n1","object":"credential","action":"create","definition":`+c.def+`,"secret_file":"`+f+`"}`)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want an error naming %q, got %v", name, c.want, err)
		}
	}
	for name, def := range map[string]string{
		"login":      `{"type":"CLI","name":"x","username":"u"}`,
		"enable":     `{"type":"CLI","name":"x","kind":"privileged_mode","privilegeLevel":15}`,
		"expert":     `{"type":"CLI","name":"x","kind":"EXPERT_MODE"}`,
		"login+link": `{"type":"CLI","name":"x","username":"u","privilegedModePasswordId":"p1"}`,
	} {
		r, _, err := runSkill(t, "edit-source", routes, `{"network_id":"n1","object":"credential","action":"create","definition":`+def+`,"secret_file":"`+f+`"}`)
		if err != nil || r.Status != "ok" {
			t.Errorf("%s should plan: %v %v", name, err, r.Status)
		}
	}
}

// A classic device takes its second credential (the enable password) and the per-device collection options on create, and the same on update; values are checked against Forward's enums at the dry run.
func TestEditSourceClassicDeviceSendsTheEnableCredentialAndCollectionOptions(t *testing.T) {
	var body, patch map[string]any
	var devs []any
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/classic-devices": func(*http.Request, []byte) (int, any) { return 200, devs },
		"POST /api/networks/n1/classic-devices": func(_ *http.Request, b []byte) (int, any) {
			_ = json.Unmarshal(b, &body)
			devs = []any{map[string]any{"name": "r1", "host": "10.0.0.1"}}
			return 201, map[string]any{"name": "r1"}
		},
		"PATCH /api/networks/n1/classic-devices/r1": func(_ *http.Request, b []byte) (int, any) {
			_ = json.Unmarshal(b, &patch)
			return 200, map[string]any{"name": "r1"}
		},
	}
	opts := `"cliCredentialId":"L-1","cliCredential2Id":"PM-2","enableSnmpCollection":true,"collectBgpAdvertisements":true,"bgpTableType":"BOTH","bgpPeerType":"BOTH","bgpAndBmpFailureHandling":"SWITCH_TO_CLI","fullCollectionLog":true,"disableIpv6Collection":false`
	r, _ := mustRun(t, "edit-source", routes, `{"network_id":"n1","object":"classic_device","action":"create","apply":true,"definition":{"name":"r1","host":"10.0.0.1",`+opts+`}}`)
	if r.Status != "ok" || body["cliCredential2Id"] != "PM-2" || body["bgpAndBmpFailureHandling"] != "SWITCH_TO_CLI" || body["collectBgpAdvertisements"] != true || body["fullCollectionLog"] != true || body["bgpTableType"] != "BOTH" {
		t.Fatalf("create body: %v (%s)", body, r.Finding)
	}
	r, _ = mustRun(t, "edit-source", routes, `{"network_id":"n1","object":"classic_device","action":"update","name":"r1","apply":true,"definition":{"cliCredential2Id":"PM-3","snmpCredentialId":"S-1","bgpPeerType":"EBGP","fullCollectionLog":false,"collectBgpAdvertisements":true}}`)
	if patch["cliCredential2Id"] != "PM-3" || patch["snmpCredentialId"] != "S-1" || patch["bgpPeerType"] != "EBGP" || patch["fullCollectionLog"] != false {
		t.Fatalf("patch body: %v (%s)", patch, r.Finding)
	}
	if _, _, err := runSkill(t, "edit-source", routes, `{"network_id":"n1","object":"classic_device","action":"update","name":"r1","definition":{"bgpPeerType":"IBGP"}}`); err == nil || !strings.Contains(err.Error(), "collectBgpAdvertisements") {
		t.Errorf("a BGP peer type without advertisement collection should be refused: %v", err)
	}
	for name, def := range map[string]string{"bgpTableType": `"bgpTableType":"ALL"`, "bgpPeerType": `"bgpPeerType":"X"`, "bgpAndBmpFailureHandling": `"bgpAndBmpFailureHandling":"EXPLODE"`} {
		if _, _, err := runSkill(t, "edit-source", routes, `{"network_id":"n1","object":"classic_device","action":"update","name":"r1","definition":{`+def+`}}`); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: a value outside Forward's enum should be refused naming the field: %v", name, err)
		}
	}
}
