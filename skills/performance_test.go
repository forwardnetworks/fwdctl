package skills_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/result"
)

const (
	unhealthyDevPath = "GET /api/networks/n1/unhealthy-devices"
	devMetricsPath   = "GET /api/networks/n1/device-metrics"
	ifMetricsPath    = "GET /api/networks/n1/interface-metrics"
	devHistPath      = "POST /api/networks/n1/device-metrics-history"
)

func perf(t *testing.T, routes map[string]fwdtest.Handler, in string) result.Result {
	r, _ := mustRun(t, "inspect-performance", routes, in)
	return r
}

// THE POSITIVE CONTROL FOR "ABSENCE IS NOT HEALTH": an empty unhealthy list is only "healthy" when raw samples exist.
func TestUnhealthyDevicesEmptyIsUnknownWithoutSamplesAndHealthyWithThem(t *testing.T) {
	empty := fwdtest.Const(200, map[string]any{"startTime": 1, "endTime": 2, "devices": map[string]any{}})
	r := perf(t, map[string]fwdtest.Handler{unhealthyDevPath: empty, devMetricsPath: fwdtest.Const(200, map[string]any{"metrics": []any{}})}, `{"network_id":"n1","view":"unhealthy_devices"}`)
	if r.Status != result.Unknown {
		t.Fatalf("no samples must be unknown, got %s: %s", r.Status, r.Finding)
	}
	r = perf(t, map[string]fwdtest.Handler{unhealthyDevPath: empty, devMetricsPath: fwdtest.Const(200, map[string]any{"metrics": []any{map[string]any{"deviceName": "r1", "value": 4}}})}, `{"network_id":"n1","view":"unhealthy_devices"}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "No device is unhealthy") {
		t.Fatalf("samples exist and none unhealthy is a real answer: %s %s", r.Status, r.Finding)
	}
}

func TestUnhealthyDevicesListedAreAFailedFindingWithForwardsReason(t *testing.T) {
	doc := fwdtest.Const(200, map[string]any{"devices": map[string]any{"r2": map[string]any{"cpu": 97}, "r1": map[string]any{"memory": 99}}})
	r := perf(t, map[string]fwdtest.Handler{unhealthyDevPath: doc}, `{"network_id":"n1","view":"unhealthy_devices"}`)
	d := fmt.Sprint(r.Evidence[0].Detail)
	if r.Status != result.Failed || !strings.Contains(r.Finding, "2 unhealthy") || !strings.Contains(d, "cpu") {
		t.Fatalf("%s %s %s", r.Status, r.Finding, d)
	}
}

func TestDeviceAndInterfaceReadingsAreHighestFirstAndEmptyIsUnknown(t *testing.T) {
	ms := fwdtest.Const(200, map[string]any{"metrics": []any{map[string]any{"deviceName": "a", "value": 10}, map[string]any{"deviceName": "b", "value": 90.126}}})
	r := perf(t, map[string]fwdtest.Handler{devMetricsPath: ms}, `{"network_id":"n1","view":"devices","metric":"CPU"}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "highest b at 90.1") {
		t.Fatalf("%s", r.Finding)
	}
	r = perf(t, map[string]fwdtest.Handler{ifMetricsPath: fwdtest.Const(200, map[string]any{"metrics": []any{}})}, `{"network_id":"n1","view":"interfaces","metric":"PACKET_LOSS"}`)
	if r.Status != result.Unknown {
		t.Fatalf("no interface samples must be unknown, got %s", r.Status)
	}
}

func TestPerformanceMetricNamesAreForwardsExactStrings(t *testing.T) {
	r := perf(t, map[string]fwdtest.Handler{}, `{"network_id":"n1","view":"devices","metric":"UTILIZATION"}`)
	if r.Status != result.Error || !strings.Contains(r.Finding, "CPU or MEMORY") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
	r = perf(t, map[string]fwdtest.Handler{}, `{"network_id":"n1","view":"unhealthy_interfaces"}`)
	if r.Status != result.Error {
		t.Fatalf("unhealthy_interfaces needs a device: %s", r.Status)
	}
}

func TestDeviceHistorySummarisesAndThinsTheSeries(t *testing.T) {
	var pts []any
	for i := 0; i < 100; i++ {
		pts = append(pts, map[string]any{"instant": fmt.Sprintf("2026-09-30T%02d:00:00Z", i%24), "value": i})
	}
	h := fwdtest.Const(200, map[string]any{"metrics": []any{map[string]any{"deviceName": "r1", "data": pts}}})
	r := perf(t, map[string]fwdtest.Handler{devHistPath: h}, `{"network_id":"n1","view":"device_history","device":"r1","metric":"CPU"}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "min 0.0, max 99.0") || !strings.Contains(strings.Join(r.Limits, "|"), "100 samples") {
		t.Fatalf("%s %v", r.Finding, r.Limits)
	}
	if n := len(r.Evidence[0].Detail["series"].([]map[string]any)); n > 24 {
		t.Errorf("the series must be bounded, got %d points", n)
	}
	r = perf(t, map[string]fwdtest.Handler{devHistPath: fwdtest.Const(200, map[string]any{"metrics": []any{}})}, `{"network_id":"n1","view":"device_history","device":"r1","metric":"CPU"}`)
	if r.Status != result.Unknown {
		t.Errorf("no history must be unknown, got %s", r.Status)
	}
}

// A missing metric is defaulted, not refused: devices answers CPU and MEMORY together.
func TestDevicesViewWithoutAMetricAnswersCPUAndMemory(t *testing.T) {
	ms := fwdtest.Const(200, map[string]any{"metrics": []any{map[string]any{"deviceName": "a", "value": 10}, map[string]any{"deviceName": "b", "value": 90}}})
	r := perf(t, map[string]fwdtest.Handler{devMetricsPath: ms}, `{"network_id":"n1","view":"devices"}`)
	d := r.Evidence[0].Detail
	if r.Status != result.OK || d["cpu"] == nil || d["memory"] == nil {
		t.Fatalf("%s %s %v", r.Status, r.Finding, d)
	}
	r = perf(t, map[string]fwdtest.Handler{ifMetricsPath: fwdtest.Const(200, map[string]any{"metrics": []any{map[string]any{"deviceName": "a", "interfaceName": "e1", "value": 3}}})}, `{"network_id":"n1","view":"interfaces"}`)
	if r.Status != result.OK || !strings.Contains(strings.Join(r.Limits, " "), "defaulted to UTILIZATION") {
		t.Fatalf("%s %v", r.Status, r.Limits)
	}
}

// An empty answer says whether the organization has performance data switched on, instead of "cannot tell which".
func TestNoPerformanceSamplesSaysWhetherThePerformanceFeatureIsOn(t *testing.T) {
	routes := map[string]fwdtest.Handler{
		ifMetricsPath:     fwdtest.Const(200, map[string]any{"metrics": []any{}}),
		"GET /api/config": fwdtest.Const(200, map[string]any{"performance_data": false}),
	}
	r := perf(t, routes, `{"network_id":"n1","view":"interfaces","metric":"ERROR"}`)
	if r.Status != result.Unknown || !strings.Contains(strings.Join(r.Limits, " "), "performance_data feature is OFF") {
		t.Fatalf("%s %v", r.Status, r.Limits)
	}
}

func TestUnhealthyInterfacesWithoutADevicePointsToTheNetworkWideView(t *testing.T) {
	r := perf(t, map[string]fwdtest.Handler{}, `{"network_id":"n1","view":"unhealthy_interfaces"}`)
	if r.Status != result.Error || !strings.Contains(r.Finding, "view interfaces") {
		t.Fatalf("%s %s", r.Status, r.Finding)
	}
}
