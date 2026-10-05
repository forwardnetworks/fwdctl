package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const performanceName = "inspect-performance"

func init() { Register(performanceName, inspectPerformance) }

type performanceInput struct {
	NetworkID string `json:"network_id"`
	View      string `json:"view"`
	Metric    string `json:"metric"`
	Direction string `json:"direction"`
	Days      int    `json:"days"`
	Device    string `json:"device"`
	Interface string `json:"interface"`
	Limit     int    `json:"limit"`
}

const (
	defaultPerfRows  = 15
	maxPerfRows      = 100
	maxHistoryPoints = 24
)

// The metric names are Forward's own exact strings.
var (
	deviceMetrics    = map[string]bool{"CPU": true, "MEMORY": true}
	interfaceMetrics = map[string]bool{"UTILIZATION": true, "PACKET_LOSS": true, "ERROR": true}
)

// inspectPerformance reads device and interface health. The rule that shapes it is "absence is not health": Forward answers an
// empty list both when everything is fine and when performance was never collected, so an empty answer is checked against the
// raw samples, and reported as unknown when there are none.
func inspectPerformance(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in performanceInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if in.Days <= 0 {
		in.Days = 1
	}
	in.Days = min(in.Days, 30)
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	// a missing metric is defaulted and said so, not refused: devices answers both CPU and MEMORY
	defaulted := ""
	if in.Metric == "" {
		switch in.View {
		case "devices":
			return deviceBoth(ctx, s, in, cx)
		case "device_history":
			in.Metric, defaulted = "CPU", "metric defaulted to CPU; give MEMORY for memory"
		case "interfaces", "interface_history":
			in.Metric, defaulted = "UTILIZATION", "metric defaulted to UTILIZATION; give PACKET_LOSS or ERROR for those"
		}
	}
	r, err := performanceView(ctx, s, in, cx)
	if defaulted != "" && err == nil {
		r.Limits = append(r.Limits, defaulted)
	}
	return r, err
}

func performanceView(ctx context.Context, s *fwd.Session, in performanceInput, cx result.Context) (result.Result, error) {
	q := forward.MetricQuery{Days: in.Days, Device: in.Device, Interface: in.Interface, Type: in.Metric, Direction: in.Direction}
	switch in.View {
	case "unhealthy_devices":
		return unhealthyDevices(ctx, s, in, q, cx)
	case "unhealthy_interfaces":
		return unhealthyInterfaces(ctx, s, in, q, cx)
	case "devices":
		return deviceNow(ctx, s, in, q, cx)
	case "interfaces":
		return interfaceNow(ctx, s, in, q, cx)
	case "device_history":
		return deviceHistory(ctx, s, in, q, cx)
	case "interface_history":
		return interfaceHistory(ctx, s, in, q, cx)
	}
	return result.NewError(performanceName, "unknown view "+in.View, cx), nil
}

func perfBadMetric(in performanceInput, cx result.Context, iface bool) (result.Result, bool) {
	set, names := deviceMetrics, "CPU or MEMORY"
	if iface {
		set, names = interfaceMetrics, "UTILIZATION, PACKET_LOSS or ERROR"
	}
	if !set[in.Metric] {
		return result.NewError(performanceName, fmt.Sprintf("metric %q is not valid here; use %s", in.Metric, names), cx), true
	}
	return result.Result{}, false
}

// noPerformanceData says no samples came back and, as far as the API shows, why: whether the organization's performance_data feature
// is on and what SNMP collection reports for the network's sources. It never says "not configured" from a failed read.
func noPerformanceData(ctx context.Context, s *fwd.Session, networkID, what string, cx result.Context, days int) (result.Result, error) {
	limits := []string{fmt.Sprintf("no samples in the last %d day(s); an empty answer is not health", days)}
	limits = append(limits, performanceCollectionLimits(ctx, s, networkID)...)
	return result.NewUnknown(performanceName, "No performance samples were returned for "+what, cx, limits,
		result.Options{NextActions: []string{"inspect-environment", "inspect-collection", "inspect-inventory"}})
}

// performanceCollectionLimits reads the two things that decide whether performance data can exist: the organization's performance_data
// property and the SNMP collection status of the network's sources.
func performanceCollectionLimits(ctx context.Context, s *fwd.Session, networkID string) []string {
	var out []string
	if props, err := s.EffectiveOrgConfig(ctx); err != nil {
		out = append(out, "whether the performance_data feature is on could not be read ("+err.Error()+")")
	} else if v, ok := props["performance_data"]; !ok {
		out = append(out, "this Forward build does not list a performance_data feature")
	} else if strings.EqualFold(v, "false") {
		out = append(out, "the organization's performance_data feature is OFF, so no performance data is shown (inspect-environment shows where it is set)")
	} else {
		out = append(out, "the organization's performance_data feature is on ("+v+")")
	}
	st, err := s.SNMPCollection(ctx, networkID)
	switch {
	case err != nil:
		out = append(out, "SNMP collection state of the sources could not be read ("+err.Error()+")")
	case st.Sources == 0:
		out = append(out, "no classic source is configured for the network, so no SNMP polling is set up (inspect-collection view config lists the sources)")
	case st.Enabled == 0:
		out = append(out, fmt.Sprintf("SNMP collection is enabled on none of the %d source(s), so no performance data can exist", st.Sources))
	default:
		line := fmt.Sprintf("SNMP collection is enabled on %d of %d source(s), %d with a credential; the last collection succeeded on %d and failed on %d, and %d reported nothing", st.Enabled, st.Sources, st.EnabledWithCredential, st.Succeeded, sum(st.Errors), st.Enabled-st.Reported)
		if len(st.Errors) > 0 {
			line += " (errors: " + typesText(st.Errors) + ")"
		}
		out = append(out, line)
	}
	return out
}

func deviceNow(ctx context.Context, s *fwd.Session, in performanceInput, q forward.MetricQuery, cx result.Context) (result.Result, error) {
	if r, bad := perfBadMetric(in, cx, false); bad {
		return r, nil
	}
	ms, err := s.DeviceMetrics(ctx, in.NetworkID, q)
	if err != nil {
		return result.Result{}, err
	}
	if in.Device != "" {
		ms = filterDevices(ms, in.Device)
	}
	if len(ms) == 0 {
		return noPerformanceData(ctx, s, in.NetworkID, "device "+in.Metric, cx, in.Days)
	}
	sort.SliceStable(ms, func(i, j int) bool { return ms[i].Value > ms[j].Value })
	win, omitted, _ := window(ms, in.Limit, 0, defaultPerfRows, maxPerfRows, "devices")
	rows := make([]map[string]any, 0, len(win))
	for _, m := range win {
		rows = append(rows, map[string]any{"device": m.DeviceName, "value": round2(m.Value)})
	}
	finding := fmt.Sprintf("%s on %d device(s); highest %s at %.1f", in.Metric, len(ms), ms[0].DeviceName, ms[0].Value)
	detail := map[string]any{"metric": in.Metric, "days": in.Days, "devices": len(ms), "highest_first": rows}
	return perfOK(finding, detail, nil, omitted, cx), nil
}

func interfaceNow(ctx context.Context, s *fwd.Session, in performanceInput, q forward.MetricQuery, cx result.Context) (result.Result, error) {
	if r, bad := perfBadMetric(in, cx, true); bad {
		return r, nil
	}
	if in.Direction == "" {
		q.Direction = "INGRESS"
	}
	ms, err := s.InterfaceMetrics(ctx, in.NetworkID, q)
	if err != nil {
		return result.Result{}, err
	}
	if in.Device != "" {
		kept := ms[:0:0]
		for _, m := range ms {
			if m.DeviceName == in.Device {
				kept = append(kept, m)
			}
		}
		ms = kept
	}
	if len(ms) == 0 {
		return noPerformanceData(ctx, s, in.NetworkID, "interface "+in.Metric, cx, in.Days)
	}
	sort.SliceStable(ms, func(i, j int) bool { return ms[i].Value > ms[j].Value })
	win, omitted, _ := window(ms, in.Limit, 0, defaultPerfRows, maxPerfRows, "interfaces")
	rows := make([]map[string]any, 0, len(win))
	for _, m := range win {
		rows = append(rows, map[string]any{"device": m.DeviceName, "interface": m.InterfaceName, "direction": m.Direction, "value": round2(m.Value)})
	}
	finding := fmt.Sprintf("%s on %d interface(s); highest %s %s at %.1f", in.Metric, len(ms), ms[0].DeviceName, ms[0].InterfaceName, ms[0].Value)
	detail := map[string]any{"metric": in.Metric, "direction": q.Direction, "days": in.Days, "interfaces": len(ms), "highest_first": rows}
	return perfOK(finding, detail, nil, omitted, cx), nil
}

// unhealthyDevices reports Forward's own unhealthy-device verdict, but only trusts an EMPTY one when raw samples exist.
func unhealthyDevices(ctx context.Context, s *fwd.Session, in performanceInput, q forward.MetricQuery, cx result.Context) (result.Result, error) {
	doc, err := s.UnhealthyDevices(ctx, in.NetworkID, q)
	if err != nil {
		return result.Result{}, err
	}
	items := unhealthyItems(doc, "devices")
	if len(items) == 0 {
		samples, serr := s.DeviceMetrics(ctx, in.NetworkID, forward.MetricQuery{Type: "CPU", Days: in.Days})
		if serr != nil {
			return result.Result{}, serr
		}
		if len(samples) == 0 {
			return noPerformanceData(ctx, s, in.NetworkID, "devices", cx, in.Days)
		}
		return perfOK("No device is unhealthy (checked against "+fmt.Sprint(len(samples))+" device(s) with samples)",
			map[string]any{"unhealthy": 0, "devices_with_samples": len(samples), "days": in.Days}, nil, nil, cx), nil
	}
	win, omitted, _ := window(items, in.Limit, 0, defaultPerfRows, maxPerfRows, "unhealthy devices")
	return resultOf(fmt.Sprintf("%d unhealthy device(s)", len(items)), map[string]any{"unhealthy": len(items), "days": in.Days, "devices": win}, nil, omitted, cx, result.Failed), nil
}

func unhealthyInterfaces(ctx context.Context, s *fwd.Session, in performanceInput, q forward.MetricQuery, cx result.Context) (result.Result, error) {
	if in.Device == "" {
		return result.NewError(performanceName, "unhealthy_interfaces needs device: Forward answers it per device and has no network-wide form. For the worst interfaces across the network use view interfaces with metric PACKET_LOSS, ERROR or UTILIZATION; for the devices to ask about use unhealthy_devices", cx), nil
	}
	doc, err := s.UnhealthyInterfaces(ctx, in.NetworkID, q, []string{in.Device})
	if err != nil {
		return result.Result{}, err
	}
	items := unhealthyItems(doc, "interfaces")
	if len(items) == 0 {
		return noPerformanceData(ctx, s, in.NetworkID, "interfaces of "+in.Device+" (Forward reported none unhealthy, which is also what it says when nothing was collected)", cx, in.Days)
	}
	win, omitted, _ := window(items, in.Limit, 0, defaultPerfRows, maxPerfRows, "unhealthy interfaces")
	return resultOf(fmt.Sprintf("%d unhealthy interface(s) on %s", len(items), in.Device), map[string]any{"device": in.Device, "unhealthy": len(items), "interfaces": win}, nil, omitted, cx, result.Failed), nil
}

func deviceHistory(ctx context.Context, s *fwd.Session, in performanceInput, q forward.MetricQuery, cx result.Context) (result.Result, error) {
	if r, bad := perfBadMetric(in, cx, false); bad {
		return r, nil
	}
	if in.Device == "" {
		return result.NewError(performanceName, "device_history needs device", cx), nil
	}
	hs, err := s.DeviceHistory(ctx, in.NetworkID, q, []string{in.Device})
	if err != nil {
		return result.Result{}, err
	}
	var pts []forward.MetricDataPoint
	for _, h := range hs {
		pts = append(pts, h.Data...)
	}
	return historyResult(ctx, s, pts, in, "device "+in.Device, cx)
}

func interfaceHistory(ctx context.Context, s *fwd.Session, in performanceInput, q forward.MetricQuery, cx result.Context) (result.Result, error) {
	if r, bad := perfBadMetric(in, cx, true); bad {
		return r, nil
	}
	if in.Device == "" || in.Interface == "" {
		return result.NewError(performanceName, "interface_history needs device and interface", cx), nil
	}
	dir := in.Direction
	if dir == "" {
		dir = "INGRESS"
	}
	hs, err := s.InterfaceHistory(ctx, in.NetworkID, q, []forward.InterfaceWithDirection{{DeviceName: in.Device, InterfaceName: in.Interface, Direction: dir}})
	if err != nil {
		return result.Result{}, err
	}
	var pts []forward.MetricDataPoint
	for _, h := range hs {
		pts = append(pts, h.Data...)
	}
	return historyResult(ctx, s, pts, in, in.Device+" "+in.Interface+" "+dir, cx)
}

func historyResult(ctx context.Context, s *fwd.Session, pts []forward.MetricDataPoint, in performanceInput, what string, cx result.Context) (result.Result, error) {
	if len(pts) == 0 {
		return noPerformanceData(ctx, s, in.NetworkID, what, cx, in.Days)
	}
	lo, hi, sum := pts[0].Value, pts[0].Value, 0.0
	for _, p := range pts {
		lo, hi, sum = minF(lo, p.Value), maxF(hi, p.Value), sum+p.Value
	}
	step := 1
	if len(pts) > maxHistoryPoints {
		step = (len(pts) + maxHistoryPoints - 1) / maxHistoryPoints
	}
	var series []map[string]any
	for i := 0; i < len(pts); i += step {
		series = append(series, map[string]any{"at": strings.Trim(string(pts[i].Instant), `"`), "value": round2(pts[i].Value)})
	}
	var omitted []result.Omission
	if step > 1 {
		omitted = append(omitted, result.Omission{What: "samples", Total: len(pts), Shown: len(series), Next: fmt.Sprintf("every %dth is shown", step)})
	}
	finding := fmt.Sprintf("%s %s over %d day(s): min %.1f, max %.1f, mean %.1f, latest %.1f", what, in.Metric, in.Days, lo, hi, sum/float64(len(pts)), pts[len(pts)-1].Value)
	detail := map[string]any{"metric": in.Metric, "days": in.Days, "samples": len(pts), "min": round2(lo), "max": round2(hi), "mean": round2(sum / float64(len(pts))), "series": series}
	return perfOK(finding, detail, nil, omitted, cx), nil
}

func perfOK(finding string, detail map[string]any, limits []string, omitted []result.Omission, cx result.Context) result.Result {
	return resultOf(finding, detail, limits, omitted, cx, result.OK)
}

func resultOf(finding string, detail map[string]any, limits []string, omitted []result.Omission, cx result.Context, status result.Status) result.Result {
	return result.MustBuild(performanceName, status, finding, result.Deterministic, cx, result.Options{Limits: limits, Omitted: omitted,
		NextActions: []string{"inspect-inventory", "investigate-reachability"},
		Evidence:    []result.Evidence{result.NewEvidence(result.EvState, "performance", nil, detail, finding)}})
}

// unhealthyItems flattens Forward's unhealthy document: an object keyed by name under key, each value its reason.
func unhealthyItems(doc []byte, key string) []map[string]any {
	var d map[string]json.RawMessage
	if json.Unmarshal(doc, &d) != nil {
		return nil
	}
	var byName map[string]json.RawMessage
	if json.Unmarshal(d[key], &byName) != nil {
		var list []json.RawMessage
		if json.Unmarshal(d[key], &list) != nil {
			return nil
		}
		out := make([]map[string]any, 0, len(list))
		for _, r := range list {
			out = append(out, map[string]any{"item": clipRaw(r)})
		}
		return out
	}
	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]map[string]any, 0, len(names))
	for _, n := range names {
		out = append(out, map[string]any{"name": n, "why": clipRaw(byName[n])})
	}
	return out
}

func clipRaw(r json.RawMessage) string { return oneLineNote(string(r)) }

func filterDevices(ms []forward.DeviceMetric, device string) []forward.DeviceMetric {
	kept := ms[:0:0]
	for _, m := range ms {
		if m.DeviceName == device {
			kept = append(kept, m)
		}
	}
	return kept
}

func round2(f float64) float64 { return float64(int64(f*100+0.5)) / 100 }

func minF(a, b float64) float64 {
	if b < a {
		return b
	}
	return a
}

func maxF(a, b float64) float64 {
	if b > a {
		return b
	}
	return a
}

// deviceBoth answers view devices without a metric: the busiest devices by CPU and by memory, in one result.
func deviceBoth(ctx context.Context, s *fwd.Session, in performanceInput, cx result.Context) (result.Result, error) {
	detail := map[string]any{"days": in.Days}
	var limits []string
	var parts []string
	for _, m := range []string{"CPU", "MEMORY"} {
		in.Metric = m
		r, err := deviceNow(ctx, s, in, forward.MetricQuery{Days: in.Days, Device: in.Device, Type: m}, cx)
		if err != nil {
			return result.Result{}, err
		}
		if r.Status != result.OK {
			limits = append(limits, m+": "+r.Finding)
			limits = append(limits, r.Limits...)
			continue
		}
		parts = append(parts, r.Finding)
		d := r.Evidence[0].Detail
		detail[strings.ToLower(m)] = map[string]any{"devices": d["devices"], "highest_first": d["highest_first"]}
		limits = append(limits, r.Limits...)
	}
	if len(parts) == 0 {
		// each metric repeats the same explanation of why there is no data; say it once
		seen := map[string]bool{}
		var uniq []string
		for _, l := range limits {
			if !seen[l] {
				seen[l] = true
				uniq = append(uniq, l)
			}
		}
		limits = uniq
		return result.NewUnknown(performanceName, "No performance samples were returned for CPU or memory", cx, limits,
			result.Options{NextActions: []string{"inspect-environment", "inspect-collection"}})
	}
	return perfOK(strings.Join(parts, "; "), detail, limits, nil, cx), nil
}
