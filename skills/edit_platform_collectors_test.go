package skills_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

// Registering a collector creates a credential Forward shows once. The key goes only to a file the caller names (mode 600, made before the registration so a bad path cannot lose the key), as the
// TOKEN line a Docker collector's --env-file takes; the result carries the id, name and username and never the key. A dry run registers nothing and touches no file.
func platformCollectorRoutes(registered *[]string, existing []any) map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		"GET /api/collectors": func(*http.Request, []byte) (int, any) { return 200, map[string]any{"collectors": existing} },
		"POST /api/collectors": func(_ *http.Request, body []byte) (int, any) {
			var b map[string]string
			_ = json.Unmarshal(body, &b)
			*registered = append(*registered, b["collectorName"])
			existing = append(existing, map[string]any{"id": "C9", "name": b["collectorName"], "username": "collector-ab12cd34"})
			return 200, map[string]any{"id": "C9", "name": b["collectorName"], "username": "collector-ab12cd34", "authorizationKey": "collector-ab12cd34:s3cretpasswordvalue"}
		},
		"DELETE /api/collectors/C9": func(*http.Request, []byte) (int, any) { return 204, nil },
	}
}

func TestEditPlatformCollectorRegisterWritesTheKeyOnlyToA600FileAndNeverReturnsIt(t *testing.T) {
	key := filepath.Join(t.TempDir(), "collector.env")
	var reg []string
	in := `{"area":"collectors","action":"register","definition":{"name":"lab","env_file":"` + key + `"}`
	r, _ := mustRun(t, "edit-platform", platformCollectorRoutes(&reg, nil), in+`}`)
	if len(reg) != 0 || r.Changes[0].Applied {
		t.Fatalf("a dry run registered: %v %+v", reg, r.Changes)
	}
	if _, err := os.Stat(key); err == nil {
		t.Fatalf("a dry run created the key file")
	}
	r, _ = mustRun(t, "edit-platform", platformCollectorRoutes(&reg, nil), in+`,"apply":true}`)
	b, _ := os.ReadFile(key)
	st, _ := os.Stat(key)
	if r.Status != result.OK || len(reg) != 1 || reg[0] != "lab" || string(b) != "TOKEN=collector-ab12cd34:s3cretpasswordvalue\n" || st.Mode().Perm() != 0o600 {
		t.Fatalf("%s %s reg=%v file=%q", r.Status, r.Finding, reg, b)
	}
	all, _ := json.Marshal(r)
	if strings.Contains(string(all), "s3cretpasswordvalue") {
		t.Fatalf("the key is in the result: %s", all)
	}
	if !strings.Contains(string(all), "collector-ab12cd34") || !strings.Contains(string(all), "C9") {
		t.Errorf("the result should name the id and username: %s", all)
	}
}

func TestEditPlatformCollectorRegisterRefusesWhatWouldLoseTheKey(t *testing.T) {
	dir := t.TempDir()
	var reg []string
	existing := filepath.Join(dir, "have.env")
	_ = os.WriteFile(existing, []byte("x"), 0o600)
	for name, in := range map[string]string{
		"no env_file":           `{"area":"collectors","action":"register","definition":{"name":"lab"},"apply":true}`,
		"env_file exists":       `{"area":"collectors","action":"register","definition":{"name":"lab","env_file":"` + existing + `"},"apply":true}`,
		"env_file dir missing":  `{"area":"collectors","action":"register","definition":{"name":"lab","env_file":"` + filepath.Join(dir, "no", "k") + `"},"apply":true}`,
		"no name":               `{"area":"collectors","action":"register","definition":{"env_file":"` + filepath.Join(dir, "k") + `"},"apply":true}`,
		"name already in use":   `{"area":"collectors","action":"register","definition":{"name":"lab","env_file":"` + filepath.Join(dir, "k2") + `"},"apply":true}`,
		"key given in the body": `{"area":"collectors","action":"register","definition":{"name":"lab","env_file":"` + filepath.Join(dir, "k3") + `","password":"x"},"apply":true}`,
	} {
		routes := platformCollectorRoutes(&reg, nil)
		if name == "name already in use" {
			routes = platformCollectorRoutes(&reg, []any{map[string]any{"id": "C1", "name": "lab", "username": "collector-x"}})
		}
		if _, _, err := runSkill(t, "edit-platform", routes, in); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if len(reg) != 0 {
		t.Errorf("a refused call still registered: %v", reg)
	}
	if b, _ := os.ReadFile(existing); string(b) != "x" {
		t.Errorf("an existing file was overwritten")
	}
}

func TestEditPlatformCollectorDeleteNeedsTheNameAsConfirmAndSaysItIsNotReversible(t *testing.T) {
	var reg []string
	routes := platformCollectorRoutes(&reg, []any{map[string]any{"id": "C9", "name": "lab", "username": "collector-ab12cd34"}})
	r, srv := mustRun(t, "edit-platform", routes, `{"area":"collectors","action":"delete","name":"lab"}`)
	if writes(srv) != 0 || r.Changes[0].Reversible || !strings.Contains(r.Finding, `confirm="lab"`) {
		t.Fatalf("dry run: %s %+v", r.Finding, r.Changes)
	}
	if _, _, err := runSkill(t, "edit-platform", routes, `{"area":"collectors","action":"delete","name":"lab","apply":true}`); err == nil {
		t.Errorf("apply without confirm was accepted")
	}
}
