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
