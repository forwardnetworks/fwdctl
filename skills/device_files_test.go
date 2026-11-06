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

// On a predicted snapshot, a read also says how many lines of the file the prediction changed, and from which change set and base (Forward's predictedFileInfo).
func TestDeviceFilesOnAPredictedSnapshotSaysHowManyLinesThePredictionChanged(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		snapsPath: fwdtest.Snapshots(fwdtest.Snap("p1", "PROCESSED", "PREDICT", "2026-09-05T00:00:00.000Z")),
		filePath:  fwdtest.Const(200, cfg(20)),
		"GET /api/snapshots/p1/files/r1,config.txt": fwdtest.Const(200, map[string]any{"lineCount": 20, "excerpts": []any{},
			"predictedFileInfo": map[string]any{"numModifiedLines": 3, "changeSetId": "cs9", "baseSnapshotId": "s1", "commitId": "c2"}}),
	}
	r, _ := mustRun(t, "inspect-device-files", routes, `{"network_id":"n1","device":"r1","mode":"read","file":"config.txt","snapshot_id":"p1"}`)
	if b := jsonOf(r); r.Status != result.OK || !strings.Contains(b, `"predicted_modified_lines":3`) || !strings.Contains(b, `"change_set_id":"cs9"`) {
		t.Fatalf("the read of a predicted file should carry the changed-line count: %s", b)
	}
}

// A shared key on AAA and IKE lines is a secret: tacacs/radius server keys (global, per host, in a server block), crypto isakmp key and a keyring
// pre-shared-key. A key chain name or "crypto key generate" is not.
func TestDeviceFilesRedactsAAAAndIKEKeys(t *testing.T) {
	file := []byte(strings.Join([]string{
		"tacacs-server key 7 SEC1",
		"tacacs-server host 192.0.2.10 key 7 SEC2",
		"radius-server host 192.0.2.11 auth-port 1812 key SEC3",
		"tacacs server T1",
		" address ipv4 192.0.2.12",
		" key 7 SEC4",
		"crypto isakmp key SEC5 address 192.0.2.13",
		"crypto keyring KR",
		" pre-shared-key address 192.0.2.14 key SEC6",
		"radius-server key \"SEC7 with space\"",
		" server-private 192.0.2.15 key 7 SEC8",
		"set security ike policy P pre-shared-key ascii-text \"SEC9\"",
		" pre-shared-key local 6 SECA",
		" authentication remote pre-share key SECB",
		"ntp authentication-key 1 md5 SECC 7",
		"snmp-server user U G v3 auth sha SECD priv aes 128 SECE",
		"username admin secret sha512 SECF",
		"snmp-server user V network-admin auth md5 0xSECH priv aes-128 0xSECI localizedkey",
		"   authentication key 7 SECG",
		"key chain KEEPCHAIN",
		" key 1",
		"crypto key generate rsa modulus 2048",
	}, "\n") + "\n")
	r, _ := devFiles(t, map[string]fwdtest.Handler{filePath: fwdtest.Const(200, file)},
		`{"network_id":"n1","device":"r1","mode":"read","file":"config.txt"}`)
	d := fmt.Sprint(r.Evidence[0].Detail)
	for _, secret := range []string{"SEC1", "SEC2", "SEC3", "SEC4", "SEC5", "SEC6", "SEC7", "SEC8", "SEC9", "SECA", "SECB", "SECC", "SECD", "SECE", "SECF", "SECG", "SECH", "SECI"} {
		if strings.Contains(d, secret) {
			t.Errorf("%s leaked: %s", secret, d)
		}
	}
	for _, keep := range []string{"key chain KEEPCHAIN", "key 1", "crypto key generate rsa", "address 192.0.2.13", "address ipv4 192.0.2.12", "server-private 192.0.2.15", "ntp authentication-key 1 md5", "snmp-server user U G v3"} {
		if !strings.Contains(d, keep) {
			t.Errorf("%q was redacted but is not a secret: %s", keep, d)
		}
	}
}
