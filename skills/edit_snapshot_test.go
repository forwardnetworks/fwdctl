package skills_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
	"github.com/forwardnetworks/fwdctl/skills"
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

func importZip(t *testing.T) string {
	f := t.TempDir() + "/a.zip"
	if err := os.WriteFile(f, []byte("PK\x03\x04"), 0o600); err != nil {
		t.Fatal(err)
	}
	return `{"network_id":"n1","action":"import","apply":true,"definition":{"files":["` + f + `"]}}`
}

func TestEditSnapshotImportGatewayTimeoutThatKeptTheFileReportsTheNewSnapshotNotAFailure(t *testing.T) {
	defer skills.ImportPollHooks(time.Millisecond, 200*time.Millisecond)()
	uploaded := false
	routes := map[string]fwdtest.Handler{
		"POST /api/networks/n1/snapshots": func(*http.Request, []byte) (int, any) {
			uploaded = true
			return 504, map[string]any{"message": "stream timeout"}
		},
		"GET /api/networks/n1/snapshots": func(*http.Request, []byte) (int, any) {
			if !uploaded {
				return 200, map[string]any{"snapshots": []any{map[string]any{"id": "old1", "state": "PROCESSED"}}}
			}
			return 200, map[string]any{"snapshots": []any{map[string]any{"id": "old1", "state": "PROCESSED"}, map[string]any{"id": "new9", "state": "UNPACKING"}}}
		},
	}
	r, _ := mustRun(t, "edit-snapshot", routes, importZip(t))
	if r.Status != result.OK || !strings.Contains(r.Finding, "new9") || !strings.Contains(strings.Join(r.Limits, " "), "not retried") {
		t.Fatalf("%s %s %v", r.Status, r.Finding, r.Limits)
	}
}

func TestEditSnapshotImportGatewayTimeoutWithNoNewSnapshotIsUnknownAndSaysDoNotRetryBlindly(t *testing.T) {
	defer skills.ImportPollHooks(time.Millisecond, 20*time.Millisecond)()
	routes := map[string]fwdtest.Handler{
		"POST /api/networks/n1/snapshots": fwdtest.Const(504, map[string]any{"message": "stream timeout"}),
		"GET /api/networks/n1/snapshots":  fwdtest.Const(200, map[string]any{"snapshots": []any{map[string]any{"id": "old1", "state": "PROCESSED"}}}),
	}
	r, _ := mustRun(t, "edit-snapshot", routes, importZip(t))
	if r.Status != result.Unknown || !strings.Contains(strings.Join(r.Limits, " "), "do NOT retry") {
		t.Fatalf("%s %s %v", r.Status, r.Finding, r.Limits)
	}
}

func TestEditSnapshotImportARealRefusalIsStillAnError(t *testing.T) {
	routes := map[string]fwdtest.Handler{"POST /api/networks/n1/snapshots": fwdtest.Const(400, map[string]any{"message": "bad zip"})}
	if _, _, err := runSkill(t, "edit-snapshot", routes, importZip(t)); err == nil || !strings.Contains(err.Error(), "import failed") {
		t.Fatalf("a 400 is a failure, not an unknown: %v", err)
	}
}

func TestEditSnapshotImportWaitIsConfigurableAndZeroDoesNotWait(t *testing.T) {
	t.Setenv("FORWARD_IMPORT_WAIT", "0")
	polls := 0
	routes := map[string]fwdtest.Handler{
		"POST /api/networks/n1/snapshots": fwdtest.Const(504, map[string]any{"message": "stream timeout"}),
		"GET /api/networks/n1/snapshots": func(*http.Request, []byte) (int, any) {
			polls++
			return 200, map[string]any{"snapshots": []any{}}
		},
	}
	start := time.Now()
	r, _ := mustRun(t, "edit-snapshot", routes, importZip(t))
	if r.Status != result.Unknown || time.Since(start) > 2*time.Second || polls != 1 {
		t.Fatalf("%s after %s, %d list reads (only the one before the upload)", r.Status, time.Since(start), polls)
	}
}

// A long export or import tells the host how far it is: bytes written (export) and bytes sent of the known total (import), through the session's Progress hook; the result is unchanged.
func TestEditSnapshotExportAndImportReportProgress(t *testing.T) {
	type seen struct {
		stage       string
		done, total int64
	}
	var got []seen
	hook := func(stage string, done, total int64, _ time.Duration) { got = append(got, seen{stage, done, total}) }
	fwd.ProgressInterval = 0
	defer func() { fwd.ProgressInterval = 5 * time.Second }()

	zip := []byte("PK\x03\x04 fake zip body")
	routes := snapWorld("PROCESSED", map[string]fwdtest.Handler{"POST /api/snapshots/s1": func(*http.Request, []byte) (int, any) { return 200, zip }})
	sess, _ := fwdtest.New(t, routes)
	sess.Progress = hook
	out := t.TempDir() + "/s1.zip"
	if _, err := skills.Run(context.Background(), "edit-snapshot", sess, json.RawMessage(`{"network_id":"n1","snapshot_id":"s1","action":"export","definition":{"path":"`+out+`"},"apply":true}`)); err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[len(got)-1] != (seen{"export", int64(len(zip)), 0}) {
		t.Errorf("export progress: %v", got)
	}

	got = nil
	f := t.TempDir() + "/a.zip"
	body := []byte(strings.Repeat("z", 4096))
	os.WriteFile(f, body, 0o600)
	routes = map[string]fwdtest.Handler{"POST /api/networks/n1/snapshots": func(*http.Request, []byte) (int, any) {
		return 200, map[string]any{"id": "new1", "state": "PROCESSING"}
	}}
	sess, _ = fwdtest.New(t, routes)
	sess.Progress = hook
	if _, err := skills.Run(context.Background(), "edit-snapshot", sess, json.RawMessage(`{"network_id":"n1","action":"import","definition":{"files":["`+f+`"]},"apply":true}`)); err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[len(got)-1] != (seen{"import", 4096, 4096}) {
		t.Errorf("import progress: %v", got)
	}
}

// An export can select its devices by platform or by an NQE query: the names are resolved on the snapshot, glob characters are escaped so each matches only itself, the result shows a count
// and a bounded sample, and an empty selection exports nothing (an empty include list would mean every device).
func TestEditSnapshotExportSelectsDevicesByPlatformOrQuery(t *testing.T) {
	var sent, asked string
	var rows []map[string]any
	routes := snapWorld("PROCESSED", map[string]fwdtest.Handler{
		"POST /api/snapshots/s1": func(_ *http.Request, b []byte) (int, any) { sent = string(b); return 200, []byte("PK\x03\x04 fake") },
		"POST /api/nqe": func(_ *http.Request, b []byte) (int, any) {
			asked = string(b)
			return 200, map[string]any{"items": rows, "totalNumItems": len(rows)}
		},
	})
	dir := t.TempDir()
	rows = []map[string]any{{"name": "fw1"}, {"name": "fw[2]"}}
	body := func(sel, file string) string {
		return `{"network_id":"n1","snapshot_id":"s1","action":"export","definition":{"path":"` + dir + `/` + file + `","select":` + sel + `}`
	}
	r, srv := mustRun(t, "edit-snapshot", routes, body(`{"vendor":"fortinet"}`, "a.zip")+`}`)
	if writes(srv) != 0 || r.Mode != result.ModeDryRun || !strings.Contains(jsonOf(r), `"selected_devices":2`) || !strings.Contains(jsonOf(r), "related devices") {
		t.Fatalf("dry run: the count and the related-devices note: %s", jsonOf(r))
	}
	if !strings.Contains(asked, "FORTINET") {
		t.Errorf("the vendor is sent upper-cased as a parameter: %s", asked)
	}
	if _, err := os.Stat(dir + "/a.zip"); err == nil {
		t.Fatalf("a dry run writes no file")
	}
	r, _ = mustRun(t, "edit-snapshot", routes, body(`{"vendor":"fortinet"}`, "a.zip")+`,"apply":true}`)
	if r.Status != result.OK || !strings.Contains(sent, `fw1`) || !strings.Contains(sent, `fw\\[2]`) {
		t.Fatalf("the selected names are sent as exact globs (a bracket escaped): %s %s", r.Status, sent)
	}
	rows = nil
	r, _ = mustRun(t, "edit-snapshot", routes, body(`{"vendor":"nonesuch"}`, "b.zip")+`,"apply":true}`)
	if r.Status != result.Unknown {
		t.Errorf("an empty selection must not export every device: %s %s", r.Status, r.Finding)
	}
	if _, err := os.Stat(dir + "/b.zip"); err == nil {
		t.Errorf("an empty selection wrote a file")
	}
	for _, sel := range []string{`{"vendor":"x","query":"select {name: 1}"}`, `{}`, `{"query_id":"Q_1","query":"select {name: 1}"}`, `{"vendor":"x","as":"sideways"}`} {
		if _, _, err := runSkill(t, "edit-snapshot", routes, body(sel, "c.zip")+`}`); err == nil {
			t.Errorf("select %s must be refused", sel)
		}
	}
}

// A favorite only changes retention, and Forward's addFavoriteSnapshot is a metadata write that does not look at the processing state: favorite and unfavorite go through on a snapshot that is
// PROCESSING (a reprocess can run for hours), with a limit saying so, while invalidate and delete stay refused.
func TestEditSnapshotFavoriteIsAllowedWhileProcessingAndDestructiveActionsAreNot(t *testing.T) {
	var calls []string
	routes := snapWorld("PROCESSING", map[string]fwdtest.Handler{"PATCH /api/snapshots/s1": func(r *http.Request, _ []byte) (int, any) {
		calls = append(calls, r.URL.Query().Get("action"))
		return 204, nil
	}})
	for _, a := range []string{"favorite", "unfavorite"} {
		r, _ := mustRun(t, "edit-snapshot", routes, `{"network_id":"n1","snapshot_id":"s1","action":"`+a+`","apply":true}`)
		if r.Status != result.OK || !r.Changes[0].Applied {
			t.Fatalf("%s while PROCESSING: %s %s", a, r.Status, r.Finding)
		}
	}
	if len(calls) != 2 {
		t.Errorf("both writes should reach Forward: %v", calls)
	}
	r, _ := mustRun(t, "edit-snapshot", routes, `{"network_id":"n1","snapshot_id":"s1","action":"favorite"}`)
	if !strings.Contains(strings.Join(r.Limits, " "), "does not look at the processing state") || !strings.Contains(strings.Join(r.Limits, " "), "silently undo") {
		t.Errorf("the limit should say why it is allowed and that a failed reprocess can undo it: %v", r.Limits)
	}
	if u, _ := mustRun(t, "edit-snapshot", routes, `{"network_id":"n1","snapshot_id":"s1","action":"unfavorite"}`); !strings.Contains(strings.Join(u.Limits, " "), "silently undo") {
		t.Errorf("unfavorite while busy carries the same caveat: %v", u.Limits)
	}
	for _, body := range []string{`{"network_id":"n1","snapshot_id":"s1","action":"invalidate","apply":true}`, `{"network_id":"n1","snapshot_id":"s1","action":"delete","apply":true,"confirm":"s1"}`} {
		r, srv := mustRun(t, "edit-snapshot", routes, body)
		if r.Status != result.Failed || writes(srv) != 0 || !strings.Contains(r.Finding, "Refused") {
			t.Errorf("%s while PROCESSING must stay refused: %s %s", body, r.Status, r.Finding)
		}
	}
}

// import with merge_into_latest merges ONE zip with the network's latest snapshot and creates a NEW snapshot (Forward: POST ...?action=merge&mergeType=...).
func mergeZip(t *testing.T) string {
	f := t.TempDir() + "/m.zip"
	if err := os.WriteFile(f, []byte("PK\x03\x04data"), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func mergeBody(f, mt string, extra string) string {
	return `{"network_id":"n1","action":"import","note":"merge","definition":{"files":["` + f + `"],"merge_into_latest":"` + mt + `"` + extra + `}`
}

func TestEditSnapshotMergeIntoLatestDryRunUploadsNothingAndNamesTheLatestSnapshot(t *testing.T) {
	f := mergeZip(t)
	uploaded := false
	routes := map[string]fwdtest.Handler{
		snapsPath:                         ready("s1"),
		"POST /api/networks/n1/snapshots": func(*http.Request, []byte) (int, any) { uploaded = true; return 202, map[string]any{"id": "m1"} },
	}
	r, srv := mustRun(t, "edit-snapshot", routes, mergeBody(f, "PRESERVE_EXISTING", "")+`}`)
	if uploaded || writes(srv) != 0 || r.Mode != result.ModeDryRun || !strings.Contains(r.Finding, "NEW snapshot") || !strings.Contains(r.Finding, "PRESERVE_EXISTING") || !strings.Contains(jsonOf(r), `"latest_processed_snapshot":"s1"`) {
		t.Fatalf("dry run: %s %s", r.Mode, r.Finding)
	}
	lim := strings.Join(r.Limits, " ")
	if !strings.Contains(lim, "network's own data is kept") || !strings.Contains(lim, "not read here") || !strings.Contains(lim, "synthetic objects") {
		t.Errorf("limits: %v", r.Limits)
	}
}

func TestEditSnapshotMergeIntoLatestSendsTheMergeActionAndTypeAndReportsTheNewSnapshot(t *testing.T) {
	f := mergeZip(t)
	var q map[string][]string
	routes := map[string]fwdtest.Handler{
		snapsPath: ready("s1"),
		"POST /api/networks/n1/snapshots": func(r *http.Request, _ []byte) (int, any) {
			q = r.URL.Query()
			return 202, map[string]any{"id": "m1", "state": "UNPACKING"}
		},
	}
	r, _ := mustRun(t, "edit-snapshot", routes, mergeBody(f, "REPLACE_EXISTING", "")+`,"apply":true}`)
	if r.Status != result.OK || q["action"] == nil || q["action"][0] != "merge" || q["mergeType"] == nil || q["mergeType"][0] != "REPLACE_EXISTING" || !strings.Contains(r.Changes[0].Undo, "m1") || !r.Changes[0].Applied {
		t.Fatalf("%s %s query=%v", r.Status, r.Finding, q)
	}
}

func TestEditSnapshotMergeIntoLatestNoneConflictIsAFailedResultNamingTheDevicesAndCreatesNothing(t *testing.T) {
	f := mergeZip(t)
	routes := map[string]fwdtest.Handler{
		snapsPath:                         ready("s1"),
		"POST /api/networks/n1/snapshots": fwdtest.Const(409, map[string]any{"message": "Conflicting devices", "conflictDevices": []string{"edge1", "edge2"}}),
	}
	r, _ := mustRun(t, "edit-snapshot", routes, mergeBody(f, "NONE", "")+`,"apply":true}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "2 device(s) are in both") || !strings.Contains(jsonOf(r), "edge1") || !strings.Contains(jsonOf(r), "edge2") || !strings.Contains(strings.Join(r.Limits, " "), "PRESERVE_EXISTING") {
		t.Fatalf("%s %s %v", r.Status, r.Finding, r.Limits)
	}
	if len(r.Changes) != 0 {
		t.Errorf("nothing was created: an applied result lists no change: %+v", r.Changes)
	}
}

func TestEditSnapshotMergeIntoLatestRefusesWhatDoesNotApply(t *testing.T) {
	f := mergeZip(t)
	routes := map[string]fwdtest.Handler{snapsPath: ready("s1")}
	for name, in := range map[string]string{
		"bad type":        mergeBody(f, "REPLACE", "") + `}`,
		"lower case":      mergeBody(f, "none", "") + `}`,
		"two files":       `{"network_id":"n1","action":"import","definition":{"files":["` + f + `","` + f + `"],"merge_into_latest":"NONE"}}`,
		"exclude failed":  mergeBody(f, "NONE", `,"exclude_failed_devices":true`) + `}`,
		"skip processing": mergeBody(f, "NONE", `,"skip_processing":true`) + `}`,
		"missing file":    `{"network_id":"n1","action":"import","definition":{"files":["/nonexistent.zip"],"merge_into_latest":"NONE"}}`,
	} {
		if _, srv, err := runSkill(t, "edit-snapshot", routes, in); err == nil || writes(srv) != 0 {
			t.Errorf("%s must be refused before any write", name)
		}
	}
	empty := t.TempDir() + "/e.zip"
	os.WriteFile(empty, nil, 0o600)
	if _, _, err := runSkill(t, "edit-snapshot", routes, mergeBody(empty, "NONE", "")+`}`); err == nil {
		t.Errorf("an empty file is refused")
	}
}

func TestEditSnapshotMergeIntoLatestARealRefusalIsStillAnError(t *testing.T) {
	f := mergeZip(t)
	routes := map[string]fwdtest.Handler{
		snapsPath:                         ready("s1"),
		"POST /api/networks/n1/snapshots": fwdtest.Const(400, map[string]any{"message": "connection names are not unique"}),
	}
	if _, _, err := runSkill(t, "edit-snapshot", routes, mergeBody(f, "PRESERVE_EXISTING", "")+`,"apply":true}`); err == nil || !strings.Contains(err.Error(), "nothing is known to have been created") {
		t.Errorf("a 400 is an error that says nothing was created: %v", err)
	}
}

// A Predict snapshot is not the network's latest collected snapshot: the dry run names the newest one that is not predicted.
func TestEditSnapshotMergeIntoLatestDryRunSkipsPredictedSnapshotsWhenNamingTheLatest(t *testing.T) {
	f := mergeZip(t)
	routes := map[string]fwdtest.Handler{
		snapsPath: fwdtest.Snapshots(fwdtest.Snap("pred9", "PROCESSED", "PREDICT", "2026-09-03T00:00:00.000Z"), fwdtest.Snap("real2", "PROCESSED", "COLLECTION", "2026-09-02T00:00:00.000Z"), fwdtest.Snap("real1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")),
	}
	r, _ := mustRun(t, "edit-snapshot", routes, mergeBody(f, "NONE", "")+`}`)
	if !strings.Contains(jsonOf(r), `"latest_processed_snapshot":"real2"`) {
		t.Errorf("the newest non-predicted snapshot is named: %s", jsonOf(r))
	}
}

func mergeSnaps(extra ...any) map[string]any {
	l := []any{map[string]any{"id": "old1", "state": "PROCESSED", "processingTrigger": "COLLECTION"}}
	return map[string]any{"snapshots": append(l, extra...)}
}

func TestEditSnapshotMergeIntoLatestGatewayTimeoutThatKeptTheFileReportsTheNewSnapshot(t *testing.T) {
	defer skills.ImportPollHooks(time.Millisecond, 200*time.Millisecond)()
	f := mergeZip(t)
	uploaded := false
	routes := map[string]fwdtest.Handler{
		"POST /api/networks/n1/snapshots": func(*http.Request, []byte) (int, any) {
			uploaded = true
			return 504, map[string]any{"message": "stream timeout"}
		},
		"GET /api/networks/n1/snapshots": func(*http.Request, []byte) (int, any) {
			if !uploaded {
				return 200, mergeSnaps()
			}
			return 200, mergeSnaps(map[string]any{"id": "new9", "state": "UNPACKING"})
		},
	}
	r, _ := mustRun(t, "edit-snapshot", routes, mergeBody(f, "PRESERVE_EXISTING", "")+`,"apply":true}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "new9") || !strings.Contains(strings.Join(r.Limits, " "), "not retried") {
		t.Fatalf("%s %s %v", r.Status, r.Finding, r.Limits)
	}
}

func TestEditSnapshotMergeIntoLatestTimeoutWithNoNewSnapshotIsUnknownAndMentionsLowDisk(t *testing.T) {
	defer skills.ImportPollHooks(time.Millisecond, 20*time.Millisecond)()
	f := mergeZip(t)
	routes := map[string]fwdtest.Handler{
		"POST /api/networks/n1/snapshots": fwdtest.Const(503, map[string]any{"message": "Could not create new snapshot, not enough disk space"}),
		"GET /api/networks/n1/snapshots":  fwdtest.Const(200, mergeSnaps()),
	}
	r, _ := mustRun(t, "edit-snapshot", routes, mergeBody(f, "NONE", "")+`,"apply":true}`)
	if r.Status != result.Unknown || !strings.Contains(strings.Join(r.Limits, " "), "do NOT retry") || !strings.Contains(strings.Join(r.Limits, " "), "low disk") {
		t.Fatalf("%s %s %v", r.Status, r.Finding, r.Limits)
	}
}

// When the snapshots could not be listed before the upload, an existing snapshot cannot be told from a new one: an ambiguous error is unknown, never an old snapshot reported as the result.
func TestEditSnapshotAmbiguousUploadWithNoBeforeListIsUnknownNotAnOldSnapshot(t *testing.T) {
	defer skills.ImportPollHooks(time.Millisecond, 20*time.Millisecond)()
	f := mergeZip(t)
	listed := 0
	routes := map[string]fwdtest.Handler{
		"POST /api/networks/n1/snapshots": fwdtest.Const(504, map[string]any{"message": "stream timeout"}),
		"GET /api/networks/n1/snapshots": func(*http.Request, []byte) (int, any) {
			listed++
			if listed == 1 {
				return 500, map[string]any{"message": "list failed"}
			}
			return 200, mergeSnaps()
		},
	}
	for name, in := range map[string]string{"merge": mergeBody(f, "NONE", "") + `,"apply":true}`, "import": `{"network_id":"n1","action":"import","apply":true,"definition":{"files":["` + f + `"]}}`} {
		listed = 0
		r, _ := mustRun(t, "edit-snapshot", routes, in)
		if r.Status != result.Unknown || strings.Contains(r.Finding, "old1") {
			t.Errorf("%s: an old snapshot must not be reported as the new one: %s %s", name, r.Status, r.Finding)
		}
	}
}

func TestEditSnapshotMergeIntoLatestSendsPreserveExistingTheNoteAndNoTimeLimit(t *testing.T) {
	f := mergeZip(t)
	var q map[string][]string
	routes := map[string]fwdtest.Handler{
		snapsPath: ready("s1"),
		"POST /api/networks/n1/snapshots": func(r *http.Request, _ []byte) (int, any) {
			q = r.URL.Query()
			return 202, map[string]any{"id": "m1", "state": "UNPACKING"}
		},
	}
	r, _ := mustRun(t, "edit-snapshot", routes, mergeBody(f, "PRESERVE_EXISTING", "")+`,"apply":true}`)
	if r.Status != result.OK || q["mergeType"] == nil || q["mergeType"][0] != "PRESERVE_EXISTING" || q["note"] == nil || q["note"][0] != "merge" {
		t.Fatalf("%s query=%v", r.Status, q)
	}
}

func TestEditSnapshotMergeIntoLatestCapsTheConflictListAndRefusesAnEmptyOne(t *testing.T) {
	f := mergeZip(t)
	var names []string
	for i := 0; i < 130; i++ {
		names = append(names, fmt.Sprintf("dev%03d", i))
	}
	routes := map[string]fwdtest.Handler{snapsPath: ready("s1"), "POST /api/networks/n1/snapshots": fwdtest.Const(409, map[string]any{"message": "Conflicting devices", "conflictDevices": names})}
	r, _ := mustRun(t, "edit-snapshot", routes, mergeBody(f, "NONE", "")+`,"apply":true}`)
	if !strings.Contains(jsonOf(r), `"conflicting_devices_total":130`) || strings.Contains(jsonOf(r), "dev100") || !strings.Contains(jsonOf(r), "dev099") || len(r.Omitted) == 0 {
		t.Errorf("100 shown of 130, declared: omitted=%v", r.Omitted)
	}
	routes["POST /api/networks/n1/snapshots"] = fwdtest.Const(409, map[string]any{"message": "Conflicting devices"})
	if _, _, err := runSkill(t, "edit-snapshot", routes, mergeBody(f, "NONE", "")+`,"apply":true}`); err == nil {
		t.Errorf("a 409 naming no device is not reported as 0 conflicts")
	}
}

func TestEditSnapshotMergeIntoLatestDryRunSaysItCannotKnowConflictsForNone(t *testing.T) {
	f := mergeZip(t)
	r, _ := mustRun(t, "edit-snapshot", map[string]fwdtest.Handler{snapsPath: ready("s1")}, mergeBody(f, "NONE", "")+`}`)
	if !strings.Contains(r.Finding, "ZIP was not read") {
		t.Errorf("the NONE dry run says conflicts are not known: %s", r.Finding)
	}
	r, _ = mustRun(t, "edit-snapshot", map[string]fwdtest.Handler{snapsPath: fwdtest.Const(500, map[string]any{"message": "x"})}, mergeBody(f, "NONE", "")+`}`)
	if !strings.Contains(strings.Join(r.Limits, " "), "could not be listed") {
		t.Errorf("a failed listing is said, not 'no snapshot': %v", r.Limits)
	}
}

// The snapshot a merge starts from is the newest PROCESSED one: a snapshot still unpacking or a draft is not it.
func TestEditSnapshotMergeIntoLatestNamesTheNewestProcessedSnapshotNotAnUnpackingOne(t *testing.T) {
	f := mergeZip(t)
	routes := map[string]fwdtest.Handler{snapsPath: fwdtest.Snapshots(
		fwdtest.Snap("unpack9", "UNPACKING", "COLLECTION", "2026-09-04T00:00:00.000Z"),
		fwdtest.Snap("done2", "PROCESSED", "COLLECTION", "2026-09-02T00:00:00.000Z"),
		fwdtest.Snap("done1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z"))}
	r, _ := mustRun(t, "edit-snapshot", routes, mergeBody(f, "NONE", "")+`}`)
	if !strings.Contains(jsonOf(r), `"latest_processed_snapshot":"done2"`) {
		t.Errorf("%s", jsonOf(r))
	}
}

// Forward answered 202 but gave neither a Location header nor a snapshot id: the merge was accepted, so this is not a plain failure and not a pass.
func TestEditSnapshotMergeIntoLatestAcceptedWithNoSnapshotIdIsUnknownNotAFailure(t *testing.T) {
	defer skills.ImportPollHooks(time.Millisecond, 20*time.Millisecond)()
	f := mergeZip(t)
	routes := map[string]fwdtest.Handler{
		"POST /api/networks/n1/snapshots": fwdtest.Const(202, map[string]any{}),
		"GET /api/networks/n1/snapshots":  fwdtest.Const(200, mergeSnaps()),
	}
	r, _, err := runSkill(t, "edit-snapshot", routes, mergeBody(f, "NONE", "")+`,"apply":true}`)
	if err != nil || r.Status != result.Unknown || !strings.Contains(r.Finding, "accepted the merge (202)") {
		t.Errorf("%v %s %s", err, r.Status, r.Finding)
	}
}
