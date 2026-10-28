package skills_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
)

// performanceRoutes is a network whose own polling is on or off, that holds rows for the devices in have, and that counts the uploads it receives.
func performanceRoutes(enabled *atomic.Bool, have []string, uploads *atomic.Int32, uploaded *[]byte) map[string]fwdtest.Handler {
	return map[string]fwdtest.Handler{
		"GET /api/networks/n1/performance/settings": func(*http.Request, []byte) (int, any) {
			return 200, map[string]any{"enabled": enabled.Load(), "intervalMinutes": 5}
		},
		"PATCH /api/networks/n1/performance/settings": func(_ *http.Request, body []byte) (int, any) {
			if strings.Contains(string(body), `"enabled":false`) {
				enabled.Store(false)
			}
			return 200, map[string]any{"enabled": enabled.Load(), "intervalMinutes": 5}
		},
		"GET /api/networks/n1/performance/devices-with-data": func(*http.Request, []byte) (int, any) { return 200, have },
		"POST /api/networks/n1/performance": func(r *http.Request, body []byte) (int, any) {
			uploads.Add(1)
			*uploaded = body
			if u, p, ok := r.BasicAuth(); !ok || u != "collector-ab12cd34" || p != "s3cret" {
				return 401, nil
			}
			return 204, nil
		},
	}
}

const perfSample = `"devices":[{"name":"edge-1","vendor_os":"CISCO_IOS_XE","time":"2026-10-28T10:00:00Z","cpu":0.2,"memory":0.4,"uptime_seconds":1000,"interfaces":{"Gi1":{"in_octets":100,"out_octets":200,"speed_mbps":1000,"interface_type":6}}},` +
	`{"name":"owned-1","vendor_os":"CISCO_IOS_XE","time":"2026-10-28T10:00:00Z","cpu":0.1,"memory":0.1},{"name":"other-1","vendor_os":"CISCO_IOS_XE","time":"2026-10-28T10:00:00Z","cpu":0.1,"memory":0.1}]`

func TestEditPlatformPerformanceUploadIsRefusedWhileForwardsOwnPollingIsOn(t *testing.T) {
	var on atomic.Bool
	on.Store(true)
	var n atomic.Int32
	var body []byte
	_, _, err := runSkill(t, "edit-platform", performanceRoutes(&on, nil, &n, &body), `{"area":"performance","action":"upload","definition":{"network_id":"n1",`+perfSample+`}}`)
	if err == nil || !strings.Contains(err.Error(), "polling is still on") || n.Load() != 0 {
		t.Fatalf("err %v, uploads %d", err, n.Load())
	}
}

func TestEditPlatformPerformanceUploadHoldsBackUnclaimedDevicesAndSendsTheRestOnceWithConfirm(t *testing.T) {
	var on atomic.Bool
	var n atomic.Int32
	var body []byte
	routes := performanceRoutes(&on, []string{"owned-1", "other-1", "edge-1"}, &n, &body)
	def := `{"area":"performance","action":"upload","definition":{"network_id":"n1","claimed":["owned-1"],"newest":{"owned-1":"2026-10-28T09:00:00Z"},` + perfSample + `}`
	// dry run: nothing is sent; the unclaimed devices are named as refused
	r, _ := mustRun(t, "edit-platform", routes, def+`}`)
	ch := r.Changes[0]
	after, _ := ch.After.(map[string]any)
	if n.Load() != 0 || ch.Applied || after["devices_sent"] != 1 || after["refused"] != 2 {
		t.Fatalf("dry run: uploads %d applied %v after %v", n.Load(), ch.Applied, after)
	}
	// apply without confirm does not send
	key := filepath.Join(t.TempDir(), "k")
	if err := os.WriteFile(key, []byte("TOKEN=collector-ab12cd34:s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runSkill(t, "edit-platform", routes, def+`,"apply":true,"secret_file":"`+key+`"}`); err == nil || n.Load() != 0 {
		t.Fatalf("an apply without confirm must be refused and send nothing: err %v uploads %d", err, n.Load())
	}
	// apply with confirm sends one upload, as the collector, and never echoes the key
	r, _ = mustRun(t, "edit-platform", routes, def+`,"apply":true,"confirm":"upload","secret_file":"`+key+`"}`)
	if n.Load() != 1 || len(body) == 0 || !r.Changes[0].Applied {
		t.Fatalf("uploads %d body %d applied %v", n.Load(), len(body), r.Changes[0].Applied)
	}
	if strings.Contains(jsonOf(r), "s3cret") {
		t.Fatalf("the collector key is in the result")
	}
}

func TestEditPlatformPerformanceDisableTurnsPollingOffAndReadsItBack(t *testing.T) {
	var on atomic.Bool
	on.Store(true)
	var n atomic.Int32
	var body []byte
	routes := performanceRoutes(&on, nil, &n, &body)
	r, _ := mustRun(t, "edit-platform", routes, `{"area":"performance","action":"disable","definition":{"network_id":"n1"}}`)
	if !on.Load() || r.Changes[0].Applied {
		t.Fatalf("a dry run changed the setting")
	}
	r, _ = mustRun(t, "edit-platform", routes, `{"area":"performance","action":"disable","apply":true,"definition":{"network_id":"n1"}}`)
	if on.Load() || !r.Changes[0].Applied {
		t.Fatalf("polling still on: %v applied %v", on.Load(), r.Changes[0].Applied)
	}
}
