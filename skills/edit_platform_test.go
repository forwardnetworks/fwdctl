package skills_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
	"github.com/forwardnetworks/fwdctl/skills"
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

func TestEditPlatformCollectionSettingsSamlAndTokens(t *testing.T) {
	org := map[string]any{"commandDelayMs": 0, "deviceCollectionTimeoutMinutes": 60}
	saml := any(nil)
	toks := []any{map[string]any{"name": "ci", "accessKey": "AK1"}}
	routes := map[string]fwdtest.Handler{
		"GET /api/collection-settings":   func(*http.Request, []byte) (int, any) { return 200, org },
		"PATCH /api/collection-settings": func(*http.Request, []byte) (int, any) { org["commandDelayMs"] = 50; return 200, nil },
		"GET /api/auth/saml-settings":    func(*http.Request, []byte) (int, any) { return 200, saml },
		"PUT /api/auth/saml-settings": func(_ *http.Request, _ []byte) (int, any) {
			saml = map[string]any{"customName": "idp", "samlAuthSettings": map[string]any{"enabled": true, "entityId": "e1", "ssoRedirectUrl": "https://idp/sso", "verificationCert": "x"}}
			return 200, nil
		},
		"GET /api/users/current/tokens":       func(*http.Request, []byte) (int, any) { return 200, map[string]any{"tokens": toks} },
		"DELETE /api/users/current/tokens/ci": func(*http.Request, []byte) (int, any) { toks = nil; return 204, nil },
	}
	r, _ := mustRun(t, "edit-platform", routes, `{"area":"collection_settings","action":"set","name":"organization","definition":{"command_delay_ms":50},"apply":true}`)
	if r.Status != result.OK || org["commandDelayMs"] != 50 {
		t.Fatalf("org settings: %s %s", r.Status, r.Finding)
	}
	if _, _, err := runSkill(t, "edit-platform", routes, `{"area":"collection_settings","action":"set","name":"organization","definition":{}}`); err == nil {
		t.Errorf("a set that changes nothing is refused")
	}
	cert := `-----BEGIN CERTIFICATE-----\nAA\n-----END CERTIFICATE-----`
	body := `{"area":"saml","action":"set","definition":{"custom_name":"idp","enabled":true,"entity_id":"e1","sso_redirect_url":"https://idp/sso","verification_cert":"` + cert + `"}`
	if _, _, err := runSkill(t, "edit-platform", routes, body+`,"apply":true}`); err == nil || saml != nil {
		t.Errorf("saml without confirm is refused before anything is sent")
	}
	if r, _ := mustRun(t, "edit-platform", routes, body+`,"apply":true,"confirm":"saml"}`); r.Status != result.OK || saml == nil {
		t.Errorf("saml: %s", r.Finding)
	}
	if _, _, err := runSkill(t, "edit-platform", routes, `{"area":"saml","action":"set","definition":{"custom_name":"idp","enabled":true}}`); err == nil {
		t.Errorf("enabled saml needs its entity, URL and certificate")
	}
	if _, _, err := runSkill(t, "edit-platform", routes, `{"area":"api_tokens","action":"create","name":"new"}`); err == nil {
		t.Errorf("creating a token is not offered")
	}
	if r, _ := mustRun(t, "edit-platform", routes, `{"area":"api_tokens","action":"delete","name":"ci","apply":true,"confirm":"ci"}`); r.Status != result.OK || toks != nil {
		t.Errorf("token delete: %s", r.Finding)
	}
}

func TestEditPlatformOrganizationDeleteNeedsItsNameAndLicenseKeyIsNeverReturned(t *testing.T) {
	orgs := []any{map[string]any{"id": "o1", "name": "acme"}}
	licensed := false
	routes := map[string]fwdtest.Handler{
		"GET /api/admin/orgs":       func(*http.Request, []byte) (int, any) { return 200, orgs },
		"DELETE /api/admin/orgs/o1": func(*http.Request, []byte) (int, any) { orgs = nil; return 200, map[string]any{"id": "o1"} },
		"POST /api/licenses": func(r *http.Request, _ []byte) (int, any) {
			licensed = licensed || r.URL.Query().Get("action") != "decode"
			return 200, map[string]any{"id": "L1", "status": "VALID"}
		},
		"GET /api/licenses": func(*http.Request, []byte) (int, any) { return 200, []any{} },
	}
	if _, _, err := runSkill(t, "edit-platform", routes, `{"area":"organizations","action":"delete","name":"acme","apply":true}`); err == nil || orgs == nil {
		t.Errorf("delete without confirm is refused")
	}
	// no deleter set: a plain build has no direct delete, so the apply is refused and nothing is removed
	if _, _, err := runSkill(t, "edit-platform", routes, `{"area":"organizations","action":"delete","name":"acme","apply":true,"confirm":"acme"}`); err == nil || !strings.Contains(err.Error(), "not allowed by the host") || orgs == nil {
		t.Errorf("without a deleter the apply is refused: %v", err)
	}
	// a host-supplied deleter is the only way an organization is removed
	sess, _ := fwdtest.New(t, routes)
	sess.OrganizationDeleter = func(context.Context, string) error { orgs = nil; return nil }
	if r, err := skills.Run(context.Background(), "edit-platform", sess, json.RawMessage(`{"area":"organizations","action":"delete","name":"acme","apply":true,"confirm":"acme"}`)); err != nil || r.Status != result.OK || orgs != nil {
		t.Errorf("delete through the injected deleter: %v %+v", err, r)
	}
	f := secretFile(t, 0o600, plantedPlatform)
	if _, _, err := runSkill(t, "edit-platform", routes, `{"area":"licensing","action":"apply","secret_file":"`+f+`","apply":true}`); err == nil || licensed {
		t.Errorf("a license apply without confirm is refused")
	}
}

func TestEditPlatformCVEIndexDeleteIsRefusedWhenBundledAndNeedsConfirm(t *testing.T) {
	meta := map[string]any{"digest": "d1", "indexUploadedAt": "2026-10-01T00:00:00Z"}
	deleted := false
	routes := map[string]fwdtest.Handler{
		"GET /api/cve-index":    func(*http.Request, []byte) (int, any) { return 200, meta },
		"DELETE /api/cve-index": func(*http.Request, []byte) (int, any) { deleted = true; meta["indexUploadedAt"] = ""; return 202, nil },
	}
	if _, _, err := runSkill(t, "edit-platform", routes, `{"area":"cve_index","action":"delete","apply":true}`); err == nil || deleted {
		t.Errorf("delete without confirm is refused")
	}
	if r, _ := mustRun(t, "edit-platform", routes, `{"area":"cve_index","action":"delete","apply":true,"confirm":"cve_index"}`); r.Status != result.OK || !deleted {
		t.Errorf("delete: %s", r.Finding)
	}
	if r, _ := mustRun(t, "edit-platform", routes, `{"area":"cve_index","action":"delete"}`); r.Status != result.Unknown {
		t.Errorf("nothing uploaded means nothing to delete: %s", r.Status)
	}
	if _, _, err := runSkill(t, "edit-platform", routes, `{"area":"cve_index","action":"upload","definition":{"path":"/nonexistent/x.gz"}}`); err == nil {
		t.Errorf("a missing file is refused")
	}
}
