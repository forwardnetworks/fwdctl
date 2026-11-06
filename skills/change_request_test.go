package skills_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
)

// A fake Forward with one change set c1, an org ServiceNow integration, and change requests that link to c1 when raised.
type snFake struct {
	mu          sync.Mutex
	linked      []map[string]any
	created     []string // bodies of create calls
	uploads     []string // bodies of attachment uploads
	attached    []map[string]any
	configured  bool
	linkOnRaise bool
	summaryDown bool
}

func (f *snFake) routes() map[string]fwdtest.Handler {
	cr := func() map[string]any {
		return map[string]any{"number": "CHG0000001", "sysId": "sys1", "summary": "add route", "impact": "2 - Medium", "state": "New",
			"url": "https://example.invalid/change/sys1", "attachments": f.attached}
	}
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/change-sets": fwdtest.Const(200, []any{map[string]any{"id": "c1", "name": "cs", "snapshotId": "s1"}}),
		"GET /api/networks/n1/change-sets/c1": func(*http.Request, []byte) (int, any) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.summaryDown {
				return 500, map[string]any{"message": "boom"}
			}
			return 200, map[string]any{"name": "cs", "snapshotId": "s1", "serviceNowChangeRequests": f.linked}
		},
		"GET /api/networks/n1/change-sets/c1/commits": fwdtest.Const(200, []any{}),
		"GET /api/networks/n1/change-set-directories": fwdtest.Const(200, map[string]any{}),
		"GET /api/integrations/servicenow": func(*http.Request, []byte) (int, any) {
			if !f.configured {
				return 404, map[string]any{"message": "not found"}
			}
			return 200, map[string]any{"instanceUrl": "https://example.invalid", "enabled": true}
		},
		"POST /api/networks/n1/change-sets/c1/servicenow/change-requests": func(_ *http.Request, b []byte) (int, any) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.created = append(f.created, string(b))
			if f.linkOnRaise {
				f.linked = append(f.linked, map[string]any{"number": "CHG0000001", "sysId": "sys1"})
			}
			return 200, cr()
		},
		"GET /api/networks/n1/change-sets/c1/servicenow/change-requests/sys1": func(*http.Request, []byte) (int, any) { return 200, cr() },
		"GET /api/networks/n1/change-sets/c1/servicenow/change-requests/sys2": fwdtest.Const(502, map[string]any{"message": "ServiceNow unreachable"}),
		"POST /api/networks/n1/change-sets/c1/servicenow/change-requests/sys1/attachments": func(_ *http.Request, b []byte) (int, any) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.uploads = append(f.uploads, string(b))
			f.attached = append(f.attached, map[string]any{"fileName": "report.csv", "fileSysId": "f1"})
			return 204, nil
		},
		"GET /api/networks/n1/change-sets/c1/servicenow/change-requests/sys1/attachments/f1": func(*http.Request, []byte) (int, any) {
			return 200, []byte("a,b\n1,2\n")
		},
	}
}

const raiseIn = `{"network_id":"n1","change_set_id":"c1","lifecycle":{"op":"raise_change_request","change_request":{"summary":"add route","impact":"medium"}}`

func TestRaiseChangeRequestIsDryRunThenNeedsConfirmAndReportsTheTicket(t *testing.T) {
	f := &snFake{configured: true, linkOnRaise: true}
	routes := f.routes()
	r, _ := mustRun(t, "edit-change-set", routes, raiseIn+`}`)
	if len(f.created) != 0 || r.Changes[0].Applied || r.Changes[0].Reversible {
		t.Fatalf("a dry run raised a request or claimed it reversible: %v %+v", f.created, r.Changes[0])
	}
	if r, _ = mustRun(t, "edit-change-set", routes, raiseIn+`,"apply":true}`); r.Status == "ok" || len(f.created) != 0 || !strings.Contains(r.Finding, `confirm="c1"`) {
		t.Fatalf("raising without confirm must be refused before anything is sent: %s %s %v", r.Status, r.Finding, f.created)
	}
	r, _ = mustRun(t, "edit-change-set", routes, raiseIn+`,"apply":true,"confirm":"c1"}`)
	if len(f.created) != 1 || !strings.Contains(f.created[0], `"impact":"MEDIUM"`) || !strings.Contains(f.created[0], `"summary":"add route"`) {
		t.Fatalf("the request sent: %v", f.created)
	}
	if r.Status != "ok" || !strings.Contains(r.Finding, "CHG0000001") || !strings.Contains(r.Finding, "https://example.invalid/change/sys1") {
		t.Fatalf("the finding must give the ticket number and URL: %s %s", r.Status, r.Finding)
	}
}

func TestRaiseChangeRequestNotLinkedAfterwardsIsUnknown(t *testing.T) {
	f := &snFake{configured: true}
	r, _ := mustRun(t, "edit-change-set", f.routes(), raiseIn+`,"apply":true,"confirm":"c1"}`)
	if r.Status != "unknown" || !strings.Contains(r.Finding, "CHG0000001") {
		t.Fatalf("a raised request the summary does not list is unknown, with its number kept: %s %s", r.Status, r.Finding)
	}
}

func TestRaiseChangeRequestRefusedWithoutServiceNowOrWithABadImpact(t *testing.T) {
	f := &snFake{}
	r, _ := mustRun(t, "edit-change-set", f.routes(), raiseIn+`,"apply":true,"confirm":"c1"}`)
	if r.Status != "failed" || len(f.created) != 0 || !strings.Contains(r.Finding, "no ServiceNow integration") {
		t.Fatalf("no integration: %s %s %v", r.Status, r.Finding, f.created)
	}
	bad := strings.Replace(raiseIn, `"medium"`, `"urgent"`, 1) + `}`
	if _, _, err := runSkill(t, "edit-change-set", f.routes(), bad); err == nil {
		t.Fatalf("impact urgent must be refused")
	}
	if _, _, err := runSkill(t, "edit-change-set", f.routes(), `{"network_id":"n1","change_set_id":"c1","lifecycle":{"op":"raise_change_request"}}`); err == nil {
		t.Fatalf("a request needs change_request")
	}
}

func TestAttachToChangeRequestOnlyToALinkedRequestAndOnlyAllowedFiles(t *testing.T) {
	dir := t.TempDir()
	csv, exe := filepath.Join(dir, "report.csv"), filepath.Join(dir, "tool.exe")
	os.WriteFile(csv, []byte("a,b\n1,2\n"), 0o600)
	os.WriteFile(exe, []byte("x"), 0o600)
	f := &snFake{configured: true, linked: []map[string]any{{"number": "CHG0000001", "sysId": "sys1"}}}
	routes := f.routes()
	in := func(sys, path, extra string) string {
		return `{"network_id":"n1","change_set_id":"c1","lifecycle":{"op":"attach_to_change_request","sys_id":"` + sys + `","path":"` + path + `"}` + extra + `}`
	}
	if _, _, err := runSkill(t, "edit-change-set", routes, in("sys1", exe, "")); err == nil {
		t.Fatalf("an .exe is not a type Forward accepts")
	}
	if r, _ := mustRun(t, "edit-change-set", routes, in("sys9", csv, `,"apply":true,"confirm":"sys9"`)); r.Status != "failed" || len(f.uploads) != 0 {
		t.Fatalf("a request not linked to the change set: %s %s", r.Status, r.Finding)
	}
	if r, _ := mustRun(t, "edit-change-set", routes, in("sys1", csv, `,"apply":true`)); r.Status == "ok" || len(f.uploads) != 0 {
		t.Fatalf("attach without confirm must be refused: %s", r.Finding)
	}
	r, _ := mustRun(t, "edit-change-set", routes, in("sys1", csv, `,"apply":true,"confirm":"sys1"`))
	if r.Status != "ok" || len(f.uploads) != 1 || !strings.Contains(f.uploads[0], "1,2") || !strings.Contains(f.uploads[0], `filename="report.csv"`) {
		t.Fatalf("attach: %s %s %v", r.Status, r.Finding, f.uploads)
	}
}

func TestDownloadChangeRequestAttachmentWritesANewFileOnly(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "got.csv")
	f := &snFake{linked: []map[string]any{{"number": "CHG0000001", "sysId": "sys1"}}}
	in := `{"network_id":"n1","change_set_id":"c1","lifecycle":{"op":"download_change_request_attachment","sys_id":"sys1","file_sys_id":"f1","path":"` + out + `"},"apply":true}`
	if r, _ := mustRun(t, "edit-change-set", f.routes(), in); r.Status != "ok" {
		t.Fatalf("download: %s %s", r.Status, r.Finding)
	}
	if b, err := os.ReadFile(out); err != nil || string(b) != "a,b\n1,2\n" {
		t.Fatalf("downloaded file: %q %v", b, err)
	}
	if _, _, err := runSkill(t, "edit-change-set", f.routes(), in); err == nil {
		t.Fatalf("an existing file must not be overwritten")
	}
}

func TestHistoryListsLinkedChangeRequestsWithStateAndSaysWhichCouldNotBeRead(t *testing.T) {
	f := &snFake{linked: []map[string]any{{"number": "CHG0000001", "sysId": "sys1"}, {"number": "CHG0000002", "sysId": "sys2"}}}
	r, _ := mustRun(t, "verify-change", f.routes(), `{"network_id":"n1","view":"history","change_set_id":"c1"}`)
	b := jsonOf(r)
	for _, want := range []string{`"state":"New"`, `https://example.invalid/change/sys1`, `"number":"CHG0000002"`, "ServiceNow unreachable", "could not be read from ServiceNow"} {
		if !strings.Contains(b, want) {
			t.Errorf("history missing %q: %s", want, b)
		}
	}
}

func TestDownloadFromARequestNotLinkedIsUnknownAndWritesNothing(t *testing.T) {
	out := filepath.Join(t.TempDir(), "got.csv")
	f := &snFake{}
	r, _ := mustRun(t, "edit-change-set", f.routes(), `{"network_id":"n1","change_set_id":"c1","lifecycle":{"op":"download_change_request_attachment","sys_id":"sys1","file_sys_id":"f1","path":"`+out+`"},"apply":true}`)
	if _, err := os.Stat(out); r.Status != "unknown" || err == nil {
		t.Fatalf("not linked: %s %s, file written: %v", r.Status, r.Finding, err == nil)
	}
}

func TestAttachDryRunSendsNothingAndWrongConfirmOrUnreadableSummaryIsRefused(t *testing.T) {
	dir := t.TempDir()
	csv, empty := filepath.Join(dir, "report.csv"), filepath.Join(dir, "empty.csv")
	os.WriteFile(csv, []byte("a,b\n"), 0o600)
	os.WriteFile(empty, nil, 0o600)
	f := &snFake{configured: true, linked: []map[string]any{{"number": "CHG0000001", "sysId": "sys1"}}}
	in := func(path, extra string) string {
		return `{"network_id":"n1","change_set_id":"c1","lifecycle":{"op":"attach_to_change_request","sys_id":"sys1","path":"` + path + `"}` + extra + `}`
	}
	if r, _ := mustRun(t, "edit-change-set", f.routes(), in(csv, "")); len(f.uploads) != 0 || r.Changes[0].Applied {
		t.Fatalf("a dry run uploaded: %v", f.uploads)
	}
	if r, _ := mustRun(t, "edit-change-set", f.routes(), in(csv, `,"apply":true,"confirm":"c1"`)); r.Status == "ok" || len(f.uploads) != 0 {
		t.Fatalf("confirm is the sys_id, not the change set id: %s", r.Finding)
	}
	if _, _, err := runSkill(t, "edit-change-set", f.routes(), in(empty, "")); err == nil {
		t.Fatalf("an empty file must be refused")
	}
	f.summaryDown = true
	if r, _ := mustRun(t, "edit-change-set", f.routes(), in(csv, `,"apply":true,"confirm":"sys1"`)); r.Status != "unknown" || len(f.uploads) != 0 {
		t.Fatalf("an unreadable summary must not let an attach through: %s %s", r.Status, r.Finding)
	}
}

func TestDownloadDryRunWritesNoFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "got.csv")
	f := &snFake{linked: []map[string]any{{"number": "CHG0000001", "sysId": "sys1"}}}
	mustRun(t, "edit-change-set", f.routes(), `{"network_id":"n1","change_set_id":"c1","lifecycle":{"op":"download_change_request_attachment","sys_id":"sys1","file_sys_id":"f1","path":"`+out+`"}}`)
	if _, err := os.Stat(out); err == nil {
		t.Fatalf("a dry run wrote the file")
	}
}

func TestHistoryReadsAtMostTenLinkedRequests(t *testing.T) {
	f := &snFake{}
	for i := 0; i < 11; i++ {
		f.linked = append(f.linked, map[string]any{"number": "CHG", "sysId": "sys1"})
	}
	r, _ := mustRun(t, "verify-change", f.routes(), `{"network_id":"n1","view":"history","change_set_id":"c1"}`)
	if b := jsonOf(r); strings.Count(b, `"read":false`) != 1 || !strings.Contains(b, "only the first 10") {
		t.Fatalf("history cap: %s", b)
	}
}
