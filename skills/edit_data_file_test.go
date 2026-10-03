package skills_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
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
