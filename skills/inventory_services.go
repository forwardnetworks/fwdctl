package skills

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// invServicesRows reads, for each device that completed collection (a virtual context is flagged by its physical name, not dropped here), the servers configured for DNS, syslog, TACACS+
// and NTP (device.services, Forward 26.11, organization property NQE_DEVICE_SERVICES). A server is the host as written (IP or FQDN) and the VRF it is reached in.
const invServicesRows = `completed(r) =
  when r is
    collectionFailed(e) -> false;
    processingFailed(e) -> false;
    completed -> true;

foreach device in network.devices
where completed(device.snapshotInfo.result)
select {
  Device: device.name,
  Physical: device.system.physicalName,
  Vendor: device.platform.vendor,
  OS: device.platform.os,
  Type: device.platform.deviceType,
  Dns: (foreach s in device.services.dnsService.servers select (when s.host is ip(a) -> toString(a); fqdn(f) -> f) + "@" + s.networkInstance),
  Log: (foreach s in device.services.logService.servers select (when s.host is ip(a) -> toString(a); fqdn(f) -> f) + "@" + s.networkInstance),
  Tacacs: (foreach s in device.services.tacacsService.servers select (when s.host is ip(a) -> toString(a); fqdn(f) -> f) + "@" + s.networkInstance),
  Ntp: (foreach s in device.services.ntpService.servers select (when s.host is ip(a) -> toString(a); fqdn(f) -> f) + "@" + s.networkInstance)
}`

var serviceNames = []struct{ key, field, label string }{{"dns", "Dns", "DNS"}, {"syslog", "Log", "syslog"}, {"tacacs", "Tacacs", "TACACS+"}, {"ntp", "Ntp", "NTP"}}

const maxServiceDevicesShown = 25

// serverSet is a device's servers for one service as a sorted, comma-joined list of host@vrf (the query joins them; the default VRF is dropped). Order is not compared: two devices that list the same servers
// in a different order have the same set.
func serverSet(v any) (string, int) {
	list, _ := v.([]any)
	var out []string
	for _, e := range list {
		h, _ := e.(string)
		if h == "" {
			continue
		}
		if i := strings.LastIndex(h, "@"); i >= 0 && h[i+1:] == "default" {
			h = h[:i]
		}
		if h == "" || h[0] == '@' {
			continue // a server with no host is not a server
		}
		out = append(out, strings.ToLower(h))
	}
	sort.Strings(out)
	out = slices.Compact(out) // Forward does not deduplicate: a server listed twice is one server
	return strings.Join(out, ","), len(out)
}

// inventoryServices is kind services: which devices have no DNS, syslog, TACACS+ or NTP server configured, and which server sets are in use. A device with no server is a fact about its
// configuration, not by itself a defect (a lab router may not need TACACS), so the answer is counts and lists and never a pass.
func inventoryServices(ctx context.Context, s *fwd.Session, in inventoryInput, cx result.Context, limits []string) (result.Result, error) {
	rows, nqeTotal, truncated, err := s.RunNQEAll(ctx, in.NetworkID, fwd.SnapshotID(cx), invServicesRows, 50000)
	if err != nil {
		if diags, _, isQuery := fwd.QueryErrors(err); isQuery && missingServicesField(diags, err) {
			return result.NewUnknown(inventoryName, "This Forward does not offer device services in NQE, so DNS, syslog, TACACS+ and NTP servers cannot be read here", cx,
				append(limits, "device.services needs Forward 26.11 and the organization property NQE_DEVICE_SERVICES (inspect-environment shows it); the read failed: "+err.Error()), result.Options{NextActions: []string{"inspect-environment", "inspect-device-files"}})
		}
		return result.Result{}, err
	}
	if truncated {
		limits = append(limits, fmt.Sprintf("this read the first %d of %d devices, so every count here is low", len(rows), nqeTotal))
	}
	if len(rows) == 0 {
		return result.NewUnknown(inventoryName, "No physical device completed collection in this snapshot, so there are no services to report", cx, append(limits, "no device was read, so nothing about DNS, syslog, TACACS+ or NTP was measured"), result.Options{NextActions: []string{"investigate-collection-failure"}})
	}
	// A virtual context (a vsys, a VDOM, an ASA context) is its own row whose physical name is its chassis. It is left out of the counts only when that chassis is itself a row (it is counted there);
	// a row whose physical name matches no row is counted, and a limit says how many.
	names := map[string]bool{}
	for _, r := range rows {
		names[strings.ToLower(str(r["Device"]))] = true
	}
	var counted []map[string]any
	virtualSkipped, orphans := 0, 0
	for _, r := range rows {
		dev, phys := strings.ToLower(str(r["Device"])), strings.ToLower(str(r["Physical"]))
		if phys != "" && phys != dev {
			if names[phys] {
				virtualSkipped++
				continue
			}
			orphans++
		}
		counted = append(counted, r)
	}
	type svc struct {
		with, without int
		sets          map[string]int // server set -> devices
		lacking       []map[string]any
		byType        map[string]int
	}
	stats := map[string]*svc{}
	for _, sn := range serviceNames {
		stats[sn.key] = &svc{sets: map[string]int{}, byType: map[string]int{}}
	}
	type platform struct{ devices, withAny int }
	byPlatform := map[string]*platform{}
	for _, r := range counted {
		pk := str(r["Vendor"]) + " " + str(r["OS"])
		if byPlatform[pk] == nil {
			byPlatform[pk] = &platform{}
		}
		byPlatform[pk].devices++
		for _, sn := range serviceNames {
			st := stats[sn.key]
			set, n := serverSet(r[sn.field])
			if n == 0 {
				st.without++
				st.byType[str(r["Type"])]++
				st.lacking = append(st.lacking, map[string]any{"device": str(r["Device"]), "type": str(r["Type"]), "vendor": str(r["Vendor"]), "os": str(r["OS"])})
				continue
			}
			st.with++
			st.sets[set]++
			byPlatform[pk].withAny++
		}
	}
	total := len(counted)
	detail := map[string]any{"devices": total, "virtual_contexts_not_counted": virtualSkipped, "virtual_contexts_whose_chassis_is_not_a_row_counted": orphans}
	var parts []string
	allNone := true
	perService := map[string]any{}
	var omitted []result.Omission
	for _, sn := range serviceNames {
		st := stats[sn.key]
		if st.with > 0 {
			allNone = false
		}
		type setCount struct {
			servers string
			devices int
		}
		var sets []setCount
		for k, v := range st.sets {
			sets = append(sets, setCount{k, v})
		}
		sort.SliceStable(sets, func(i, j int) bool {
			if sets[i].devices != sets[j].devices {
				return sets[i].devices > sets[j].devices
			}
			return sets[i].servers < sets[j].servers
		})
		topSets := []map[string]any{}
		for i, sc := range sets {
			if i == 5 {
				break
			}
			topSets = append(topSets, map[string]any{"servers": sc.servers, "devices": sc.devices})
		}
		if len(sets) > 5 {
			omitted = append(omitted, result.Omission{What: "server sets for " + sn.label, Total: len(sets), Shown: 5, Next: "the counts cover every set; list the rest with author-nqe-query over device.services"})
		}
		sort.SliceStable(st.lacking, func(i, j int) bool { return st.lacking[i]["device"].(string) < st.lacking[j]["device"].(string) })
		shown, capped := result.Cap(st.lacking, maxServiceDevicesShown, "devices with no "+sn.label+" server", "the count is complete; list the rest with author-nqe-query over device.services or read one device with inspect-device-files")
		omitted = append(omitted, capped...)
		perService[sn.key] = map[string]any{"configured": st.with, "none_configured": st.without, "none_by_device_type": st.byType, "distinct_server_sets": len(sets), "most_common_server_sets": topSets, "devices_with_none_shown": shown}
		parts = append(parts, fmt.Sprintf("%s %d", sn.label, st.with))
	}
	detail["services"] = perService
	// a platform where no device has any server in any service may be one the parser does not read for services, which looks the same as none configured
	var unparsed []string
	for pk, p := range byPlatform {
		if p.devices > 0 && p.withAny == 0 && !allNone {
			unparsed = append(unparsed, fmt.Sprintf("%s (%d)", strings.TrimSpace(pk), p.devices))
		}
	}
	sort.Strings(unparsed)
	if len(unparsed) > 0 {
		detail["platforms_with_no_server_in_any_service"] = unparsed
	}
	limits = append(limits,
		"a device with no server is a fact about its configuration, not by itself a defect: a lab device may not need TACACS. Read who should have what from the organization's standard, not from these counts",
		"servers are what Forward parsed from the configuration (host as written, VRF when not default, a server listed twice counted once); a server set that differs between devices can be intended (a site-local NTP) or drift",
		fmt.Sprintf("a device that failed collection is not in this read (none read is not the same as none configured); %d virtual context(s) whose chassis is also a row are not counted separately (a context can configure its own servers: read it with inspect-device-files); %d virtual context(s) whose chassis is not a row are counted", virtualSkipped, orphans),
		fmt.Sprintf("the lists show the first %d devices per service in name order and the five most common server sets; the counts cover all", maxServiceDevicesShown))
	if len(unparsed) > 0 {
		limits = append(limits, "no device of these platforms has a server in any service, while others do: "+strings.Join(unparsed, ", ")+". The parser may not read device services for them; check one with inspect-device-files before reading the zeros as none configured")
	}
	finding := fmt.Sprintf("Of %d devices, the number with at least one server: %s", total, strings.Join(parts, ", "))
	opts := result.Options{Limits: limits, Omitted: omitted, NextActions: []string{"inspect-device-files", "check-network-compliance", "inspect-environment"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvState, "runNqeQuery", cx.SnapshotID, detail, finding)}}
	if allNone {
		opts.Limits = append(opts.Limits, "no device has any DNS, syslog, TACACS+ or NTP server in Forward's model: if the devices do have them in their configuration, the parser may not read them for these platforms or the snapshot was processed before NQE_DEVICE_SERVICES was on. Check one device's configuration with inspect-device-files before trusting zeros")
		return result.NewUnknown(inventoryName, finding+": none anywhere, which proves nothing about the configuration", cx, opts.Limits, opts)
	}
	return result.Build(inventoryName, result.OK, finding, result.Deterministic, cx, opts)
}

// missingServicesField says a query error is Forward's "Record does not have field" for services: the field is absent from this build or organization. Any other failure (a timeout, a permission error,
// a different query error) is not this.
func missingServicesField(diags []fwd.QueryDiagnostic, err error) bool {
	for _, d := range diags {
		if strings.Contains(d.Message, "does not have field") && strings.Contains(d.Message, "services") {
			return true
		}
	}
	return false
}
