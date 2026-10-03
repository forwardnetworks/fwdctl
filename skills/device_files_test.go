package skills_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

const (
	filesPath = "GET /api/networks/n1/devices/r1/files"
	filePath  = "GET /api/networks/n1/devices/r1/files/config.txt"
)

func cfg(n int) []byte {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		switch i {
		case 5:
			b.WriteString("snmp-server community s3cr3t RO\n")
		case 7:
			b.WriteString("ntp server 10.0.0.1\n")
		default:
			fmt.Fprintf(&b, "line %d\n", i)
		}
	}
	return []byte(b.String())
}

func devFiles(t *testing.T, routes map[string]fwdtest.Handler, in string) (result.Result, *fwdtest.Server) {
	routes[snapsPath] = ready("s1")
	return mustRun(t, "inspect-device-files", routes, in)
}

func TestDeviceFilesListNamesTheFilesAndTheirCommands(t *testing.T) {
	r, _ := devFiles(t, map[string]fwdtest.Handler{filesPath: fwdtest.Const(200, map[string]any{"files": []any{
		map[string]any{"name": "config.txt", "bytes": 100, "command": "show running-config"}}})},
		`{"network_id":"n1","device":"r1","mode":"list"}`)
	if r.Status != result.OK || !strings.Contains(fmt.Sprint(r.Evidence[0].Detail), "show running-config") {
		t.Fatalf("%+v", r)
	}
}

func TestDeviceFilesSearchFindsLinesWithNumbersAndRedactsSecrets(t *testing.T) {
	r, _ := devFiles(t, map[string]fwdtest.Handler{filePath: fwdtest.Const(200, cfg(20))},
		`{"network_id":"n1","device":"r1","mode":"search","file":"config.txt","pattern":"snmp-server","context":1}`)
	d := fmt.Sprint(r.Evidence[0].Detail)
	if r.Status != result.OK || !strings.Contains(d, "5: snmp-server community <redacted>") || strings.Contains(d, "s3cr3t") {
		t.Fatalf("secret leaked or line missing: %s", d)
	}
	if !strings.Contains(strings.Join(r.Limits, "|"), "replaced with <redacted>") {
		t.Errorf("limits do not say lines were redacted: %v", r.Limits)
	}
}

func TestDeviceFilesNoRedactShowsTheValue(t *testing.T) {
	r, _ := devFiles(t, map[string]fwdtest.Handler{filePath: fwdtest.Const(200, cfg(20))},
		`{"network_id":"n1","device":"r1","mode":"search","file":"config.txt","pattern":"snmp","no_redact":true}`)
	if !strings.Contains(fmt.Sprint(r.Evidence[0].Detail), "s3cr3t") {
		t.Fatal("no_redact should show the value")
	}
}

func TestDeviceFilesNoMatchIsUnknownNeverNotConfigured(t *testing.T) {
	r, _ := devFiles(t, map[string]fwdtest.Handler{filePath: fwdtest.Const(200, cfg(20))},
		`{"network_id":"n1","device":"r1","mode":"search","file":"config.txt","pattern":"telnet"}`)
	if r.Status != result.Unknown {
		t.Fatalf("status %s, want unknown", r.Status)
	}
}

func TestDeviceFilesReadPagesAndSaysWhereToContinue(t *testing.T) {
	r, _ := devFiles(t, map[string]fwdtest.Handler{filePath: fwdtest.Const(200, cfg(400))},
		`{"network_id":"n1","device":"r1","mode":"read","file":"config.txt","max_lines":100}`)
	if r.Status != result.OK || !strings.Contains(strings.Join(r.Limits, "|"), "start_line=101") {
		t.Fatalf("%+v", r.Limits)
	}
	r, _ = devFiles(t, map[string]fwdtest.Handler{filePath: fwdtest.Const(200, cfg(400))},
		`{"network_id":"n1","device":"r1","mode":"read","file":"config.txt","start_line":999}`)
	if r.Status != result.Unknown {
		t.Fatalf("past the end should be unknown, got %s", r.Status)
	}
}

func TestDeviceFilesMissingFileOrDeviceIsUnknown(t *testing.T) {
	r, _ := devFiles(t, map[string]fwdtest.Handler{filePath: fwdtest.Const(404, map[string]any{"message": "no"})},
		`{"network_id":"n1","device":"r1","mode":"read","file":"config.txt"}`)
	if r.Status != result.Unknown {
		t.Fatalf("status %s", r.Status)
	}
	r, _ = devFiles(t, map[string]fwdtest.Handler{filesPath: fwdtest.Const(404, map[string]any{"message": "no"})},
		`{"network_id":"n1","device":"r1","mode":"list"}`)
	if r.Status != result.Unknown {
		t.Fatalf("status %s", r.Status)
	}
}

func TestDeviceFilesHugeFileIsCappedAndSaidSo(t *testing.T) {
	big := []byte(strings.Repeat("x\n", 3<<20)) // 6 MiB
	r, _ := devFiles(t, map[string]fwdtest.Handler{filePath: fwdtest.Const(200, big)},
		`{"network_id":"n1","device":"r1","mode":"search","file":"config.txt","pattern":"x","max_matches":1}`)
	if !strings.Contains(strings.Join(r.Limits, "|"), "only its first 4 MiB") {
		t.Fatalf("%v", r.Limits)
	}
}

func TestDeviceFilesBadPatternIsAnError(t *testing.T) {
	r, _ := devFiles(t, map[string]fwdtest.Handler{}, `{"network_id":"n1","device":"r1","mode":"search","file":"config.txt","pattern":"("}`)
	if r.Status != result.Error {
		t.Fatalf("status %s", r.Status)
	}
}

func TestDeviceFilesRedactsSNMPCommunityButNotBGPCommunity(t *testing.T) {
	file := []byte("community public {\n    authorization read-only;\n}\nthen community add ATL-EXPORT;\nsnmp-server community s3cr3t RO\n")
	r, _ := devFiles(t, map[string]fwdtest.Handler{filePath: fwdtest.Const(200, file)},
		`{"network_id":"n1","device":"r1","mode":"read","file":"config.txt"}`)
	d := fmt.Sprint(r.Evidence[0].Detail)
	for _, secret := range []string{"community public", "s3cr3t"} {
		if strings.Contains(d, secret) {
			t.Errorf("%q leaked: %s", secret, d)
		}
	}
	if !strings.Contains(d, "community add ATL-EXPORT") {
		t.Errorf("routing community was redacted: %s", d)
	}
}

func TestDeviceFilesOfACloudAccountSaysTheCollectedJSONIsNotReachable(t *testing.T) {
	routes := map[string]fwdtest.Handler{snapsPath: ready("s1"),
		"GET /api/networks/n1/devices/gcp-prod/files": fwdtest.Const(200, map[string]any{"files": []any{map[string]any{"name": "snapshot_time.txt", "bytes": 29}}})}
	r, _ := mustRun(t, "inspect-device-files", routes, `{"network_id":"n1","device":"gcp-prod","mode":"list"}`)
	if r.Status != result.OK || !strings.Contains(strings.Join(r.Limits, " "), "only snapshot_time.txt") || !strings.Contains(strings.Join(r.Limits, " "), "cloud_instances") {
		t.Fatalf("%s %v", r.Status, r.Limits)
	}
}

func TestDeviceFilesSearchWithoutAFileSearchesEveryFileAndSaysWhichHadMatches(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		filesPath: fwdtest.Const(200, map[string]any{"files": []any{
			map[string]any{"name": "config.txt", "bytes": 100}, map[string]any{"name": "vxlan.txt", "bytes": 50}, map[string]any{"name": "other.txt", "bytes": 10}}}),
		filePath: fwdtest.Const(200, []byte("hostname r1\nvxlan vlan 10 vni 5010\nvxlan vlan 20 vni 5020\n")),
		"GET /api/networks/n1/devices/r1/files/vxlan.txt": fwdtest.Const(200, []byte("VTEP 10.0.0.2 vxlan vlan 10\n")),
		"GET /api/networks/n1/devices/r1/files/other.txt": fwdtest.Const(200, []byte("nothing here\n")),
	}
	r, _ := devFiles(t, routes, `{"network_id":"n1","device":"r1","mode":"search","pattern":"vxlan vlan"}`)
	d := fmt.Sprint(r.Evidence[0].Detail)
	if r.Status != result.OK || !strings.Contains(r.Finding, "3 line(s) in 2 of 3 file(s)") || !strings.Contains(d, "file:vxlan.txt matches:1") || strings.Contains(d, "other.txt") {
		t.Fatalf("%s %s", r.Finding, d)
	}
	if r, _ := devFiles(t, routes, `{"network_id":"n1","device":"r1","mode":"search","pattern":"zzz-none"}`); r.Status != result.Unknown {
		t.Errorf("no match anywhere is unknown, not 'absent': %s", r.Status)
	}
	if _, _, err := runSkill(t, "inspect-device-files", routes, `{"network_id":"n1","device":"r1","mode":"read"}`); err != nil {
		t.Errorf("read without file is an error result, not a crash: %v", err)
	}
}
