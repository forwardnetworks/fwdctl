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
		map[string]any{"name": "down1", "host": "192.0.2.5", "createdBy": "alice", "createdAt": "2026-08-17T10:00:00Z", "testResult": map[string]any{"error": "PING_FAILED", "errorPhase": "CONNECTION", "discoveredType": "cisco_ios_xe_ssh", "savedAt": "2026-10-16T01:00:00Z"}},
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
	if d["tested_at"] != "2026-10-16T01:00:00Z" || d["created_by"] != "alice" || d["created_at"] != "2026-08-17T10:00:00Z" {
		t.Errorf("when it was tested and who created it: %v", d)
	}
	if u := by["untested1"]; u["connectivity_error"] != nil || strings.Contains(fmt.Sprint(u["reason"]), "reached") {
		t.Errorf("a device with no stored test says nothing it does not know: %v", u)
	}
}

// For a failed device the devices view says when it last collected successfully: the newest earlier processed snapshot (up to five are read) in which the device completed. A device that never completed in those says so.
func TestDevicesViewSaysWhenAFailedDeviceLastCollected(t *testing.T) {
	metrics := map[string]any{"numSuccessfulDevices": 9, "deviceCollectionFailures": map[string]any{"CONNECTION_REFUSED": 1, "AUTHENTICATION_FAILED": 1}}
	routes := failedRoutes(metrics, nil, nil)
	routes[snapsPath] = fwdtest.Snapshots(
		fwdtest.Snap("s1", "PROCESSED", "COLLECTION", "2026-09-03T00:00:00.000Z"),
		fwdtest.Snap("old1", "PROCESSED", "COLLECTION", "2026-09-02T00:00:00.000Z"),
		fwdtest.Snap("old2", "PROCESSED", "COLLECTION", "2026-09-01T00:00:00.000Z"))
	base := routes[nqePath]
	routes[nqePath] = func(r *http.Request, body []byte) (int, any) {
		if strings.Contains(string(body), "completed(r)") {
			ok := map[string][]string{"old1": {"edge2"}, "old2": {"edge1", "edge2"}}[r.URL.Query().Get("snapshotId")]
			items := []map[string]any{}
			for _, n := range ok {
				items = append(items, map[string]any{"device": n})
			}
			return 200, map[string]any{"items": items, "totalNumItems": len(items)}
		}
		return base(r, body)
	}
	r, _ := collect(t, routes, `{"network_id":"n1","view":"devices","failure":"network_path"}`)
	devs := r.Evidence[0].Detail["devices"].([]map[string]any)
	if len(devs) != 1 || devs[0]["device"] != "edge1" || devs[0]["last_collected_snapshot"] != "old2" {
		t.Fatalf("edge1 last collected in the older snapshot: %v", devs)
	}
	r, _ = collect(t, routes, `{"network_id":"n1","view":"devices","failure":"credentials"}`)
	devs = r.Evidence[0].Detail["devices"].([]map[string]any)
	if len(devs) != 1 || devs[0]["last_collected_snapshot"] != "old1" {
		t.Errorf("edge2 last collected in the newer earlier snapshot: %v", devs)
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "earlier processed snapshots") {
		t.Errorf("the limits say how many earlier snapshots were read: %v", r.Limits)
	}
}
