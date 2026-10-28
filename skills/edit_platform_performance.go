package skills

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	forward "github.com/forwardnetworks/forward-go-sdk"
	"github.com/forwardnetworks/forward-go-sdk/perfpayload"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// planPerformance is edit-platform area performance, for a telemetry source other than Forward's own SNMP polling: disable turns that polling off for a network, and upload sends one batch of
// device performance samples (interface counters, CPU, memory, uptime) as a collector. An upload cannot be deleted afterwards, so it is refused while Forward's own polling is still on, refuses
// a device that already has performance rows unless the caller says it owns them, and needs confirm. The collector key (username:password, as Forward shows it once at registration) comes from
// secret_file or secret_env, only when applying.
func planPerformance(ctx context.Context, s *fwd.Session, in editPlatformInput) (*networkPlan, error) {
	switch in.Action {
	case "disable":
		return planPerformanceDisable(ctx, s, in)
	case "upload":
		return planPerformanceUpload(ctx, s, in)
	}
	return nil, fmt.Errorf("%w: area performance takes action disable (turn Forward's own SNMP performance polling off for a network) or upload", ErrInvalidInput)
}

func planPerformanceDisable(ctx context.Context, s *fwd.Session, in editPlatformInput) (*networkPlan, error) {
	var def struct {
		NetworkID string `json:"network_id"`
	}
	if err := decodeDefinition(in.Definition, &def, "network_id"); err != nil {
		return nil, err
	}
	if def.NetworkID == "" {
		return nil, fmt.Errorf("%w: disable needs network_id", ErrInvalidInput)
	}
	cur, _, err := s.Client.Performance.Settings(ctx, def.NetworkID)
	if err != nil {
		return nil, err
	}
	p := &networkPlan{target: "turn off Forward's own SNMP performance polling on network " + def.NetworkID, action: "disable_performance_polling",
		before: map[string]any{"enabled": cur.Enabled, "interval_minutes": cur.Interval()}, after: map[string]any{"enabled": false}, reversible: true,
		undo: "set enabled true again through Forward's performance settings; Forward refuses that (400) until an SNMP credential exists, and polling restarts on the collectors",
		limits: []string{"this stops Forward polling the network's devices over SNMP and sends the collectors a stop; it does not delete performance data already stored, and per-device enableSnmpCollection flags are left as they are",
			"it does not touch SNMP used for anything else (custom-OID command groups, SNMP endpoints, credential discovery): those are separate settings (source-read, not confirmed on a live Forward)"}}
	if !cur.Enabled {
		p.noop = "Forward's own SNMP performance polling is already off on this network"
		return p, nil
	}
	p.do = func(ctx context.Context) error {
		off := false
		_, _, err := s.Client.Performance.PatchSettings(ctx, def.NetworkID, forward.PerformanceSettingsPatch{Enabled: &off})
		return err
	}
	p.verify = func(ctx context.Context) (bool, any, error) {
		now, _, err := s.Client.Performance.Settings(ctx, def.NetworkID)
		if err != nil {
			return false, nil, err
		}
		return !now.Enabled, map[string]any{"enabled": now.Enabled}, nil
	}
	return p, nil
}

type perfCounters struct {
	InOctets      uint64 `json:"in_octets"`
	InDiscards    uint64 `json:"in_discards"`
	InErrors      uint64 `json:"in_errors"`
	OutOctets     uint64 `json:"out_octets"`
	OutDiscards   uint64 `json:"out_discards"`
	OutErrors     uint64 `json:"out_errors"`
	SpeedMbps     uint32 `json:"speed_mbps"`
	InterfaceType uint32 `json:"interface_type"`
}

type perfDevice struct {
	Name          string                  `json:"name"`
	VendorOS      string                  `json:"vendor_os"`
	Time          string                  `json:"time"`
	UptimeSeconds uint32                  `json:"uptime_seconds"`
	CPU           float32                 `json:"cpu"`
	Memory        float32                 `json:"memory"`
	Interfaces    map[string]perfCounters `json:"interfaces"`
}

func planPerformanceUpload(ctx context.Context, s *fwd.Session, in editPlatformInput) (*networkPlan, error) {
	var def struct {
		NetworkID string            `json:"network_id"`
		Devices   []perfDevice      `json:"devices"`
		Claimed   []string          `json:"claimed"`
		Newest    map[string]string `json:"newest"`
	}
	fields := "network_id, devices (name, vendor_os, time RFC3339, cpu and memory as fractions 0..1, uptime_seconds, interfaces keyed by ifName with in_octets, out_octets, in_errors, out_errors, in_discards, out_discards, speed_mbps, interface_type), claimed (device names you own the telemetry of), newest (device -> RFC3339 time of your newest sample already sent)"
	if err := decodeDefinition(in.Definition, &def, fields); err != nil {
		return nil, err
	}
	if def.NetworkID == "" || len(def.Devices) == 0 {
		return nil, fmt.Errorf("%w: upload needs network_id and at least one device", ErrInvalidInput)
	}
	payload := perfpayload.Payload{}
	samples, ifaces := 0, 0
	for _, d := range def.Devices {
		t, err := time.Parse(time.RFC3339, d.Time)
		if err != nil {
			return nil, fmt.Errorf("%w: device %q time %q is not RFC3339", ErrInvalidInput, d.Name, d.Time)
		}
		vos, ok := perfpayload.LookupVendorOS(d.VendorOS)
		if !ok {
			return nil, fmt.Errorf("%w: device %q vendor_os %q is not a Forward vendor OS name (perfpayload.VendorOS lists them)", ErrInvalidInput, d.Name, d.VendorOS)
		}
		dp := perfpayload.DevicePerf{DeviceName: d.Name, VendorOS: vos, Time: t, UptimeSeconds: d.UptimeSeconds, CPU: d.CPU, Memory: d.Memory, Interfaces: map[string]perfpayload.Counters{}}
		for name, c := range d.Interfaces {
			dp.Interfaces[name] = perfpayload.Counters{InOctets: c.InOctets, InDiscards: c.InDiscards, InErrors: c.InErrors, OutOctets: c.OutOctets, OutDiscards: c.OutDiscards, OutErrors: c.OutErrors, SpeedMbps: c.SpeedMbps, InterfaceType: c.InterfaceType}
			ifaces++
		}
		payload.Devices = append(payload.Devices, dp)
		samples++
	}
	payload.Time = time.Now()
	if err := payload.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	settings, _, err := s.Client.Performance.Settings(ctx, def.NetworkID)
	if err != nil {
		return nil, err
	}
	if settings.Enabled {
		return nil, fmt.Errorf("%w: Forward's own SNMP performance polling is still on for network %s; two sources would write the same devices. Turn it off first (edit-platform area performance, action disable), then upload", ErrInvalidInput, def.NetworkID)
	}
	// which devices already hold rows (their own or another source's): the last 7 days
	now := time.Now().UTC()
	have, _, err := s.Client.Performance.DevicesWithData(ctx, def.NetworkID, forward.MetricQuery{StartTime: now.AddDate(0, 0, -7).Format(time.RFC3339), EndTime: now.Format(time.RFC3339)})
	if err != nil {
		return nil, err
	}
	wm := perfpayload.Watermarks{Newest: map[string]time.Time{}, Claimed: map[string]bool{}}
	for _, n := range have {
		if _, ok := wm.Newest[n]; !ok {
			wm.Newest[n] = time.Time{} // has rows; its newest time is Forward's shifted clock, so only the caller's own record of what it sent (newest) is a watermark
		}
	}
	for _, c := range def.Claimed {
		wm.Claimed[c] = true
	}
	for n, ts := range def.Newest {
		t, err := time.Parse(time.RFC3339, ts)
		if err != nil {
			return nil, fmt.Errorf("%w: newest[%q] %q is not RFC3339", ErrInvalidInput, n, ts)
		}
		wm.Newest[n] = t
	}
	send, refused := wm.Filter(payload)
	rows := make([]map[string]any, 0, len(refused))
	for _, r := range refused {
		rows = append(rows, map[string]any{"device": r.Device, "time": r.Time.UTC().Format(time.RFC3339), "reason": string(r.Reason)})
	}
	sort.Slice(rows, func(i, j int) bool { return fmt.Sprint(rows[i]["device"]) < fmt.Sprint(rows[j]["device"]) })
	shown, nrefused := result.CapRow(rows, 25)
	names := make([]string, 0, len(send.Devices))
	for _, d := range send.Devices {
		names = append(names, d.DeviceName)
	}
	after := map[string]any{"devices_sent": len(send.Devices), "samples_given": samples, "interfaces_given": ifaces, "refused": nrefused, "refused_rows": shown,
		"devices_with_rows_in_forward": len(have), "claimed": len(def.Claimed)}
	p := &networkPlan{target: fmt.Sprintf("upload performance samples for %d device(s) to network %s", len(send.Devices), def.NetworkID), action: "upload_performance",
		before: map[string]any{"polling_enabled": false, "devices_with_rows_in_forward": len(have)}, after: after, reversible: false, confirm: "upload",
		limits: []string{"an upload cannot be deleted or replaced afterwards: Forward records no source for it, and a sample older than a device's newest is dropped silently (HTTP 204 either way)",
			"the collector key is read only when applying (secret_file or secret_env, as username:password); the upload is accepted for any collector of the network's organization, with no per-network or role check (source-read, not run on a live Forward)",
			"a device 'has rows' when it appears in Forward's performance data for the last 7 days; a refused device is held back, not sent. newest is your own record of the times you sent: Forward stores each sample shifted by the batch's clock lag, so times read back from it are not comparable",
			"interface utilization above 1 drops that interface only, and cannot be checked before sending"},
		undo: "none: uploaded samples stay until Forward's retention (PERF_DATA_RETENTION_DAYS) removes them"}
	if len(send.Devices) == 0 {
		p.noop = "nothing to send: every device was refused (see refused_rows)"
		return p, nil
	}
	p.do = func(ctx context.Context) error {
		sec, err := fwd.ReadSecret(in.SecretFile, in.SecretEnv)
		if err != nil {
			return err
		}
		key := strings.TrimPrefix(strings.TrimSpace(sec.Reveal()), "TOKEN=")
		user, pass, ok := strings.Cut(key, ":")
		if !ok || user == "" || pass == "" {
			return fmt.Errorf("%w: the collector key must be username:password", ErrInvalidInput)
		}
		send.Time = time.Now()
		_, err = s.Client.Performance.UploadPayload(ctx, def.NetworkID, user, pass, send)
		return err
	}
	p.verify = func(ctx context.Context) (bool, any, error) {
		end := time.Now().UTC()
		got, _, err := s.Client.Performance.DevicesWithData(ctx, def.NetworkID, forward.MetricQuery{StartTime: end.Add(-time.Hour).Format(time.RFC3339), EndTime: end.Add(time.Minute).Format(time.RFC3339)})
		if err != nil {
			return false, nil, err
		}
		seen := map[string]bool{}
		for _, n := range got {
			seen[n] = true
		}
		var missing []string
		for _, n := range names {
			if !seen[n] {
				missing = append(missing, n)
			}
		}
		miss, total := result.CapRow(missing, 25)
		return len(missing) == 0, map[string]any{"devices_sent": len(names), "not_yet_visible": miss, "not_yet_visible_count": total}, nil
	}
	return p, nil
}
