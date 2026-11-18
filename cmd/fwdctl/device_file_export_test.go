package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
)

// exportRoutes is a snapshot of three devices: a router with one config, a Palo Alto style firewall that keeps its candidate config, running config and a command output under
// several file names (one with characters that are not safe in a path), and a device with no files at all.
func exportRoutes(extra map[string]fwdtest.Handler) map[string]fwdtest.Handler {
	files := func(list ...map[string]any) fwdtest.Handler {
		return fwdtest.Const(200, map[string]any{"files": list})
	}
	routes := map[string]fwdtest.Handler{
		"GET /api/networks/n1/snapshots":        fwdtest.Snapshots(fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z")),
		"POST /api/nqe":                         fwdtest.Const(200, map[string]any{"items": []any{map[string]any{"name": "r1"}, map[string]any{"name": "fw-1"}, map[string]any{"name": "empty-1"}}, "totalNumItems": 3}),
		"GET /api/networks/n1/devices/r1/files": files(map[string]any{"name": "config.txt", "bytes": 40, "command": "show running-config"}),
		"GET /api/networks/n1/devices/fw-1/files": files(
			map[string]any{"name": "panos-config.xml", "bytes": 30, "command": "show config running"},
			map[string]any{"name": "panos-policy.xml", "bytes": 30, "command": "show config policy"},
			map[string]any{"name": "show system info.txt", "bytes": 20, "command": "show system info"}),
		"GET /api/networks/n1/devices/empty-1/files":                   files(),
		"GET /api/networks/n1/devices/r1/files/config.txt":             fwdtest.Const(200, []byte("hostname r1\nsnmp-server community s3cr3tvalue RO\n")),
		"GET /api/networks/n1/devices/fw-1/files/panos-config.xml":     fwdtest.Const(200, []byte("<config>running</config>\n")),
		"GET /api/networks/n1/devices/fw-1/files/panos-policy.xml":     fwdtest.Const(200, []byte("<config>policy</config>\n")),
		"GET /api/networks/n1/devices/fw-1/files/show system info.txt": fwdtest.Const(200, []byte("hostname fw-1\n")),
	}
	for k, v := range extra {
		routes[k] = v
	}
	return routes
}

func readManifest(t *testing.T, dir string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func lastJSON(t *testing.T, stdout string) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &m); err != nil {
		t.Fatalf("last line is not JSON: %q (%v)", lines[len(lines)-1], err)
	}
	return m
}

// A platform with several files (a Palo Alto firewall) gets every one of them, each under Forward's own name made safe for a path, and the device with no files is listed, not dropped.
func TestDeviceFileExportWritesEveryFileOfEveryDeviceAndListsWhatHadNone(t *testing.T) {
	out := filepath.Join(t.TempDir(), "configs")
	code, stdout, errb := call(t, []string{"device-file", "export", "n1", "--out", out}, "", exportRoutes(nil))
	res := lastJSON(t, stdout)
	if code != 1 || res["status"] != "partial" || res["files_written"] != float64(4) || res["devices"] != float64(3) {
		t.Fatalf("exit %d (%s): %v", code, errb, res)
	}
	for _, p := range []string{"r1/config.txt", "fw-1/panos-config.xml", "fw-1/panos-policy.xml", "fw-1/show_system_info.txt"} {
		if _, err := os.Stat(filepath.Join(out, p)); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
	probs, _ := res["problems"].([]any)
	if len(probs) != 1 || !strings.Contains(probs[0].(map[string]any)["device"].(string), "empty-1") || probs[0].(map[string]any)["status"] != "no_files" {
		t.Errorf("a device with no files is a listed problem: %v", probs)
	}
	m := readManifest(t, out)
	devs := m["devices"].([]any)
	var fw map[string]any
	for _, d := range devs {
		if d.(map[string]any)["device"] == "fw-1" {
			fw = d.(map[string]any)
		}
	}
	files := fw["files"].([]any)
	if len(files) != 3 {
		t.Fatalf("fw-1 files: %v", files)
	}
	var sys map[string]any
	for _, f := range files {
		if f.(map[string]any)["name"] == "show system info.txt" {
			sys = f.(map[string]any)
		}
	}
	if sys["path"] != "fw-1/show_system_info.txt" || sys["status"] != "written" || sys["sha256"] == "" || sys["lines"] != float64(1) {
		t.Errorf("the manifest keeps the real name and records the path written: %v", sys)
	}
	if st, _ := os.Stat(filepath.Join(out, "fw-1")); st.Mode().Perm() != 0o700 {
		t.Errorf("device folders are 700: %v", st.Mode().Perm())
	}
	if st, _ := os.Stat(filepath.Join(out, "r1", "config.txt")); st.Mode().Perm() != 0o600 {
		t.Errorf("files are 600: %v", st.Mode().Perm())
	}
}

func TestDeviceFileExportRedactsByDefaultAndNoRedactIsRaw(t *testing.T) {
	out := filepath.Join(t.TempDir(), "a")
	call(t, []string{"device-file", "export", "n1", "--out", out, "--device", "r1"}, "", exportRoutes(nil))
	got, _ := os.ReadFile(filepath.Join(out, "r1", "config.txt"))
	if strings.Contains(string(got), "s3cr3tvalue") || !strings.Contains(string(got), "<redacted>") {
		t.Errorf("redacted by default: %q", got)
	}
	m := readManifest(t, out)
	if m["redacted"] != true {
		t.Errorf("manifest says redacted: %v", m["redacted"])
	}
	raw := filepath.Join(t.TempDir(), "b")
	call(t, []string{"device-file", "export", "n1", "--out", raw, "--device", "r1", "--no-redact"}, "", exportRoutes(nil))
	got, _ = os.ReadFile(filepath.Join(raw, "r1", "config.txt"))
	if !strings.Contains(string(got), "s3cr3tvalue") {
		t.Errorf("--no-redact writes the raw text: %q", got)
	}
}

// --files keeps only the matching names; a device left with nothing is reported as such; --dry-run writes nothing and lists the plan with sizes.
func TestDeviceFileExportFilterAndDryRun(t *testing.T) {
	out := filepath.Join(t.TempDir(), "d")
	code, stdout, _ := call(t, []string{"device-file", "export", "n1", "--out", out, "--files", "^panos-", "--dry-run", "--device", "fw-1", "--device", "r1"}, "", exportRoutes(nil))
	res := lastJSON(t, stdout)
	if _, err := os.Stat(out); err == nil {
		t.Errorf("a dry run writes nothing")
	}
	if res["dry_run"] != true || res["files_planned"] != float64(2) || res["bytes_planned"] != float64(60) {
		t.Errorf("plan: %v", res)
	}
	if code != 1 { // r1 has no file matching: a listed problem, so partial
		t.Errorf("exit %d", code)
	}
	probs := res["problems"].([]any)
	if len(probs) != 1 || !strings.Contains(probs[0].(map[string]any)["error"].(string), "none matches --files") {
		t.Errorf("r1 matched nothing: %v", probs)
	}
}

func TestDeviceFileExportNeverTouchesAnExistingPathAndChecksTheTotalFirst(t *testing.T) {
	dir := t.TempDir()
	code, _, errb := call(t, []string{"device-file", "export", "n1", "--out", dir}, "", exportRoutes(nil))
	if code == 0 || !strings.Contains(errb, "already exists") {
		t.Fatalf("an existing folder is refused: %d %s", code, errb)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("the existing folder was touched: %v", ents)
	}
	out := filepath.Join(t.TempDir(), "big")
	routes := exportRoutes(map[string]fwdtest.Handler{
		"GET /api/networks/n1/devices/r1/files": fwdtest.Const(200, map[string]any{"files": []any{map[string]any{"name": "config.txt", "bytes": 3 << 20}}}),
	})
	code, _, errb = call(t, []string{"device-file", "export", "n1", "--out", out, "--device", "r1", "--max-total-mib", "1"}, "", routes)
	if code == 0 || !strings.Contains(errb, "--max-total-mib") {
		t.Fatalf("a total over the limit refuses before anything is written: %d %s", code, errb)
	}
	if _, err := os.Stat(out); err == nil {
		t.Errorf("nothing may be created when the total is refused")
	}
}

// a file larger than --max-mib is not written (never a cut file passing for the whole one), is listed as too_large, and the others are still written.
func TestDeviceFileExportTooLargeFileIsListedAndNotWritten(t *testing.T) {
	big := strings.Repeat("x\n", 1<<20+1) // a little over 2 MiB
	routes := exportRoutes(map[string]fwdtest.Handler{
		"GET /api/networks/n1/devices/fw-1/files/panos-policy.xml": fwdtest.Const(200, []byte(big)),
	})
	out := filepath.Join(t.TempDir(), "e")
	code, stdout, _ := call(t, []string{"device-file", "export", "n1", "--out", out, "--device", "fw-1", "--max-mib", "1"}, "", routes)
	res := lastJSON(t, stdout)
	if code != 1 || res["files_written"] != float64(2) {
		t.Fatalf("exit %d: %v", code, res)
	}
	if _, err := os.Stat(filepath.Join(out, "fw-1", "panos-policy.xml")); err == nil {
		t.Errorf("an oversize file must not be written")
	}
	probs := res["problems"].([]any)
	if len(probs) != 1 || probs[0].(map[string]any)["status"] != "too_large" || probs[0].(map[string]any)["file"] != "panos-policy.xml" {
		t.Errorf("the oversize file is listed: %v", probs)
	}
}

// a failed file or device listing is a listed problem and the rest is still written.
func TestDeviceFileExportAFailedDownloadAndAFailedListingAreListed(t *testing.T) {
	routes := exportRoutes(map[string]fwdtest.Handler{
		"GET /api/networks/n1/devices/fw-1/files/panos-config.xml": func(*http.Request, []byte) (int, any) { return 500, map[string]any{"message": "boom"} },
		"GET /api/networks/n1/devices/empty-1/files":               func(*http.Request, []byte) (int, any) { return 404, map[string]any{"message": "no device"} },
	})
	out := filepath.Join(t.TempDir(), "f")
	code, stdout, _ := call(t, []string{"device-file", "export", "n1", "--out", out}, "", routes)
	res := lastJSON(t, stdout)
	if code != 1 || res["status"] != "partial" || res["files_written"] != float64(3) {
		t.Fatalf("exit %d: %v", code, res)
	}
	seen := map[string]string{}
	for _, p := range res["problems"].([]any) {
		m := p.(map[string]any)
		seen[m["device"].(string)] = m["status"].(string)
	}
	if seen["fw-1"] != "error" || seen["empty-1"] != "list_error" {
		t.Errorf("problems: %v", seen)
	}
}

// Two names that map to one safe name keep both files (the second gets a short hash), and a name that is only dots or traversal is made harmless.
func TestExportNamesAreSafeAndNeverCollide(t *testing.T) {
	taken := map[string]bool{}
	a, b := uniqueName("core 1", taken), uniqueName("core/1", taken)
	if a != "core_1" || b == a || !strings.HasPrefix(b, "core_1-") {
		t.Errorf("collision: %q %q", a, b)
	}
	for _, bad := range []string{"..", ".", "", "../../etc/passwd", "/abs/path"} {
		s := safeName(bad)
		if s == "" || s == "." || s == ".." || strings.ContainsAny(s, "/\\") || strings.Trim(s, ".") == "" {
			t.Errorf("%q -> %q is not a safe element", bad, s)
		}
	}
	if safeName("../../etc/passwd") != ".._.._etc_passwd" && !strings.Contains(safeName("../../etc/passwd"), "etc_passwd") {
		t.Errorf("traversal kept: %q", safeName("../../etc/passwd"))
	}
}

// A clean export exits 0 with status ok and an empty problems list (an array, not null), and the manifest is there.
func TestDeviceFileExportCleanRunIsOkWithAnEmptyProblemsList(t *testing.T) {
	out := filepath.Join(t.TempDir(), "ok")
	code, stdout, errb := call(t, []string{"device-file", "export", "n1", "--out", out, "--device", "r1", "--device", "fw-1"}, "", exportRoutes(nil))
	res := lastJSON(t, stdout)
	probs, isList := res["problems"].([]any)
	if code != 0 || res["status"] != "ok" || !isList || len(probs) != 0 || res["files_written"] != float64(4) {
		t.Fatalf("exit %d (%s): %v", code, errb, res)
	}
	if res["manifest"] != filepath.Join(out, "manifest.json") {
		t.Errorf("manifest path: %v", res["manifest"])
	}
}

// A device whose safe name is manifest.json must not take the manifest's name (the export would end with files and no manifest), and a literal name equal to a hashed one still gets its own folder.
func TestExportDeviceNamedLikeTheManifestKeepsTheManifest(t *testing.T) {
	routes := exportRoutes(map[string]fwdtest.Handler{
		"GET /api/networks/n1/devices/manifest.json/files":            fwdtest.Const(200, map[string]any{"files": []any{map[string]any{"name": "config.txt", "bytes": 5}}}),
		"GET /api/networks/n1/devices/manifest.json/files/config.txt": fwdtest.Const(200, []byte("x\n")),
	})
	out := filepath.Join(t.TempDir(), "m")
	code, stdout, errb := call(t, []string{"device-file", "export", "n1", "--out", out, "--device", "manifest.json", "--device", "r1"}, "", routes)
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, errb, stdout)
	}
	if st, err := os.Stat(filepath.Join(out, "manifest.json")); err != nil || st.IsDir() {
		t.Fatalf("manifest.json must be the manifest file: %v", err)
	}
	m := readManifest(t, out)
	found := false
	for _, d := range m["devices"].([]any) {
		if d.(map[string]any)["device"] == "manifest.json" && d.(map[string]any)["dir"] != "manifest.json" {
			found = true
		}
	}
	if !found {
		t.Errorf("the device named manifest.json gets another folder: %v", m["devices"])
	}
	// the collision the loop exists for: "core 1" takes core_1, a literal name equal to what "core/1" would be hashed to takes that, and then "core/1" must still get a free name
	probe := map[string]bool{"core_1": true}
	hashed := uniqueName("core/1", probe) // core_1-<hash>
	taken := map[string]bool{}
	a := uniqueName("core 1", taken)
	l := uniqueName(hashed, taken)
	b := uniqueName("core/1", taken)
	if a == l || a == b || l == b || l != hashed {
		t.Errorf("names must stay distinct: %q %q %q (hashed form %q)", a, l, b, hashed)
	}
}

// When the listing gave no size, the total limit cannot be checked up front; the export stops at the limit while writing and says which files were not written.
func TestExportStopsAtTheTotalLimitWhenTheListingGaveNoSizes(t *testing.T) {
	big := strings.Repeat("x\n", 1<<20+1) // a little over 2 MiB
	routes := exportRoutes(map[string]fwdtest.Handler{
		"GET /api/networks/n1/devices/fw-1/files": fwdtest.Const(200, map[string]any{"files": []any{
			map[string]any{"name": "a.txt"}, map[string]any{"name": "b.txt"}, map[string]any{"name": "c.txt"}}}),
		"GET /api/networks/n1/devices/fw-1/files/a.txt": fwdtest.Const(200, []byte(big)),
		"GET /api/networks/n1/devices/fw-1/files/b.txt": fwdtest.Const(200, []byte(big)),
		"GET /api/networks/n1/devices/fw-1/files/c.txt": fwdtest.Const(200, []byte(big)),
	})
	out := filepath.Join(t.TempDir(), "t")
	code, stdout, _ := call(t, []string{"device-file", "export", "n1", "--out", out, "--device", "fw-1", "--max-total-mib", "1", "--jobs", "1"}, "", routes)
	res := lastJSON(t, stdout)
	if code != 1 || res["files_with_unknown_size"] != float64(3) {
		t.Fatalf("exit %d: %v", code, res)
	}
	if res["files_written"] != float64(1) || len(res["problems"].([]any)) != 2 {
		t.Errorf("the first file passes the limit, the other two are listed as not written: %v", res)
	}
}
