package skills_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

const planted2 = "PLANTED-DEVICE-PASSWORD-9912"

func secretFile(t *testing.T, mode os.FileMode, text string) string {
	f := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(f, []byte(text+"\n"), mode); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(f, mode)
	return f
}

func sourceWorld(created *[]map[string]any) map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/cli-credentials": func(*http.Request, []byte) (int, any) { return 200, *created },
		"POST /api/networks/n1/cli-credentials": func(_ *http.Request, _ []byte) (int, any) {
			c := map[string]any{"id": "c9", "name": "ops", "type": "LOGIN", "username": "admin", "password": "secret-id-1"}
			*created = append(*created, c)
			return 201, c
		},
	}
}

func TestEditSourceCredentialDryRunReadsNoSecretAndApplySendsItOnceWithoutEchoingIt(t *testing.T) {
	var created []map[string]any
	routes := sourceWorld(&created)
	f := secretFile(t, 0o600, planted2)
	in := `{"network_id":"n1","object":"credential","action":"create","definition":{"type":"CLI","name":"ops","username":"admin"},"secret_file":"` + f + `"`
	r, srv := mustRun(t, "edit-source", routes, in+`}`)
	if r.Mode != result.ModeDryRun || writes(srv) != 0 || strings.Contains(jsonOf(r), planted2) || !strings.Contains(jsonOf(r), "secret") {
		t.Fatalf("dry run: %s", jsonOf(r))
	}
	r, srv = mustRun(t, "edit-source", routes, in+`,"apply":true}`)
	b := jsonOf(r)
	if r.Status != result.OK || len(created) != 1 || strings.Contains(b, planted2) {
		t.Fatalf("apply: %s %s", r.Status, b)
	}
	sent := false
	for _, c := range srv.Calls() {
		if c.Method == "POST" && strings.Contains(fmtAny(c.Body), planted2) {
			sent = true
		}
	}
	if !sent {
		t.Errorf("the secret must reach Forward exactly in the create request")
	}
}

func TestEditSourceRefusesASecretInTheInputALooseFileAndAMissingSecret(t *testing.T) {
	var created []map[string]any
	routes := sourceWorld(&created)
	loose := secretFile(t, 0o644, planted2)
	for name, in := range map[string]string{
		"a password in the definition": `{"network_id":"n1","object":"credential","action":"create","definition":{"type":"CLI","name":"ops","password":"` + planted2 + `"}}`,
		"a loose secret file":          `{"network_id":"n1","object":"credential","action":"create","definition":{"type":"CLI","name":"ops"},"secret_file":"` + loose + `","apply":true}`,
		"no secret on apply":           `{"network_id":"n1","object":"credential","action":"create","definition":{"type":"CLI","name":"ops"},"apply":true}`,
		"delete without confirm":       `{"network_id":"n1","object":"credential","action":"delete","name":"c9","definition":{"type":"CLI"},"apply":true}`,
	} {
		_, srv, err := runSkill(t, "edit-source", routes, in)
		if err == nil || writes(srv) != 0 || len(created) != 0 {
			t.Errorf("%s must be refused before anything is sent: %v", name, err)
		} else if strings.Contains(err.Error(), planted2) {
			t.Errorf("%s: the error must not echo the secret: %v", name, err)
		}
	}
}

func TestEditSourceSnmpV3ReadsTwoNamedSecretsFromOneJSONFile(t *testing.T) {
	var body string
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/snmpCredentials": fwdtest.Const(200, []any{map[string]any{"id": "s1", "name": "poll"}}),
		"POST /api/networks/n1/snmpCredentials": func(_ *http.Request, b []byte) (int, any) {
			body = string(b)
			return 201, map[string]any{"id": "s1", "name": "poll"}
		},
	}
	f := secretFile(t, 0o600, `{"password":"AUTH-PW-1","privacyPassword":"PRIV-PW-2"}`)
	r, _ := mustRun(t, "edit-source", routes, `{"network_id":"n1","object":"credential","action":"create","apply":true,"secret_file":"`+f+`","definition":{"type":"SNMP","name":"poll","version":"V3","authUsername":"u","authType":"sha","privacyProtocol":"aes_128"}}`)
	if r.Status != result.OK || !strings.Contains(body, "AUTH-PW-1") || !strings.Contains(body, "PRIV-PW-2") || strings.Contains(jsonOf(r), "PW-") {
		t.Errorf("both secrets go to Forward and neither to the result: %s / %s", body, jsonOf(r))
	}
}

func TestEditSourceClassicDeviceAndScheduleAreDryRunsWithUndoAndConfirmForDelete(t *testing.T) {
	devs := []any{map[string]any{"name": "r1", "host": "10.0.0.1"}}
	deleted := false
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/classic-devices":        func(*http.Request, []byte) (int, any) { return 200, devs },
		"DELETE /api/networks/n1/classic-devices/r1":  func(*http.Request, []byte) (int, any) { deleted = true; devs = nil; return 204, nil },
		"GET /api/networks/n1/collection-schedules":   fwdtest.Const(200, []any{map[string]any{"id": "7", "enabled": true, "timeZone": "UTC", "daysOfTheWeek": []int{1}, "times": []string{"02:00"}}}),
		"PUT /api/networks/n1/collection-schedules/7": fwdtest.Const(200, map[string]any{}),
	}
	r, srv := mustRun(t, "edit-source", routes, `{"network_id":"n1","object":"classic_device","action":"delete","name":"r1"}`)
	if writes(srv) != 0 || !r.Changes[0].Reversible || !strings.Contains(r.Finding, `confirm="r1"`) {
		t.Fatalf("dry run: %s", r.Finding)
	}
	if _, _, err := runSkill(t, "edit-source", routes, `{"network_id":"n1","object":"classic_device","action":"delete","name":"r1","apply":true}`); err == nil || deleted {
		t.Errorf("delete without confirm is refused")
	}
	r, _ = mustRun(t, "edit-source", routes, `{"network_id":"n1","object":"classic_device","action":"delete","name":"r1","apply":true,"confirm":"r1"}`)
	if r.Status != result.OK || !deleted {
		t.Errorf("apply: %s %s", r.Status, r.Finding)
	}
	r, srv = mustRun(t, "edit-source", routes, `{"network_id":"n1","object":"schedule","action":"update","name":"7","definition":{"timeZone":"America/Chicago","daysOfTheWeek":[1,2],"times":["03:00"]}}`)
	if r.Mode != result.ModeDryRun || writes(srv) != 0 || !strings.Contains(strings.Join(r.Limits, "|"), "restates EVERY field") {
		t.Errorf("schedule update is a dry run that says it replaces: %s", jsonOf(r))
	}
	if r, _ := mustRun(t, "edit-source", routes, `{"network_id":"n1","object":"schedule","action":"update","name":"99","definition":{"daysOfTheWeek":[1],"times":["03:00"]}}`); r.Status != result.Unknown {
		t.Errorf("a schedule that is not there is unknown: %s", r.Status)
	}
}

func TestEditSourceCloudAccountSecretGoesToForwardOnlyAndRotateReplacesCredentials(t *testing.T) {
	var created, rotated string
	accounts := []any{}
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/cloudAccounts": func(*http.Request, []byte) (int, any) { return 200, accounts },
		"POST /api/networks/n1/cloudAccounts": func(_ *http.Request, b []byte) (int, any) {
			created = string(b)
			accounts = append(accounts, map[string]any{"name": "prod-aws", "type": "AWS"})
			return 201, map[string]any{"name": "prod-aws", "type": "AWS"}
		},
		"POST /api/networks/n1/cloudAccounts/prod-aws/credential": func(_ *http.Request, b []byte) (int, any) { rotated = string(b); return 204, nil },
	}
	f := secretFile(t, 0o600, "AWS-SECRET-KEY-5521")
	in := `{"network_id":"n1","object":"cloud_account","action":"create","secret_file":"` + f + `","definition":{"type":"AWS","name":"prod-aws","username":"AKIAEXAMPLE","regions":{"us-east-1":1}}`
	r, srv := mustRun(t, "edit-source", routes, in+`}`)
	if writes(srv) != 0 || strings.Contains(jsonOf(r), "AWS-SECRET") {
		t.Fatalf("dry run: %s", jsonOf(r))
	}
	r, _ = mustRun(t, "edit-source", routes, in+`,"apply":true}`)
	if r.Status != result.OK || !strings.Contains(created, "AWS-SECRET-KEY-5521") || strings.Contains(jsonOf(r), "AWS-SECRET") {
		t.Fatalf("apply: %s created=%s", r.Status, created)
	}
	mustRun(t, "edit-source", routes, `{"network_id":"n1","object":"cloud_account","action":"rotate","name":"prod-aws","secret_file":"`+f+`","apply":true,"definition":{"type":"AWS","username":"AKIAEXAMPLE"}}`)
	if !strings.Contains(rotated, "AWS-SECRET-KEY-5521") {
		t.Errorf("rotate sends the secret to Forward's credential route: %q", rotated)
	}
	if _, _, err := runSkill(t, "edit-source", routes, `{"network_id":"n1","object":"cloud_account","action":"delete","name":"prod-aws","apply":true}`); err == nil {
		t.Errorf("delete needs confirm")
	}
}

func TestEditSourceRapid7UpdateReplacesAndTakesNoSecret(t *testing.T) {
	srcs := []any{map[string]any{"name": "r7", "baseUrl": "https://r7.example", "credentialId": "c1"}}
	var patched string
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/end-host-scanners": func(*http.Request, []byte) (int, any) { return 200, srcs },
		"PATCH /api/networks/n1/rapid7-sources/r7": func(_ *http.Request, b []byte) (int, any) {
			patched = string(b)
			srcs = []any{map[string]any{"name": "r7", "baseUrl": "https://r7.example", "credentialId": "c1", "collectionDisabled": true}}
			return 200, srcs[0]
		},
	}
	body := `{"network_id":"n1","object":"rapid7_source","action":"update","name":"r7","definition":{"baseUrl":"https://r7.example","credentialId":"c1","collectionDisabled":true}`
	if r, srv := mustRun(t, "edit-source", routes, body+`}`); writes(srv) != 0 || r.Mode != result.ModeDryRun {
		t.Fatalf("dry run: %s", r.Finding)
	}
	if r, _ := mustRun(t, "edit-source", routes, body+`,"apply":true}`); r.Status != result.OK || !strings.Contains(patched, "collectionDisabled") {
		t.Fatalf("apply: %s %s", r.Status, patched)
	}
	if _, _, err := runSkill(t, "edit-source", routes, `{"network_id":"n1","object":"rapid7_source","action":"update","name":"r7","definition":{"baseUrl":"https://r7.example"}}`); err == nil {
		t.Errorf("update REPLACES: a missing credentialId is refused")
	}
	if _, _, err := runSkill(t, "edit-source", routes, body+`,"secret_env":"X"}`); err == nil {
		t.Errorf("this object takes no secret")
	}
}

func TestEditSourceControllerAndMistSetups(t *testing.T) {
	ctrl := []any{}
	mist := []any{map[string]any{"name": "m1", "region": "GLOBAL_01"}}
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/controller-managed-setups": func(*http.Request, []byte) (int, any) { return 200, ctrl },
		"POST /api/networks/n1/controller-managed-setups": func(_ *http.Request, _ []byte) (int, any) {
			ctrl = []any{map[string]any{"name": "dnac", "controllers": []any{map[string]any{"name": "c1"}}}}
			return 201, ctrl[0]
		},
		"GET /api/networks/n1/cloud-managed-setups":       func(*http.Request, []byte) (int, any) { return 200, mist },
		"DELETE /api/networks/n1/cloud-managed-setups/m1": func(*http.Request, []byte) (int, any) { mist = nil; return 204, nil },
	}
	create := `{"network_id":"n1","object":"controller_setup","action":"create","definition":{"name":"dnac","controllers":[{"name":"c1","host":"10.0.0.9","cliCredentialId":"c7"}]}`
	if r, srv := mustRun(t, "edit-source", routes, create+`}`); writes(srv) != 0 || r.Mode != result.ModeDryRun {
		t.Fatalf("dry run: %s", r.Finding)
	}
	if r, _ := mustRun(t, "edit-source", routes, create+`,"apply":true}`); r.Status != result.OK || len(ctrl) != 1 {
		t.Fatalf("create: %s %s", r.Status, r.Finding)
	}
	if _, _, err := runSkill(t, "edit-source", routes, `{"network_id":"n1","object":"controller_setup","action":"create","definition":{"name":"x"}}`); err == nil {
		t.Errorf("a setup needs a controller")
	}
	if _, _, err := runSkill(t, "edit-source", routes, `{"network_id":"n1","object":"mist_setup","action":"delete","name":"m1","apply":true}`); err == nil || mist == nil {
		t.Errorf("delete without confirm is refused")
	}
	if r, _ := mustRun(t, "edit-source", routes, `{"network_id":"n1","object":"mist_setup","action":"delete","name":"m1","apply":true,"confirm":"m1"}`); r.Status != result.OK || mist != nil {
		t.Errorf("delete: %s", r.Finding)
	}
}

func TestEditSourceCredentialUpdateChangesFieldsAndRotatesTheSecretFromAFile(t *testing.T) {
	cred := map[string]any{"id": "c1", "name": "ops", "username": "admin"}
	var sent string
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/cli-credentials/c1": func(*http.Request, []byte) (int, any) { return 200, cred },
		"GET /api/networks/n1/cli-credentials":    func(*http.Request, []byte) (int, any) { return 200, []any{cred} },
		"PATCH /api/networks/n1/cli-credentials/c1": func(_ *http.Request, b []byte) (int, any) {
			sent = string(b)
			cred["username"] = "root"
			return 200, cred
		},
	}
	f := secretFile(t, 0o600, planted2)
	body := `{"network_id":"n1","object":"credential","action":"update","name":"c1","definition":{"type":"CLI","username":"root"},"secret_file":"` + f + `"`
	r, srv := mustRun(t, "edit-source", routes, body+`}`)
	if writes(srv) != 0 || r.Mode != result.ModeDryRun || strings.Contains(jsonOf(r), planted2) {
		t.Fatalf("dry run: %s", jsonOf(r))
	}
	r, _ = mustRun(t, "edit-source", routes, body+`,"apply":true}`)
	if r.Status != result.OK || !strings.Contains(sent, planted2) || !strings.Contains(sent, "root") || strings.Contains(jsonOf(r), planted2) {
		t.Fatalf("apply: %s sent=%s", r.Status, sent)
	}
	if _, _, err := runSkill(t, "edit-source", routes, `{"network_id":"n1","object":"credential","action":"update","name":"c1","definition":{"type":"CLI"}}`); err == nil {
		t.Errorf("an update that changes nothing is refused")
	}
}

func TestEditSourceJumpServerUpdateAndDeleteAndRapid7Delete(t *testing.T) {
	js := []any{map[string]any{"id": "j1", "host": "jump.example", "port": 22, "username": "ops"}}
	var patched string
	src := []any{map[string]any{"name": "r7", "baseUrl": "https://r7.example"}}
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/jumpServers": func(*http.Request, []byte) (int, any) { return 200, js },
		"PATCH /api/networks/n1/jumpServers/j1": func(_ *http.Request, b []byte) (int, any) {
			patched = string(b)
			js = []any{map[string]any{"id": "j1", "host": "jump2.example", "port": 22, "username": "ops"}}
			return 200, nil
		},
		"DELETE /api/networks/n1/jumpServers/j1":    func(*http.Request, []byte) (int, any) { js = nil; return 204, nil },
		"GET /api/networks/n1/end-host-scanners":    func(*http.Request, []byte) (int, any) { return 200, src },
		"DELETE /api/networks/n1/rapid7-sources/r7": func(*http.Request, []byte) (int, any) { src = nil; return 204, nil },
	}
	f := secretFile(t, 0o600, planted2)
	upd := `{"network_id":"n1","object":"jump_server","action":"update","name":"j1","definition":{"host":"jump2.example"},"secret_file":"` + f + `","apply":true}`
	r, _ := mustRun(t, "edit-source", routes, upd)
	if r.Status != result.OK || !strings.Contains(patched, planted2) || strings.Contains(jsonOf(r), planted2) {
		t.Fatalf("update: %s %s", r.Status, patched)
	}
	if _, _, err := runSkill(t, "edit-source", routes, `{"network_id":"n1","object":"jump_server","action":"delete","name":"j1","apply":true}`); err == nil || js == nil {
		t.Errorf("delete without confirm is refused")
	}
	if r, _ := mustRun(t, "edit-source", routes, `{"network_id":"n1","object":"jump_server","action":"delete","name":"j1","apply":true,"confirm":"j1"}`); r.Status != result.OK || js != nil {
		t.Errorf("jump delete: %s", r.Finding)
	}
	if r, _ := mustRun(t, "edit-source", routes, `{"network_id":"n1","object":"rapid7_source","action":"delete","name":"r7","apply":true,"confirm":"r7"}`); r.Status != result.OK || src != nil {
		t.Errorf("rapid7 delete: %s", r.Finding)
	}
}

func TestEditSourceClassicDeviceCreateAndUpdateSendCollectorID(t *testing.T) {
	var body, patch string
	var devs []any
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/classic-devices": func(*http.Request, []byte) (int, any) { return 200, devs },
		"POST /api/networks/n1/classic-devices": func(_ *http.Request, b []byte) (int, any) {
			body = string(b)
			devs = []any{map[string]any{"name": "r9", "host": "10.0.0.9", "collectorId": "C42"}}
			return 201, map[string]any{"name": "r9"}
		},
		"PATCH /api/networks/n1/classic-devices/r9": func(_ *http.Request, b []byte) (int, any) {
			patch = string(b)
			return 200, map[string]any{"name": "r9", "collectorId": "C7"}
		},
	}
	r, _ := mustRun(t, "edit-source", routes, `{"network_id":"n1","object":"classic_device","action":"create","apply":true,"definition":{"name":"r9","host":"10.0.0.9","collectorId":"C42"}}`)
	if r.Status != result.OK || !strings.Contains(body, `"collectorId":"C42"`) {
		t.Errorf("collectorId goes to Forward on create: %s / %s", body, jsonOf(r))
	}
	mustRun(t, "edit-source", routes, `{"network_id":"n1","object":"classic_device","action":"update","name":"r9","apply":true,"definition":{"collectorId":"C7"}}`)
	if !strings.Contains(patch, `"collectorId":"C7"`) {
		t.Errorf("collectorId goes to Forward on update: %q", patch)
	}
}
