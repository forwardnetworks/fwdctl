package skills_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
)

// A PATCH of an AWS setup that carries `regions` makes Forward record every region as a freshly passed connectivity test (it clears the regions and re-adds them with a success result), turning a real per-region
// authorization failure into NONE. No cloud_account update may send regions (or any property it was not asked to change), whatever the field changed. The raw PATCH body of every variant is checked.
func TestNoCloudAccountUpdateEverSendsRegionsOrAnythingItWasNotAskedToChange(t *testing.T) {
	variants := map[string]struct {
		def     string
		secret  string
		allowed []string
	}{
		"collect":          {`{"collect":false}`, "", []string{"type", "collect"}},
		"accounts_enabled": {`{"accounts_enabled":{"222222222222":true}}`, "", []string{"type", "assumeRoleInfos"}},
		"external ids":     {`{"accounts_external_ids":["111111111111"]}`, `{"111111111111":"v"}`, []string{"type", "assumeRoleInfos"}},
		"accounts_add":     {`{"accounts_add":["444444444444"]}`, "", []string{"type", "assumeRoleInfos"}},
		"add newly":        {`{"add_newly_discovered":true}`, "", []string{"type", "assumeRoleInfos"}},
	}
	for name, v := range variants {
		w := newMemberWorld()
		var raw string
		routes := w.routes()
		inner := routes["PATCH /api/networks/n1/cloudAccounts/aws-main"]
		routes["PATCH /api/networks/n1/cloudAccounts/aws-main"] = func(r *http.Request, b []byte) (int, any) {
			raw = string(b)
			return inner(r, b)
		}
		secret := ""
		if v.secret != "" {
			secret = `,"secret_file":"` + secretFile(t, 0o600, v.secret) + `"`
		}
		mustRun(t, "edit-source", routes, `{"network_id":"n1","object":"cloud_account","action":"update","name":"aws-main","apply":true,"definition":`+v.def+secret+`}`)
		if raw == "" {
			t.Errorf("%s: nothing was patched", name)
			continue
		}
		if strings.Contains(raw, `"regions"`) {
			t.Errorf("%s: the PATCH body carries regions: %s", name, raw)
		}
		var keys map[string]json.RawMessage
		_ = json.Unmarshal([]byte(raw), &keys)
		for k := range keys {
			ok := false
			for _, a := range v.allowed {
				ok = ok || a == k
			}
			if !ok {
				t.Errorf("%s: the PATCH body has a property that was not asked for: %q in %s", name, k, raw)
			}
		}
	}
	_ = fwdtest.Const
}
