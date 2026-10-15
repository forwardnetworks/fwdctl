package skills_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

// An AWS multi-account setup's member accounts are edited incrementally: external ids are set or cleared for the listed accounts only (the values come from a mode-600 file and are never echoed), and newly
// discovered accounts are appended with the setup's own role name. The account list is replaced as a whole on the wire, so every other entry is resent unchanged, and the account is re-read right before the write.
type memberWorld struct {
	infos   []map[string]any
	patched []map[string]any
	newly   []any
}

func (w *memberWorld) routes() map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/cloudAccounts": func(r *http.Request, _ []byte) (int, any) {
			if r.URL.Query().Get("view") == "newlyDiscovered" {
				return 200, map[string]any{"aws-main": w.newly}
			}
			return 200, []any{map[string]any{"type": "AWS", "name": "aws-main", "collect": true, "username": "AKIAEXAMPLE", "assumeRoleInfos": w.infos}}
		},
		"PATCH /api/networks/n1/cloudAccounts/aws-main": func(_ *http.Request, b []byte) (int, any) {
			var p struct {
				AssumeRoleInfos []map[string]any `json:"assumeRoleInfos"`
			}
			_ = json.Unmarshal(b, &p)
			w.patched = p.AssumeRoleInfos
			w.infos = p.AssumeRoleInfos
			return 200, map[string]any{"type": "AWS", "name": "aws-main"}
		},
	}
}

func newMemberWorld() *memberWorld {
	arn := func(id string) string { return "arn:aws:iam::" + id + ":role/ForwardReadOnlyAccess" }
	return &memberWorld{
		infos: []map[string]any{
			{"accountId": "111111111111", "accountName": "one", "roleArn": arn("111111111111"), "enabled": true, "externalId": "old-one"},
			{"accountId": "222222222222", "accountName": "two", "roleArn": arn("222222222222"), "enabled": false, "errorMsg": "AccessDenied"},
			{"accountId": "333333333333", "accountName": "three", "roleArn": arn("333333333333"), "enabled": true},
		},
		newly: []any{map[string]any{"id": "444444444444", "name": "four"}, map[string]any{"id": "555555555555", "name": "five"}},
	}
}

func byAccount(l []map[string]any) map[string]map[string]any {
	m := map[string]map[string]any{}
	for _, x := range l {
		m[x["accountId"].(string)] = x
	}
	return m
}

func TestExternalIdsAreSetAndClearedForTheListedAccountsOnlyAndNeverEchoed(t *testing.T) {
	w := newMemberWorld()
	f := secretFile(t, 0o600, `{"222222222222":"s3cret-two","111111111111":null}`)
	in := `{"network_id":"n1","object":"cloud_account","action":"update","name":"aws-main","secret_file":"` + f + `","definition":{"accounts_external_ids":["111111111111","222222222222"]}`
	r, srv := mustRun(t, "edit-source", w.routes(), in+`}`)
	if writes(srv) != 0 || w.patched != nil || strings.Contains(jsonOf(r), "s3cret") || strings.Contains(jsonOf(r), "old-one") {
		t.Fatalf("dry run wrote or echoed a value: %s", jsonOf(r))
	}
	r, _ = mustRun(t, "edit-source", w.routes(), in+`,"apply":true}`)
	got := byAccount(w.patched)
	if r.Status != result.OK || len(w.patched) != 3 {
		t.Fatalf("%s %s patched=%v", r.Status, r.Finding, w.patched)
	}
	if got["222222222222"]["externalId"] != "s3cret-two" || got["111111111111"]["externalId"] != nil && got["111111111111"]["externalId"] != "" {
		t.Errorf("set and clear: %v", got)
	}
	if got["333333333333"]["enabled"] != true || got["222222222222"]["enabled"] != false || got["222222222222"]["roleArn"] == "" {
		t.Errorf("the untouched fields and accounts must be resent as they were: %v", got)
	}
	if strings.Contains(jsonOf(r), "s3cret") || strings.Contains(jsonOf(r), "old-one") {
		t.Errorf("a value was echoed: %s", jsonOf(r))
	}
}

func TestExternalIdRequestsThatCannotBeHonouredAreRefused(t *testing.T) {
	f := secretFile(t, 0o600, `{"999999999999":"x"}`)
	for name, c := range map[string]struct{ def, file string }{
		"unknown account":          {`{"accounts_external_ids":["999999999999"]}`, f},
		"duplicate id":             {`{"accounts_external_ids":["111111111111","111111111111"]}`, secretFile(t, 0o600, `{"111111111111":"x"}`)},
		"id without a value":       {`{"accounts_external_ids":["111111111111"]}`, secretFile(t, 0o600, `{"222222222222":"x"}`)},
		"value for an unlisted id": {`{"accounts_external_ids":["111111111111"]}`, secretFile(t, 0o600, `{"111111111111":"x","333333333333":"y"}`)},
		"not twelve digits":        {`{"accounts_external_ids":["abc"]}`, secretFile(t, 0o600, `{"abc":"x"}`)},
	} {
		w := newMemberWorld()
		if _, _, err := runSkill(t, "edit-source", w.routes(), `{"network_id":"n1","object":"cloud_account","action":"update","name":"aws-main","secret_file":"`+c.file+`","definition":`+c.def+`,"apply":true}`); err == nil {
			t.Errorf("%s was accepted", name)
		}
		if w.patched != nil {
			t.Errorf("%s: a refused call still wrote", name)
		}
	}
}

func TestNewlyDiscoveredAccountsAreAppendedWithTheSetupsRoleName(t *testing.T) {
	w := newMemberWorld()
	in := `{"network_id":"n1","object":"cloud_account","action":"update","name":"aws-main","definition":{"accounts_add":["444444444444"]}`
	r, srv := mustRun(t, "edit-source", w.routes(), in+`}`)
	if writes(srv) != 0 || w.patched != nil || !strings.Contains(jsonOf(r), "444444444444") {
		t.Fatalf("dry run: %s", jsonOf(r))
	}
	r, _ = mustRun(t, "edit-source", w.routes(), in+`,"apply":true}`)
	got := byAccount(w.patched)
	if r.Status != result.OK || len(w.patched) != 4 || got["444444444444"]["roleArn"] != "arn:aws:iam::444444444444:role/ForwardReadOnlyAccess" || got["444444444444"]["enabled"] != true || got["444444444444"]["accountName"] != "four" {
		t.Fatalf("%s %s patched=%v", r.Status, r.Finding, w.patched)
	}
	if got["111111111111"]["externalId"] != "old-one" || got["222222222222"]["errorMsg"] != "AccessDenied" {
		t.Errorf("existing entries must be resent unchanged: %v", got)
	}
	// all newly discovered
	w = newMemberWorld()
	mustRun(t, "edit-source", w.routes(), `{"network_id":"n1","object":"cloud_account","action":"update","name":"aws-main","apply":true,"definition":{"add_newly_discovered":true}}`)
	if len(w.patched) != 5 {
		t.Errorf("add_newly_discovered adds all: %v", w.patched)
	}
	// refused: already a member, not newly discovered
	for _, bad := range []string{`["111111111111"]`, `["666666666666"]`, `["444444444444","444444444444"]`} {
		w = newMemberWorld()
		if _, _, err := runSkill(t, "edit-source", w.routes(), `{"network_id":"n1","object":"cloud_account","action":"update","name":"aws-main","apply":true,"definition":{"accounts_add":`+bad+`}}`); err == nil || w.patched != nil {
			t.Errorf("accounts_add %s was accepted", bad)
		}
	}
}

// Forward lists, for an IAM-user setup, one "newly discovered" entry whose id is the setup's own name (a cloud account row whose id equals its setup id). It is not an AWS account: add_newly_discovered
// skips anything that is not 12 digits and says so, and accounts_add refuses such an id.
func TestNewlyDiscoveredEntriesThatAreNotAccountIdsAreSkippedAndSaid(t *testing.T) {
	w := newMemberWorld()
	w.newly = []any{map[string]any{"id": "aws-main", "name": "aws-main"}, map[string]any{"id": "444444444444", "name": "four"}}
	r, _ := mustRun(t, "edit-source", w.routes(), `{"network_id":"n1","object":"cloud_account","action":"update","name":"aws-main","apply":true,"definition":{"add_newly_discovered":true}}`)
	got := byAccount(w.patched)
	if r.Status != result.OK || len(w.patched) != 4 || got["444444444444"] == nil || got["aws-main"] != nil {
		t.Fatalf("only the real account id is added: %v", w.patched)
	}
	if !strings.Contains(strings.Join(r.Limits, " ")+jsonOf(r), "not an AWS account id") {
		t.Errorf("the skipped entry should be said: %s", jsonOf(r))
	}
	w = newMemberWorld()
	w.newly = []any{map[string]any{"id": "aws-main", "name": "aws-main"}}
	if _, _, err := runSkill(t, "edit-source", w.routes(), `{"network_id":"n1","object":"cloud_account","action":"update","name":"aws-main","apply":true,"definition":{"accounts_add":["aws-main"]}}`); err == nil || w.patched != nil {
		t.Errorf("accounts_add of a non-account id must be refused: %v", err)
	}
}
