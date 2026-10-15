package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
)

// `fwdctl source apply` takes a whole source inventory (profiles, credentials, devices, endpoints) and drives edit-platform and edit-source one item at a time. Dry run by default; each item is
// create, update or unchanged against what Forward holds; names are resolved to ids; secrets come only from the environment variable or file the inventory names.
type fakeForward struct {
	creds, devs, eps, profiles []any
	posted                     []string
}

func (f *fakeForward) routes() map[string]fwdtest.Handler {
	rec := func(tag string, list *[]any, mk func(map[string]any) map[string]any) fwdtest.Handler {
		return func(_ *http.Request, b []byte) (int, any) {
			var m map[string]any
			if json.Unmarshal(b, &m) != nil { // endpoints post a list
				var l []map[string]any
				_ = json.Unmarshal(b, &l)
				m = l[0]
			}
			f.posted = append(f.posted, tag+":"+m["name"].(string))
			*list = append(*list, mk(m))
			return 200, map[string]any{"id": "new", "name": m["name"]}
		}
	}
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/cli-credentials":  func(*http.Request, []byte) (int, any) { return 200, f.creds },
		"GET /api/networks/n1/http-credentials": fwdtest.Const(200, []any{}),
		"GET /api/networks/n1/snmpCredentials":  fwdtest.Const(200, []any{}),
		"POST /api/networks/n1/cli-credentials": rec("cred", &f.creds, func(m map[string]any) map[string]any {
			return map[string]any{"id": "L-" + m["name"].(string), "type": "LOGIN", "name": m["name"]}
		}),
		"GET /api/networks/n1/classic-devices":  func(*http.Request, []byte) (int, any) { return 200, f.devs },
		"POST /api/networks/n1/classic-devices": rec("dev", &f.devs, func(m map[string]any) map[string]any { return m }),
		"GET /api/networks/n1/endpoints":        func(*http.Request, []byte) (int, any) { return 200, f.eps },
		"POST /api/networks/n1/endpoints": rec("ep", &f.eps, func(m map[string]any) map[string]any {
			return map[string]any{"type": "CLI", "name": m["name"], "host": m["host"], "credentialId": m["credentialId"], "profileId": m["profileId"]}
		}),
		"GET /api/endpoint-profiles": func(*http.Request, []byte) (int, any) { return 200, f.profiles },
		"POST /api/endpoint-profiles": rec("profile", &f.profiles, func(m map[string]any) map[string]any {
			return map[string]any{"id": "CLI-" + m["name"].(string), "name": m["name"], "type": "CLI"}
		}),
		"POST /api/approved-cli-commands": fwdtest.Const(200, map[string]any{"approved": []any{}, "unapproved": []any{}}),
	}
}

const inventory = `{"network_id":"n1",
 "endpoint_profiles":[{"name":"linux","command_sets":["UNIX"]}],
 "credentials":[{"name":"lab-login","type":"CLI","username":"admin","secret_env":"LAB_PW"},{"name":"lab-enable","type":"CLI","kind":"PRIVILEGED_MODE","secret_env":"LAB_EN"}],
 "devices":[{"name":"r1","host":"10.0.0.1","credential":"lab-login","enable_credential":"lab-enable","bgpAndBmpFailureHandling":"SWITCH_TO_CLI"}],
 "endpoints":[{"name":"h1","host":"10.0.1.1","credential":"lab-login","profile":"linux"}]}`

func runInventory(t *testing.T, f *fakeForward, spec string, apply bool) (int, map[string]any, string) {
	t.Helper()
	sess, _ := fwdtest.New(t, f.routes())
	var out, errb bytes.Buffer
	code := sourceApply(context.Background(), sess, []byte(spec), "", apply, &out, &errb)
	var sum map[string]any
	_ = json.Unmarshal(out.Bytes(), &sum)
	return code, sum, errb.String()
}

func TestSourceApplyDryRunPlansEverythingAndWritesNothing(t *testing.T) {
	f := &fakeForward{}
	code, sum, errs := runInventory(t, f, inventory, false)
	counts, _ := sum["counts"].(map[string]any)
	if code != 0 || len(f.posted) != 0 || counts["create"] != float64(5) || sum["mode"] != "dry_run" {
		t.Fatalf("code %d posted %v counts %v err %s", code, f.posted, counts, errs)
	}
}

func TestSourceApplyCreatesInOrderResolvesNamesAndASecondRunChangesNothing(t *testing.T) {
	t.Setenv("LAB_PW", "pw1")
	t.Setenv("LAB_EN", "en1")
	f := &fakeForward{}
	code, sum, errs := runInventory(t, f, inventory, true)
	if code != 0 || strings.Join(f.posted, ",") != "profile:linux,cred:lab-login,cred:lab-enable,dev:r1,ep:h1" {
		t.Fatalf("code %d posted %v err %s sum %v", code, f.posted, errs, sum)
	}
	n := len(f.posted)
	code, sum, _ = runInventory(t, f, inventory, true)
	counts, _ := sum["counts"].(map[string]any)
	if code != 0 || len(f.posted) != n || counts["unchanged"] != float64(5) || counts["create"] != nil && counts["create"] != float64(0) {
		t.Fatalf("a second run should change nothing: code %d posted %v counts %v items %v", code, f.posted[n:], counts, sum["items"])
	}
}

func TestSourceApplyStopsAtTheFirstFailureAndRefusesSecretsInTheFile(t *testing.T) {
	t.Setenv("LAB_PW", "pw1") // LAB_EN is deliberately unset
	f := &fakeForward{}
	code, sum, _ := runInventory(t, f, inventory, true)
	counts, _ := sum["counts"].(map[string]any)
	if code != 1 || counts["failed"] != float64(1) || counts["skipped"] == nil || strings.Contains(strings.Join(f.posted, ","), "dev:") {
		t.Fatalf("a missing secret should fail that item and skip the rest: code %d posted %v counts %v", code, f.posted, counts)
	}
	code, _, errs := runInventory(t, &fakeForward{}, `{"network_id":"n1","credentials":[{"name":"x","type":"CLI","username":"u","password":"hunter2"}]}`, false)
	if code != 64 || !strings.Contains(errs, "password") {
		t.Fatalf("a secret in the file is refused: %d %s", code, errs)
	}
}

// An existing credential is "unchanged" without its secret being compared, which is not proof it will authenticate: when the inventory names a secret for a credential that already exists, the
// summary says so once, in notes.
func TestSourceApplyNotesOnceThatAnExistingCredentialsSecretWasNotChecked(t *testing.T) {
	f := &fakeForward{creds: []any{map[string]any{"id": "L-1", "type": "LOGIN", "name": "lab-login"}, map[string]any{"id": "L-2", "type": "LOGIN", "name": "other"}}}
	spec := `{"network_id":"n1","credentials":[{"name":"lab-login","type":"CLI","username":"a","secret_env":"X"},{"name":"other","type":"CLI","username":"a","secret_env":"X"}]}`
	_, sum, _ := runInventory(t, f, spec, false)
	notes, _ := sum["notes"].([]any)
	if len(notes) != 1 || !strings.Contains(notes[0].(string), "2 existing credential") || !strings.Contains(notes[0].(string), "authenticate") {
		t.Fatalf("notes = %v", sum["notes"])
	}
	items, _ := sum["items"].([]any)
	first, _ := items[0].(map[string]any)
	if first["kind"] != "credential" || first["name"] != "lab-login" || first["action"] != "unchanged" {
		t.Errorf("item shape: %v", first)
	}
}
