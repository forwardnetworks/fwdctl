package skills_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

func dataFilesWorld(files *[]map[string]any) map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		"GET /api/data-files": func(*http.Request, []byte) (int, any) { return 200, *files },
		"POST /api/data-files": func(r *http.Request, body []byte) (int, any) {
			if r.URL.Query().Get("action") != "" {
				return 400, map[string]any{"message": "unexpected action"}
			}
			_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil {
				return 400, map[string]any{"message": err.Error()}
			}
			mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
			form, err := mr.ReadForm(1 << 20)
			if err != nil {
				return 400, map[string]any{"message": err.Error()}
			}
			var req map[string]any
			_ = json.Unmarshal([]byte(form.Value["request"][0]), &req)
			fh := form.File["file"][0]
			f, _ := fh.Open()
			content, _ := io.ReadAll(f)
			name := strings.ToLower(req["name"].(string))
			nqeName, _ := req["nqeName"].(string)
			if nqeName == "" {
				nqeName = strings.TrimSuffix(name, "."+strings.ToLower(req["fileType"].(string)))
			}
			created := map[string]any{"name": name, "nqeName": nqeName, "type": req["fileType"], "networkIds": []string{}}
			*files = append(*files, created)
			_ = content
			return 201, created
		},
		"POST /api/networks/n1/data-files/sites.csv": func(*http.Request, []byte) (int, any) {
			for _, f := range *files {
				if f["name"] == "sites.csv" {
					f["networkIds"] = append(f["networkIds"].([]string), "n1")
				}
			}
			return 204, nil
		},
		"DELETE /api/networks/n1/data-files/sites.csv": func(*http.Request, []byte) (int, any) {
			for _, f := range *files {
				if f["name"] == "sites.csv" {
					ids := f["networkIds"].([]string)
					var kept []string
					for _, id := range ids {
						if id != "n1" {
							kept = append(kept, id)
						}
					}
					f["networkIds"] = kept
				}
			}
			return 204, nil
		},
		"DELETE /api/data-files/sites.csv": func(*http.Request, []byte) (int, any) {
			var kept []map[string]any
			for _, f := range *files {
				if f["name"] != "sites.csv" {
					kept = append(kept, f)
				}
			}
			*files = kept
			return 204, nil
		},
		"GET /api/networks/n1/data-files": func(*http.Request, []byte) (int, any) {
			var names []string
			for _, f := range *files {
				for _, id := range f["networkIds"].([]string) {
					if id == "n1" {
						names = append(names, f["name"].(string))
					}
				}
			}
			return 200, names
		},
	}
}

func TestEditDataFileUploadDryRunThenAppliesAndReadsBack(t *testing.T) {
	files := []map[string]any{}
	in := `{"action":"upload","name":"sites.csv","file_type":"CSV","content":"site,owner\nnyc,ops\n"`
	r, srv := mustRun(t, "edit-data-file", dataFilesWorld(&files), in+`}`)
	if r.Status != result.OK || r.Mode != result.ModeDryRun || writes(srv) != 0 || !r.Changes[0].Reversible || !strings.Contains(r.Changes[0].Undo, "action delete") {
		t.Fatalf("%s %s writes=%d undo=%q", r.Status, r.Finding, writes(srv), r.Changes[0].Undo)
	}
	r, _ = mustRun(t, "edit-data-file", dataFilesWorld(&files), in+`,"apply":true}`)
	if r.Status != result.OK || !r.Changes[0].Applied || len(files) != 1 || files[0]["nqeName"] != "sites" {
		t.Fatalf("%s %s files=%v", r.Status, r.Finding, files)
	}
	// uploading the same name again is refused, not overwritten
	r, srv = mustRun(t, "edit-data-file", dataFilesWorld(&files), in+`,"apply":true}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "already exists") || writes(srv) != 0 {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
}

func TestEditDataFileRefusesWhatItShould(t *testing.T) {
	files := []map[string]any{}
	for in, want := range map[string]string{
		`{"action":"upload","name":"s.csv","file_type":"CSV","content":"x","network_id":"n1"}`: "organization-wide",
		`{"action":"upload","name":"s.csv","file_type":"XLSX","content":"x"}`:                  "binary format",
		`{"action":"upload","name":"s.csv","file_type":"STIG","content":"x"}`:                  "fixed name",
		`{"action":"upload","name":"s.csv","file_type":"JSON","headers":["a"],"content":"x"}`:  "CSV",
		`{"action":"attach","name":"nope.csv","network_id":"n1"}`:                              "no data file named",
		`{"action":"attach","name":"s.csv","network_id":"n1","file_type":"CSV"}`:               "belong to upload",
	} {
		_, _, err := runSkill(t, "edit-data-file", dataFilesWorld(&files), in)
		if err == nil {
			// attach on a missing file returns a Failed result, not an error; re-run through mustRun for that case
			r, _ := mustRun(t, "edit-data-file", dataFilesWorld(&files), in)
			if r.Status != result.Failed || !strings.Contains(r.Finding, want) {
				t.Errorf("%s: want %q, got %s %s", in, want, r.Status, r.Finding)
			}
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want %q, got %v", in, want, err)
		}
	}
}

func TestEditDataFileAttachAndDetachRoundTripWithReadBack(t *testing.T) {
	files := []map[string]any{{"name": "sites.csv", "nqeName": "sites", "type": "CSV", "networkIds": []string{}}}
	world := dataFilesWorld(&files)
	r, srv := mustRun(t, "edit-data-file", world, `{"action":"attach","name":"sites.csv","network_id":"n1"}`)
	if r.Status != result.OK || r.Mode != result.ModeDryRun || writes(srv) != 0 || !strings.Contains(strings.Join(r.Limits, " "), "MISSING") {
		t.Fatalf("%s %s %v", r.Status, r.Finding, r.Limits)
	}
	r, _ = mustRun(t, "edit-data-file", world, `{"action":"attach","name":"sites.csv","network_id":"n1","apply":true}`)
	if r.Status != result.OK || !r.Changes[0].Applied {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	// attaching again is a no-op
	r, _ = mustRun(t, "edit-data-file", world, `{"action":"attach","name":"sites.csv","network_id":"n1","apply":true}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "already") || len(r.Changes) != 0 {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	r, _ = mustRun(t, "edit-data-file", world, `{"action":"detach","name":"sites.csv","network_id":"n1","apply":true}`)
	if r.Status != result.OK || !r.Changes[0].Applied || !strings.Contains(r.Changes[0].Undo, "attach") {
		t.Fatalf("%s %s undo=%q", r.Status, r.Finding, r.Changes[0].Undo)
	}
}

func TestEditDataFileRefusesTheStigFileByEitherAction(t *testing.T) {
	files := []map[string]any{{"name": "stig-policy", "nqeName": "stigPolicy", "type": "STIG", "networkIds": []string{"n1"}}}
	world := dataFilesWorld(&files)
	r, srv := mustRun(t, "edit-data-file", world, `{"action":"detach","name":"stig-policy","network_id":"n1","apply":true}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "cannot be excluded") || writes(srv) != 0 {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
}

func TestEditDataFileDeleteNeedsConfirmShowsWhereItIsAttachedAndSaysThereIsNoUndo(t *testing.T) {
	files := []map[string]any{{"name": "sites.csv", "nqeName": "sites", "type": "CSV", "networkIds": []string{"n1", "n2"}}}
	r, srv := mustRun(t, "edit-data-file", dataFilesWorld(&files), `{"action":"delete","name":"sites.csv"}`)
	if r.Status != result.OK || r.Mode != result.ModeDryRun || writes(srv) != 0 || r.Changes[0].Reversible || !strings.Contains(r.Finding, "attached to 2 network(s)") {
		t.Fatalf("dry run: %s %s %+v writes=%d", r.Status, r.Finding, r.Changes, writes(srv))
	}
	if _, _, err := runSkill(t, "edit-data-file", dataFilesWorld(&files), `{"action":"delete","name":"sites.csv","apply":true}`); err == nil || !strings.Contains(err.Error(), "confirm") {
		t.Errorf("apply without confirm must be refused: %v", err)
	}
	if _, _, err := runSkill(t, "edit-data-file", dataFilesWorld(&files), `{"action":"delete","name":"sites.csv","network_id":"n1"}`); err == nil {
		t.Errorf("delete takes no network_id: it is org-wide")
	}
	r, srv = mustRun(t, "edit-data-file", dataFilesWorld(&files), `{"action":"delete","name":"sites.csv","confirm":"sites.csv","apply":true}`)
	if r.Status != result.OK || !r.Changes[0].Applied || len(files) != 0 || writes(srv) != 1 {
		t.Fatalf("apply: %s %s files=%v", r.Status, r.Finding, files)
	}
	if r, _ := mustRun(t, "edit-data-file", dataFilesWorld(&files), `{"action":"delete","name":"sites.csv"}`); r.Status != result.Failed {
		t.Errorf("a missing file is a refusal: %s", r.Status)
	}
}

// A query reads a data file as network.extensions.<nqe_name>, so the name has to be a plain NQE identifier. A name Forward would derive with a hyphen is not one: the dry run says so and suggests the camelCase
// spelling the org's existing files use.
func TestEditDataFileWarnsWhenTheNQENameIsNotAnIdentifier(t *testing.T) {
	files := []map[string]any{}
	r, _ := mustRun(t, "edit-data-file", dataFilesWorld(&files), `{"action":"upload","name":"endpoint-lifecycle.csv","file_type":"CSV","content":"a,b\n1,2\n"}`)
	all := strings.Join(r.Limits, " ")
	if !strings.Contains(all, "not a plain NQE identifier") || !strings.Contains(all, "endpointLifecycle") {
		t.Errorf("limits = %v", r.Limits)
	}
	r, _ = mustRun(t, "edit-data-file", dataFilesWorld(&files), `{"action":"upload","name":"endpoint-lifecycle.csv","nqe_name":"endpointLifecycle","file_type":"CSV","content":"a,b\n1,2\n"}`)
	if strings.Contains(strings.Join(r.Limits, " "), "not a plain NQE identifier") {
		t.Errorf("a valid nqe_name should not warn: %v", r.Limits)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "network.extensions.endpointLifecycle.value") {
		t.Errorf("the limits should show how to read the rows: %v", r.Limits)
	}
}

func replaceWorld(stored string, assess string, replaced *string) map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		"GET /api/data-files": func(*http.Request, []byte) (int, any) {
			return 200, []map[string]any{{"name": "acl.csv", "nqeName": "acl", "type": "CSV", "networkIds": []string{}}}
		},
		"GET /api/data-files/acl.csv": func(*http.Request, []byte) (int, any) { return 200, []byte(stored) },
		"POST /api/data-files/acl.csv": func(r *http.Request, body []byte) (int, any) {
			if r.URL.Query().Get("action") == "assess" {
				return 200, map[string]any{"result": assess}
			}
			*replaced = "sent"
			return 200, map[string]any{"name": "acl.csv", "nqeName": "acl", "type": "CSV", "networkIds": []string{}}
		},
	}
}

func TestEditDataFileUploadFromFilePathPastTheInlineCap(t *testing.T) {
	big := filepath.Join(t.TempDir(), "big.csv")
	if err := os.WriteFile(big, []byte("a,b\n"+strings.Repeat("xxxxxxxxxxxxxxxxxxxx,yyyyyyyyyyyyyyyyyyyy\n", 20000)), 0o600); err != nil {
		t.Fatal(err)
	}
	files := []map[string]any{}
	in := `{"action":"upload","name":"big.csv","file_type":"CSV","file_path":` + strconv.Quote(big) + `}`
	r, _ := mustRun(t, "edit-data-file", dataFilesWorld(&files), in)
	if r.Status != result.OK || r.Changes[0].After.(map[string]any)["rows"] != 20001 {
		t.Fatalf("%s %s %v", r.Status, r.Finding, r.Changes)
	}
	// inline stays capped, and content with file_path is ambiguous
	if _, _, err := runSkill(t, "edit-data-file", dataFilesWorld(&files), `{"action":"upload","name":"b.csv","file_type":"CSV","content":"`+strings.Repeat("x", 500001)+`"}`); err == nil || !strings.Contains(err.Error(), "file_path") {
		t.Fatalf("inline cap error should point at file_path: %v", err)
	}
	if _, _, err := runSkill(t, "edit-data-file", dataFilesWorld(&files), `{"action":"upload","name":"b.csv","file_type":"CSV","content":"x","file_path":"`+big+`"}`); err == nil {
		t.Fatal("content and file_path together should be refused")
	}
}

func TestEditDataFileReplaceShowsLineCountsAssessesAndWrites(t *testing.T) {
	p := filepath.Join(t.TempDir(), "new.csv")
	_ = os.WriteFile(p, []byte("a,b\n1,2\n3,4\n5,6\n"), 0o600)
	in := `{"action":"replace","name":"acl.csv","file_path":` + strconv.Quote(p) + `}`
	var sent string
	r, srv := mustRun(t, "edit-data-file", replaceWorld("a,b\n1,2\n", "SUCCESS", &sent), in)
	b, a := r.Changes[0].Before.(map[string]any), r.Changes[0].After.(map[string]any)
	if r.Status != result.OK || r.Mode != result.ModeDryRun || b["lines"] != 2 || a["lines"] != 4 || a["lines_change"] != 2 || sent != "" || writes(srv) != 1 { // the one POST is Forward's read-only assessment
		t.Fatalf("%s %s before=%v after=%v sent=%q writes=%d", r.Status, r.Finding, b, a, sent, writes(srv))
	}
	r, _ = mustRun(t, "edit-data-file", replaceWorld("a,b\n1,2\n", "SUCCESS", &sent), strings.TrimSuffix(in, "}")+`,"apply":true}`)
	if r.Status != result.OK || !r.Changes[0].Applied || sent != "sent" {
		t.Fatalf("%s %s sent=%q", r.Status, r.Finding, sent)
	}
	sent = ""
	r, _ = mustRun(t, "edit-data-file", replaceWorld("a,b\n1,2\n", "FAILURE", &sent), strings.TrimSuffix(in, "}")+`,"apply":true}`)
	if r.Status != result.Failed || sent != "" || !strings.Contains(r.Finding, "does not parse") {
		t.Fatalf("incompatible content must be refused: %s %s sent=%q", r.Status, r.Finding, sent)
	}
	r, _ = mustRun(t, "edit-data-file", replaceWorld("", "SUCCESS", &sent), `{"action":"replace","name":"nope.csv","content":"x"}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "no data file named") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
}
