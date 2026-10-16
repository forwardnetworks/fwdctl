package skills_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
	"github.com/forwardnetworks/fwdctl/skills"
)

// classic_device action test queues Forward's connectivity test of a small NAMED set of classic devices: each is probed from its collector. The dry run lists the devices with their collectors and says it probes each host;
// apply needs confirm equal to the number of devices; a name that is not a classic device refuses the whole call; there is no whole-network form.
func deviceTestRoutes(posted *string, fresh *bool) map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/classic-devices": func(*http.Request, []byte) (int, any) {
			end := "2020-01-01T00:00:00Z"
			if *fresh {
				end = time.Now().Add(time.Second).UTC().Format(time.RFC3339)
			}
			return 200, []any{
				map[string]any{"name": "sw1", "host": "192.0.2.1", "type": "cisco_ios_xe_ssh", "collectorId": "C7", "testResult": map[string]any{"error": "CUSTOM_COMMAND_AUTHORIZATION_FAILED", "errorPhase": "AUTHORIZATION", "endTime": end, "savedAt": end}},
				map[string]any{"name": "sw2", "host": "192.0.2.2", "type": "cisco_ios_xe_ssh", "testResult": map[string]any{"endTime": end, "savedAt": end, "discoveredType": "cisco_ios_xe_ssh"}},
				map[string]any{"name": "sw3", "host": "192.0.2.3"},
			}
		},
		"POST /api/networks/n1/connectivityTests/bulkStart": func(_ *http.Request, b []byte) (int, any) {
			*posted = string(b)
			*fresh = true
			return 204, nil
		},
	}
}

func TestClassicDeviceTestDryRunNamesWhatItProbesAndApplyNeedsTheCount(t *testing.T) {
	var posted string
	fresh := false
	in := `{"network_id":"n1","object":"classic_device","action":"test","definition":{"names":["sw1","sw2"]}`
	r, srv := mustRun(t, "edit-source", deviceTestRoutes(&posted, &fresh), in+`}`)
	b := jsonOf(r)
	if r.Mode != result.ModeDryRun || writes(srv) != 0 || posted != "" {
		t.Fatalf("dry run wrote: %s %s", r.Mode, b)
	}
	for _, want := range []string{"sw1", "sw2", "192.0.2.1", "C7", "default collector", "probes", "100"} {
		if !strings.Contains(b, want) {
			t.Errorf("dry run should show %q: %s", want, b)
		}
	}
	if _, _, err := runSkill(t, "edit-source", deviceTestRoutes(&posted, &fresh), in+`,"apply":true}`); err == nil || posted != "" {
		t.Errorf("apply without confirm must be refused: %v %q", err, posted)
	}
	r, _ = mustRun(t, "edit-source", deviceTestRoutes(&posted, &fresh), in+`,"apply":true,"confirm":"2"}`)
	if r.Status != result.OK || !strings.Contains(posted, `"sw1"`) || !strings.Contains(posted, `"sw2"`) {
		t.Errorf("apply: %s %s posted %q", r.Status, r.Finding, posted)
	}
}

func TestClassicDeviceTestRefusesUnknownNamesAndTooManyAndWaitsForFreshResults(t *testing.T) {
	defer skills.SetupTestPollHook(5 * time.Millisecond)()
	var posted string
	fresh := false
	for name, def := range map[string]string{
		"unknown":    `{"names":["sw1","nope"]}`,
		"none":       `{"names":[]}`,
		"whole net":  `{}`,
		"too many":   `{"names":[` + tooMany() + `]}`,
		"bad fields": `{"names":["sw1"],"all":true}`,
	} {
		if _, _, err := runSkill(t, "edit-source", deviceTestRoutes(&posted, &fresh), `{"network_id":"n1","object":"classic_device","action":"test","definition":`+def+`}`); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
	if posted != "" {
		t.Errorf("a refused call queued tests: %q", posted)
	}
	r, _ := mustRun(t, "edit-source", deviceTestRoutes(&posted, &fresh), `{"network_id":"n1","object":"classic_device","action":"test","apply":true,"confirm":"2","definition":{"names":["sw1","sw2"],"wait_seconds":20}}`)
	if r.Status != result.OK || !strings.Contains(jsonOf(r), "newer_than_queue_time") {
		t.Errorf("wait: %s %s", r.Status, jsonOf(r))
	}
}

func tooMany() string {
	var l []string
	for i := 0; i < 101; i++ {
		l = append(l, `"d`+strings.Repeat("x", i%7)+string(rune('a'+i%26))+string(rune('a'+i/26))+`"`)
	}
	return strings.Join(l, ",")
}
