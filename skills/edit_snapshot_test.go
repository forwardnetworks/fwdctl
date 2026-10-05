package skills_test

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

func snapWorld(state string, extra map[string]fwdtest.Handler) map[string]fwdtest.Handler {
	r := map[string]fwdtest.Handler{snapsPath: fwdtest.Snapshots(fwdtest.Snap("s1", state, "COLLECTION", "2026-09-01T00:00:00.000Z"))}
	for k, v := range extra {
		r[k] = v
	}
	return r
}

func TestEditSnapshotDeleteAndFavoriteNeedTheExactConfirmAndSayThereIsNoUndo(t *testing.T) {
	deleted := false
	routes := snapWorld("PROCESSED", map[string]fwdtest.Handler{"DELETE /api/snapshots/s1": func(*http.Request, []byte) (int, any) { deleted = true; return 204, nil }})
	r, srv := mustRun(t, "edit-snapshot", routes, `{"network_id":"n1","snapshot_id":"s1","action":"delete"}`)
	if r.Status != result.OK || r.Mode != result.ModeDryRun || writes(srv) != 0 || r.Changes[0].Reversible || !strings.Contains(r.Finding, `confirm="s1"`) {
		t.Fatalf("dry run: %s %s %+v", r.Status, r.Finding, r.Changes)
	}
	if _, _, err := runSkill(t, "edit-snapshot", routes, `{"network_id":"n1","snapshot_id":"s1","action":"delete","apply":true}`); err == nil || deleted {
		t.Errorf("apply without confirm must be refused before anything is sent: %v", err)
	}
	if _, _, err := runSkill(t, "edit-snapshot", routes, `{"network_id":"n1","snapshot_id":"s1","action":"delete","apply":true,"confirm":"other"}`); err == nil || deleted {
		t.Errorf("a wrong confirm is refused")
	}
	// Forward accepts but the snapshot is still listed: failed, not ok
	r, _ = mustRun(t, "edit-snapshot", routes, `{"network_id":"n1","snapshot_id":"s1","action":"delete","apply":true,"confirm":"s1"}`)
	if !deleted || r.Status != result.Failed || !strings.Contains(r.Finding, "still listed") {
		t.Errorf("read-back decides: deleted=%v %s %s", deleted, r.Status, r.Finding)
	}
}

func TestEditSnapshotRefusesABusySnapshotAndUnknownActionsAndStrayInputs(t *testing.T) {
	r, srv := mustRun(t, "edit-snapshot", snapWorld("PROCESSING", nil), `{"network_id":"n1","snapshot_id":"s1","action":"invalidate","apply":true}`)
	if r.Status != result.Failed || writes(srv) != 0 || !strings.Contains(r.Finding, "PROCESSING") {
		t.Errorf("a snapshot Forward is working on is refused: %s %s", r.Status, r.Finding)
	}
	for _, bad := range []string{`{"network_id":"n1","snapshot_id":"s1","action":"nope"}`, `{"network_id":"n1","snapshot_id":"s1","action":"invalidate","confirm":"s1"}`, `{"network_id":"n1","snapshot_id":"s1","action":"delete","definition":{}}`, `{"network_id":"n1","action":"delete"}`} {
		if _, _, err := runSkill(t, "edit-snapshot", snapWorld("PROCESSED", nil), bad); err == nil {
			t.Errorf("%s must be refused", bad)
		}
	}
}

func TestEditSnapshotRetentionPolicyIsADryRunMergesTheGivenFieldsAndNeedsTheNetworkAsConfirm(t *testing.T) {
	policy := map[string]any{"enabled": true, "lastWeek": "ALL", "lastMonth": "ALL", "lastQuarter": "ALL", "lastYear": "ONE_PER_MONTH", "older": "NONE"}
	var put map[string]any
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/snapshotRetentionPolicy": func(*http.Request, []byte) (int, any) { return 200, policy },
		"PUT /api/networks/n1/snapshotRetentionPolicy": func(_ *http.Request, b []byte) (int, any) {
			put = map[string]any{}
			for k, v := range policy {
				put[k] = v
			}
			put["lastMonth"] = "ONE_PER_DAY"
			policy = put
			return 204, nil
		},
	}
	in := `{"network_id":"n1","action":"retention_policy","definition":{"lastMonth":"one_per_day"}`
	r, srv := mustRun(t, "edit-snapshot", routes, in+`}`)
	b := jsonOf(r)
	if r.Mode != result.ModeDryRun || writes(srv) != 0 || !strings.Contains(b, `"lastMonth":"ONE_PER_DAY"`) || !strings.Contains(b, `"lastYear":"ONE_PER_MONTH"`) || r.Changes[0].Reversible {
		t.Fatalf("the given field changes and the others keep their value: %s", b)
	}
	if _, _, err := runSkill(t, "edit-snapshot", routes, in+`,"apply":true}`); err == nil {
		t.Errorf("apply without confirm=network_id must be refused")
	}
	r, _ = mustRun(t, "edit-snapshot", routes, in+`,"apply":true,"confirm":"n1"}`)
	if r.Status != result.OK || !r.Changes[0].Applied {
		t.Errorf("apply: %s %s", r.Status, r.Finding)
	}
	if _, _, err := runSkill(t, "edit-snapshot", routes, `{"network_id":"n1","action":"retention_policy","definition":{"bogus":1}}`); err == nil {
		t.Errorf("an unknown field is refused")
	}
}

func TestEditSnapshotExportStreamsToANewFileNeverOverwritesAndKeepsTheKeyOut(t *testing.T) {
	var sent string
	zip := []byte("PK\x03\x04 fake zip body")
	routes := snapWorld("PROCESSED", map[string]fwdtest.Handler{"POST /api/snapshots/s1": func(_ *http.Request, b []byte) (int, any) { sent = string(b); return 200, zip }})
	dir := t.TempDir()
	out := dir + "/s1.zip"
	key := secretFile(t, 0o600, planted2)
	body := `{"network_id":"n1","snapshot_id":"s1","action":"export","definition":{"path":"` + out + `","obfuscate_names":true},"secret_file":"` + key + `"`
	r, srv := mustRun(t, "edit-snapshot", routes, body+`}`)
	if writes(srv) != 0 || r.Mode != result.ModeDryRun {
		t.Fatalf("dry run writes nothing: %s", r.Finding)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatalf("the dry run must not create the file")
	}
	r, _ = mustRun(t, "edit-snapshot", routes, body+`,"apply":true}`)
	st, err := os.Stat(out)
	if r.Status != result.OK || err != nil || st.Mode().Perm() != 0o600 || !strings.Contains(sent, planted2) || strings.Contains(jsonOf(r), planted2) {
		t.Fatalf("export: %s %v %s", r.Status, err, jsonOf(r))
	}
	if _, _, err := runSkill(t, "edit-snapshot", routes, body+`,"apply":true}`); err == nil {
		t.Errorf("an existing file is never overwritten")
	}
	if _, _, err := runSkill(t, "edit-snapshot", routes, `{"network_id":"n1","snapshot_id":"s1","action":"export","definition":{"path":"`+dir+`/x.zip","obfuscate_names":true}}`); err == nil {
		t.Errorf("obfuscate_names needs a key")
	}
	if _, _, err := runSkill(t, "edit-snapshot", routes, `{"network_id":"n1","snapshot_id":"s1","action":"delete","secret_file":"`+key+`"}`); err == nil {
		t.Errorf("only export takes a secret")
	}
}

func TestEditSnapshotImportUploadsFilesAsANewSnapshot(t *testing.T) {
	dir := t.TempDir()
	f := dir + "/a.zip"
	if err := os.WriteFile(f, []byte("PK\x03\x04"), 0o600); err != nil {
		t.Fatal(err)
	}
	var uploaded bool
	routes := map[string]fwdtest.Handler{"POST /api/networks/n1/snapshots": func(*http.Request, []byte) (int, any) {
		uploaded = true
		return 200, map[string]any{"id": "new1", "state": "PROCESSING"}
	}}
	body := `{"network_id":"n1","action":"import","note":"from lab","definition":{"files":["` + f + `"]}`
	if r, _ := mustRun(t, "edit-snapshot", routes, body+`}`); uploaded || r.Mode != result.ModeDryRun {
		t.Fatalf("dry run uploads nothing")
	}
	r, _ := mustRun(t, "edit-snapshot", routes, body+`,"apply":true}`)
	if r.Status != result.OK || !uploaded || !strings.Contains(r.Changes[0].Undo, "new1") {
		t.Fatalf("import: %s %s %+v", r.Status, r.Finding, r.Changes)
	}
	if _, _, err := runSkill(t, "edit-snapshot", routes, `{"network_id":"n1","action":"import","definition":{"files":["/nonexistent.zip"]}}`); err == nil {
		t.Errorf("a missing file is refused")
	}
}

func TestEditSnapshotFavoriteAndUnfavoriteAreReversibleAndNeedNoConfirm(t *testing.T) {
	var calls []string
	routes := snapWorld("PROCESSED", map[string]fwdtest.Handler{"PATCH /api/snapshots/s1": func(r *http.Request, _ []byte) (int, any) {
		calls = append(calls, r.URL.Query().Get("action"))
		return 204, nil
	}})
	for _, a := range []string{"favorite", "unfavorite"} {
		r, _ := mustRun(t, "edit-snapshot", routes, `{"network_id":"n1","snapshot_id":"s1","action":"`+a+`","apply":true}`)
		if r.Status != result.OK || !r.Changes[0].Reversible {
			t.Fatalf("%s: %s %+v", a, r.Status, r.Changes)
		}
	}
	if len(calls) != 2 || calls[0] != "favorite" || calls[1] != "unfavorite" {
		t.Errorf("calls %v", calls)
	}
}
