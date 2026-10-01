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

func TestRefusedRequestIsRetriedButAServerErrorIsNot(t *testing.T) {
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
	if _, _, err := runSkill(t, "compare-device-config", map[string]fwdtest.Handler{snapsPath: twoSnaps(), diffFilesPath: boom}, cdIn+`}`); err == nil || calls != 1 {
		t.Fatalf("a 500 must not be retried: err %v calls %d", err, calls)
	}
}
