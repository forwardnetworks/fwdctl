package skills_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

func TestEditPlatformWebhookUpdateIsADryRunThenReadsBack(t *testing.T) {
	hook := map[string]any{"name": "ops", "url": "https://hooks.example/a", "enabled": true}
	routes := map[string]fwdtest.Handler{
		"GET /api/webhooks":        func(*http.Request, []byte) (int, any) { return 200, map[string]any{"webhooks": []any{hook}} },
		"PATCH /api/webhooks/ops":  func(*http.Request, []byte) (int, any) { hook["enabled"] = false; return 200, nil },
		"DELETE /api/webhooks/ops": func(*http.Request, []byte) (int, any) { return 204, nil },
	}
	body := `{"area":"webhooks","action":"update","name":"ops","definition":{"enabled":false}`
	r, srv := mustRun(t, "edit-platform", routes, body+`}`)
	if r.Mode != result.ModeDryRun || writes(srv) != 0 {
		t.Fatalf("dry run: %s", r.Finding)
	}
	r, _ = mustRun(t, "edit-platform", routes, body+`,"apply":true}`)
	if r.Status != result.OK || hook["enabled"] != false {
		t.Fatalf("apply: %s %s", r.Status, r.Finding)
	}
	if r, _ := mustRun(t, "edit-platform", routes, `{"area":"webhooks","action":"delete","name":"ops"}`); r.Changes[0].Reversible || !strings.Contains(r.Finding, `confirm="ops"`) {
		t.Errorf("delete names its confirm: %s", r.Finding)
	}
	if _, _, err := runSkill(t, "edit-platform", routes, `{"area":"webhooks","action":"delete","name":"ops","apply":true}`); err == nil {
		t.Errorf("delete without confirm is refused")
	}
	if r, _ := mustRun(t, "edit-platform", routes, `{"area":"webhooks","action":"update","name":"gone","definition":{"enabled":true}}`); r.Status != result.Unknown {
		t.Errorf("a webhook that is not there is unknown: %s", r.Status)
	}
}

func TestEditPlatformCertificatesAddRefusesNonPEMAndApplyNeedsConfirm(t *testing.T) {
	certs := []any{}
	applied := false
	routes := map[string]fwdtest.Handler{
		"GET /api/trusted-certificates": func(*http.Request, []byte) (int, any) { return 200, certs },
		"POST /api/trusted-certificates": func(*http.Request, []byte) (int, any) {
			c := map[string]any{"name": "ca", "certificate": "x"}
			certs = append(certs, c)
			return 200, c
		},
	}
	routes["POST /api/trusted-certificates?action=apply"] = func(*http.Request, []byte) (int, any) { applied = true; return 200, []any{"t1"} }
	if _, _, err := runSkill(t, "edit-platform", routes, `{"area":"certificates","action":"add","definition":{"name":"ca","certificate":"not pem"}}`); err == nil {
		t.Errorf("a non-PEM certificate is refused")
	}
	r, _ := mustRun(t, "edit-platform", routes, `{"area":"certificates","action":"add","definition":{"name":"ca","certificate":"-----BEGIN CERTIFICATE-----\nAA\n-----END CERTIFICATE-----"},"apply":true}`)
	if r.Status != result.OK || len(certs) != 1 {
		t.Fatalf("add: %s %s", r.Status, r.Finding)
	}
	if _, _, err := runSkill(t, "edit-platform", routes, `{"area":"certificates","action":"apply","apply":true}`); err == nil || applied {
		t.Errorf("apply without confirm is refused before anything is sent")
	}
}

func TestEditPlatformBannerUpdateDisables(t *testing.T) {
	b := map[string]any{"id": "b1", "enabled": true, "message": "hi", "backgroundColor": "#fff", "networkIds": []any{"1"}}
	routes := map[string]fwdtest.Handler{
		"GET /api/custom-banners":    func(*http.Request, []byte) (int, any) { return 200, map[string]any{"banners": []any{b}} },
		"PUT /api/custom-banners/b1": func(*http.Request, []byte) (int, any) { b["enabled"] = false; return 200, nil },
	}
	r, _ := mustRun(t, "edit-platform", routes, `{"area":"banners","action":"update","name":"b1","definition":{"enabled":false},"apply":true}`)
	if r.Status != result.OK || b["enabled"] != false {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
}

func TestEditPlatformAccessLabelDeleteNeedsConfirmAndBackupDeleteNeedsTheID(t *testing.T) {
	labels := []any{map[string]any{"id": "l1", "name": "edge", "deviceGlobs": []any{"edge-*"}}}
	backups := []any{map[string]any{"id": 7, "storageType": "INTERNAL", "backupTimeMillis": 1700000000000}}
	deleted := map[string]bool{}
	routes := map[string]fwdtest.Handler{
		"GET /api/device-access-labels":       func(*http.Request, []byte) (int, any) { return 200, labels },
		"DELETE /api/device-access-labels/l1": func(*http.Request, []byte) (int, any) { deleted["label"] = true; labels = nil; return 204, nil },
		"GET /api/backups":                    func(*http.Request, []byte) (int, any) { return 200, backups },
		"DELETE /api/backups/7":               func(*http.Request, []byte) (int, any) { deleted["backup"] = true; backups = nil; return 204, nil },
	}
	r, srv := mustRun(t, "edit-platform", routes, `{"area":"access_labels","action":"delete","name":"edge"}`)
	if writes(srv) != 0 || r.Changes[0].Reversible || !strings.Contains(r.Finding, `confirm="edge"`) {
		t.Fatalf("dry run: %s", r.Finding)
	}
	if _, _, err := runSkill(t, "edit-platform", routes, `{"area":"access_labels","action":"delete","name":"edge","apply":true}`); err == nil || deleted["label"] {
		t.Errorf("apply without confirm is refused")
	}
	if r, _ := mustRun(t, "edit-platform", routes, `{"area":"access_labels","action":"delete","name":"edge","apply":true,"confirm":"edge"}`); r.Status != result.OK || !deleted["label"] {
		t.Errorf("label delete: %s", r.Finding)
	}
	if _, _, err := runSkill(t, "edit-platform", routes, `{"area":"backups","action":"delete","name":"latest"}`); err == nil {
		t.Errorf("a backup is named by its numeric id")
	}
	if r, _ := mustRun(t, "edit-platform", routes, `{"area":"backups","action":"delete","name":"7","apply":true,"confirm":"7"}`); r.Status != result.OK || !deleted["backup"] {
		t.Errorf("backup delete: %s", r.Finding)
	}
	if _, _, err := runSkill(t, "edit-platform", routes, `{"area":"access_labels","action":"create","definition":{"name":"x"}}`); err == nil {
		t.Errorf("a label that selects no devices is refused")
	}
}

const plantedPlatform = "PLANTED-PLATFORM-SECRET-5531"

func TestEditPlatformServiceNowSendsThePasswordOnceAndNeverReturnsIt(t *testing.T) {
	var sent string
	settings := map[string]any{}
	routes := map[string]fwdtest.Handler{
		"GET /api/integrations/servicenow": func(*http.Request, []byte) (int, any) {
			if len(settings) == 0 {
				return 404, map[string]any{"message": "none"}
			}
			return 200, settings
		},
		"PATCH /api/integrations/servicenow": func(_ *http.Request, b []byte) (int, any) {
			sent = string(b)
			settings = map[string]any{"instanceUrl": "https://x.service-now.com", "username": "svc", "enabled": true}
			return 200, nil
		},
	}
	f := secretFile(t, 0o600, plantedPlatform)
	def := `"name":"servicenow","area":"integrations","action":"set","definition":{"instance_url":"https://x.service-now.com","username":"svc"}`
	r, srv := mustRun(t, "edit-platform", routes, `{`+def+`,"secret_file":"`+f+`"}`)
	if writes(srv) != 0 || strings.Contains(jsonOf(r), plantedPlatform) {
		t.Fatalf("dry run sends nothing and shows no secret: %s", jsonOf(r))
	}
	r, _ = mustRun(t, "edit-platform", routes, `{`+def+`,"secret_file":"`+f+`","apply":true}`)
	if r.Status != result.OK || !strings.Contains(sent, plantedPlatform) || strings.Contains(jsonOf(r), plantedPlatform) {
		t.Fatalf("the password goes to Forward and not to the result: %s / %s", sent, jsonOf(r))
	}
	loose := secretFile(t, 0o644, plantedPlatform)
	for name, in := range map[string]string{
		"a password in the definition": `{"name":"servicenow","area":"integrations","action":"set","definition":{"username":"svc","password":"` + plantedPlatform + `"}}`,
		"a loose file":                 `{` + def + `,"secret_file":"` + loose + `","apply":true}`,
		"no secret":                    `{` + def + `}`,
		"a secret on another action":   `{"area":"banners","action":"create","secret_file":"` + f + `"}`,
	} {
		if _, _, err := runSkill(t, "edit-platform", routes, in); err == nil {
			t.Errorf("%s must be refused", name)
		} else if strings.Contains(err.Error(), plantedPlatform) {
			t.Errorf("%s: the error echoes the secret", name)
		}
	}
}
