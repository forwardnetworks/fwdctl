package skills_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

const (
	diffFilesPath = "GET /api/diffs/s1/s2/files"
	dlBefore      = "GET /api/networks/n1/devices/r1/files/CONFIGURATION.txt"
)

func changedFiles() fwdtest.Handler {
	return fwdtest.Const(200, []any{map[string]any{"name": "r1", "hasConfigChange": true,
		"files": []any{map[string]any{"name": "r1,CONFIGURATION.txt"}}}})
}

// download answers by snapshotId, the only thing that tells the two sides apart.
func download(before, after string) fwdtest.Handler {
	return func(r *http.Request, _ []byte) (int, any) {
		if r.URL.Query().Get("snapshotId") == "s1" {
			return 200, []byte(before)
		}
		return 200, []byte(after)
	}
}

const cdIn = `{"network_id":"n1","before_snapshot_id":"s1","after_snapshot_id":"s2"`

func TestConfigDiffListsChangedDevicesWithDownloadableNames(t *testing.T) {
	r, _ := mustRun(t, "compare-device-config", map[string]fwdtest.Handler{snapsPath: twoSnaps(), diffFilesPath: changedFiles()}, cdIn+`}`)
	d := fmt.Sprint(r.Evidence[0].Detail)
	if r.Status != result.OK || !strings.Contains(d, "r1") || !strings.Contains(d, "CONFIGURATION.txt") || strings.Contains(d, "r1,CONFIGURATION") {
		t.Fatalf("%s", d)
	}
}

func TestConfigDiffShowsAddedAndRemovedLinesAndRedacts(t *testing.T) {
	r, srv := mustRun(t, "compare-device-config", map[string]fwdtest.Handler{snapsPath: twoSnaps(), diffFilesPath: changedFiles(),
		dlBefore: download("hostname r1\nntp server 1.1.1.1\nsnmp-server community old RO\n", "hostname r1\nntp server 2.2.2.2\nsnmp-server community new RO\n")},
		cdIn+`,"device":"r1"}`)
	d := fmt.Sprint(r.Evidence[0].Detail)
	if r.Status != result.OK || !strings.Contains(d, "2: ntp server 2.2.2.2") || !strings.Contains(d, "2: ntp server 1.1.1.1") {
		t.Fatalf("%s", d)
	}
	if strings.Contains(d, "community old") || strings.Contains(d, "community new") {
		t.Errorf("secret leaked: %s", d)
	}
	if !srv.Called("GET", "/api/networks/n1/devices/r1/files/CONFIGURATION.txt") {
		t.Error("file was not downloaded")
	}
}

func TestConfigDiffSameTextButListedChangedSaysSo(t *testing.T) {
	r, _ := mustRun(t, "compare-device-config", map[string]fwdtest.Handler{snapsPath: twoSnaps(), diffFilesPath: changedFiles(),
		dlBefore: download("a\nb\n", "b\na\n")}, cdIn+`,"device":"r1"}`)
	if !strings.Contains(r.Finding, "no line differs") {
		t.Fatalf("%s", r.Finding)
	}
}

func TestConfigDiffUnprocessedSnapshotAndUnreadableFilesAreUnknown(t *testing.T) {
	r, _ := mustRun(t, "compare-device-config", map[string]fwdtest.Handler{snapsPath: fwdtest.Snapshots(
		fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z"),
		fwdtest.Snap("s2", "FAILED", "COLLECTION", "2026-09-02T00:00:00.000Z"))}, cdIn+`}`)
	if r.Status != result.Unknown {
		t.Fatalf("status %s", r.Status)
	}
	r, _ = mustRun(t, "compare-device-config", map[string]fwdtest.Handler{snapsPath: twoSnaps(), diffFilesPath: changedFiles(),
		dlBefore: fwdtest.Const(404, map[string]any{"message": "no"})}, cdIn+`,"device":"r1"}`)
	if r.Status != result.Unknown {
		t.Fatalf("status %s", r.Status)
	}
}

func TestConfigDiffDeviceNotChangedIsAnAnswerWithALimit(t *testing.T) {
	r, _ := mustRun(t, "compare-device-config", map[string]fwdtest.Handler{snapsPath: twoSnaps(), diffFilesPath: changedFiles()}, cdIn+`,"device":"r9"}`)
	if r.Status != result.OK || !strings.Contains(strings.Join(r.Limits, "|"), "inspect-inventory") {
		t.Fatalf("%s %v", r.Status, r.Limits)
	}
}

func TestRefusedRequestIsRetriedOnceAndAServerErrorOnAReadIsRetriedAtMostFourTimes(t *testing.T) {
	calls := 0
	flaky := func(*http.Request, []byte) (int, any) {
		calls++
		if calls == 1 {
			return 429, map[string]any{"message": "slow down"}
		}
		return 200, []any{}
	}
	r, _ := mustRun(t, "compare-device-config", map[string]fwdtest.Handler{snapsPath: twoSnaps(), diffFilesPath: flaky}, cdIn+`}`)
	if r.Status != result.OK || calls != 2 {
		t.Fatalf("429 should be retried once: status %s calls %d", r.Status, calls)
	}
	calls = 0
	boom := func(*http.Request, []byte) (int, any) { calls++; return 500, map[string]any{"message": "boom"} }
	if _, _, err := runSkill(t, "compare-device-config", map[string]fwdtest.Handler{snapsPath: twoSnaps(), diffFilesPath: boom}, cdIn+`}`); err == nil || calls != 4 {
		t.Fatalf("a 500 on a read is retried, bounded at 4 attempts, then reported: err %v calls %d", err, calls)
	}
}

// A name that is no device in a snapshot has no changed file, which is not "unchanged": the result is unknown, says which snapshot lacks it, and names
// the devices that carry the name plus an underscore suffix (a firewall host and its virtual systems).
func TestConfigDiffAnAbsentDeviceIsUnknownNotUnchanged(t *testing.T) {
	routes := map[string]fwdtest.Handler{snapsPath: twoSnaps(),
		diffFilesPath:                            fwdtest.Const(200, []any{map[string]any{"name": "fw1_vsys1", "hasConfigChange": true, "files": []any{map[string]any{"name": "fw1_vsys1,CONFIGURATION.txt"}}}}),
		"GET /api/networks/n1/devices/fw1/files": fwdtest.Const(404, map[string]any{"message": "no such device"}),
	}
	r, _ := mustRun(t, "compare-device-config", routes, cdIn+`,"device":"fw1"}`)
	b := jsonOf(r)
	if r.Status != result.Unknown || !strings.Contains(b, "not a device in the before or after snapshot") && !strings.Contains(b, "not a device in the before snapshot") || !strings.Contains(b, "fw1_vsys1") {
		t.Fatalf("an absent device must be unknown and name the suffixed devices: %s %s", r.Status, b)
	}
}

// a `[ ... ]` list longer than the 400-character cut hides what changed in it: list_changes names the entries added and removed.
func TestConfigDiffNamesTheEntriesChangedInsideALongList(t *testing.T) {
	entries := func(skip map[int]bool, extra ...string) string {
		var b strings.Builder
		for i := 0; i < 60; i++ {
			if !skip[i] {
				fmt.Fprintf(&b, "site%02d.example.invalid ", i)
			}
		}
		for _, e := range extra {
			b.WriteString(e + " ")
		}
		return "        list [ " + b.String() + "];\n"
	}
	before := "category big {\n" + entries(nil) + "}\nshort [ a b ];\n"
	after := "category big {\n" + entries(map[int]bool{7: true, 41: true}, "newsite.example.invalid") + "}\nshort [ a c ];\n"
	r, _ := mustRun(t, "compare-device-config", map[string]fwdtest.Handler{snapsPath: twoSnaps(), diffFilesPath: changedFiles(), dlBefore: download(before, after)}, cdIn+`,"device":"r1"}`)
	d := fmt.Sprint(r.Evidence[0].Detail)
	if !strings.Contains(d, "list_changes") || !strings.Contains(d, "newsite.example.invalid") || !strings.Contains(d, "site07.example.invalid") || !strings.Contains(d, "site41.example.invalid") {
		t.Fatalf("the added and the two removed entries must be named: %s", d)
	}
	if strings.Contains(d, "added_total:1") == false || strings.Contains(d, "removed_total:2") == false {
		t.Errorf("counts must be 1 added and 2 removed: %s", d)
	}
	if !strings.Contains(d, "length_before:") || !strings.Contains(d, "length_after:") {
		t.Errorf("the changed lines' full lengths must be reported: %s", d)
	}
	if strings.Count(d, "line_before") != 1 {
		t.Errorf("the short list is shown whole and must not be in list_changes: %s", d)
	}
}

// Forward's changed-file list can omit a file whose size changed (a 3.6 MB config went to 2.1 MB and was not listed): the sizes of every file are compared from the
// file lists, the files not compared line by line are named with why, and a file the caller names is compared by its text.
func TestConfigDiffReportsFileSizeChangesForeignToForwardsChangedList(t *testing.T) {
	filesBySnap := func(r *http.Request, _ []byte) (int, any) {
		if r.URL.Query().Get("snapshotId") == "s1" {
			return 200, map[string]any{"files": []any{map[string]any{"name": "CONFIGURATION.txt", "bytes": 3617331}, map[string]any{"name": "other.txt", "bytes": 100},
				map[string]any{"name": "bgp_peers.txt", "bytes": 5000}, map[string]any{"name": "same.txt", "bytes": 7}}}
		}
		return 200, map[string]any{"files": []any{map[string]any{"name": "CONFIGURATION.txt", "bytes": 2133226}, map[string]any{"name": "other.txt", "bytes": 100},
			map[string]any{"name": "same.txt", "bytes": 7}}}
	}
	routes := map[string]fwdtest.Handler{snapsPath: twoSnaps(), diffFilesPath: fwdtest.Const(200, []any{map[string]any{"name": "r1", "hasConfigChange": true,
		"files": []any{map[string]any{"name": "r1,other.txt"}}}}), "GET /api/networks/n1/devices/r1/files": filesBySnap,
		"GET /api/networks/n1/devices/r1/files/other.txt":         download("a\n", "a\nb\n"),
		"GET /api/networks/n1/devices/r1/files/CONFIGURATION.txt": download("x\ny\n", "x\n")}
	r, _ := mustRun(t, "compare-device-config", routes, cdIn+`,"device":"r1"}`)
	d := fmt.Sprint(r.Evidence[0].Detail)
	for _, want := range []string{"file_inventory", "CONFIGURATION.txt", "delta_bytes:-1484105", "bgp_peers.txt", "removed", "does not include it although its size changed"} {
		if !strings.Contains(d, want) {
			t.Errorf("the inventory must name %q: %s", want, d)
		}
	}
	if strings.Contains(d, "same.txt") {
		t.Errorf("an unchanged file must not be listed: %s", d)
	}
	if !strings.Contains(r.Finding, "file sizes: CONFIGURATION.txt -1484105 B") {
		t.Errorf("the finding must carry the byte deltas: %s", r.Finding)
	}
	// naming the file Forward did not list compares its text directly
	r, _ = mustRun(t, "compare-device-config", routes, cdIn+`,"device":"r1","file":"CONFIGURATION.txt"}`)
	d = fmt.Sprint(r.Evidence[0].Detail)
	if r.Status != result.OK || !strings.Contains(d, "2: y") || !strings.Contains(strings.Join(r.Limits, "|"), "does not include CONFIGURATION.txt; its text was compared directly") {
		t.Errorf("a named file missing from Forward's list must be compared directly: %s %v", d, r.Limits)
	}
}
