package skills_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

// A device Forward recognised and refuses (UNSUPPORTED_DEVICE_TYPE) fails collection every run. The devices view says why, what it most likely is by platform (from Forward's explicit unsupported-device detectors),
// what to do, the type its connectivity test discovered, and groups the finding by platform.
func TestDevicesViewExplainsUnsupportedDeviceTypes(t *testing.T) {
	metrics := map[string]any{"numSuccessfulDevices": 9, "deviceCollectionFailures": map[string]any{"UNSUPPORTED_DEVICE_TYPE": 3}}
	routes := failedRoutes(metrics, nil, nil)
	base := routes[nqePath]
	routes[nqePath] = func(r *http.Request, body []byte) (int, any) {
		if strings.Contains(string(body), "collectionFailed(e)") {
			row := func(n string) map[string]any {
				return map[string]any{"device": n, "stage": "collection", "reason": "DeviceCollectionError.UNSUPPORTED_DEVICE_TYPE", "vendor": "CISCO", "os": "NXOS"}
			}
			return 200, map[string]any{"items": []map[string]any{row("sw1"), row("sw2"), row("sw3")}, "totalNumItems": 3}
		}
		return base(r, body)
	}
	routes["GET /api/networks/n1/classic-devices"] = fwdtest.Const(200, []any{
		map[string]any{"name": "sw1", "testResult": map[string]any{"discoveredType": "cisco_nxos_ssh"}},
		map[string]any{"name": "sw2", "type": "cisco_nxos_ssh"},
	})
	r, _ := collect(t, routes, `{"network_id":"n1","view":"devices","failure":"UNSUPPORTED_DEVICE_TYPE"}`)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "Cisco NXOS") && !strings.Contains(r.Finding, "CISCO NXOS") {
		t.Fatalf("the finding must group by platform: %s %s", r.Status, r.Finding)
	}
	d := r.Evidence[0].Detail
	devs := d["devices"].([]map[string]any)
	if len(devs) != 3 || !strings.Contains(devs[0]["reason"].(string), "cannot collect") || !strings.Contains(devs[0]["likely"].(string), "MDS") || devs[0]["discovered_type"] != "cisco_nxos_ssh" {
		t.Errorf("rows: %v", devs)
	}
	if devs[1]["discovered_type"] != nil {
		t.Errorf("a configured type is not a discovered one: %v", devs[1])
	}
	all := strings.Join(r.Limits, " ") + r.Finding
	for _, want := range []string{"remove", "support", "not confirmed"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q: %v", want, r.Limits)
		}
	}
	if by, ok := d["by_platform"].(map[string]int); !ok || by["CISCO NXOS"] != 3 {
		t.Errorf("by_platform: %v", d["by_platform"])
	}
}

// A device that failed as INCOMPLETE_SETUP ("the device type and/or credentials are unknown") often just could not be reached: the stored connectivity test says so, and the type never resolves. The devices view
// shows each failed device's last connectivity-test error and phase and, for INCOMPLETE_SETUP, says which it is.
func TestDevicesViewExplainsIncompleteSetupFromTheConnectivityTest(t *testing.T) {
	metrics := map[string]any{"numSuccessfulDevices": 9, "deviceCollectionFailures": map[string]any{"INCOMPLETE_SETUP": 2}}
	routes := failedRoutes(metrics, nil, nil)
	base := routes[nqePath]
	routes[nqePath] = func(r *http.Request, body []byte) (int, any) {
		if strings.Contains(string(body), "collectionFailed(e)") {
			row := func(n string) map[string]any {
				return map[string]any{"device": n, "stage": "collection", "reason": "DeviceCollectionError.INCOMPLETE_SETUP"}
			}
			return 200, map[string]any{"items": []map[string]any{row("down1"), row("untested1")}, "totalNumItems": 2}
		}
		return base(r, body)
	}
	routes["GET /api/networks/n1/classic-devices"] = fwdtest.Const(200, []any{
		map[string]any{"name": "down1", "host": "192.0.2.5", "testResult": map[string]any{"error": "PING_FAILED", "errorPhase": "CONNECTION", "discoveredType": "cisco_ios_xe_ssh"}},
		map[string]any{"name": "untested1", "host": "192.0.2.6"},
	})
	r, _ := collect(t, routes, `{"network_id":"n1","view":"devices","failure":"INCOMPLETE_SETUP"}`)
	devs := r.Evidence[0].Detail["devices"].([]map[string]any)
	by := map[string]map[string]any{}
	for _, d := range devs {
		by[d["device"].(string)] = d
	}
	d := by["down1"]
	if d["connectivity_error"] != "PING_FAILED" || d["error_phase"] != "CONNECTION" || !strings.Contains(fmt.Sprint(d["reason"]), "could not be reached") {
		t.Errorf("an unreachable device: %v", d)
	}
	if u := by["untested1"]; u["connectivity_error"] != nil || strings.Contains(fmt.Sprint(u["reason"]), "reached") {
		t.Errorf("a device with no stored test says nothing it does not know: %v", u)
	}
}
