package skills

import (
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/result"
)

// controllerSetupRows is the one reader of controller-managed setups for every skill that shows them (inspect-collection view config for one network, inspect-platform area controller_setups for all of them): per setup
// its collector, the controllers and managed devices (type, host, port, jump server, the stored login IDS by kind and never a value, the collect flag, the last saved connectivity test and what it carries), and the test
// outcomes counted. in.Device, in.Limit and in.Offset only matter for a controller named by in.Device, whose reported managed devices are returned in full, paged.
func controllerSetupRows(setups []forward.ControllerManagedSetup, in collectionConfigInput) (rows []map[string]any, omitted []result.Omission) {
	rows = make([]map[string]any, 0, len(setups))
	for _, st := range setups {
		var devs []map[string]any
		tests := map[string]int{}
		add := func(name, role string, src controllerEntry, collect *bool, tr *forward.DeviceTestResult) {
			d := map[string]any{"name": name, "role": role}
			if src.host != "" {
				d["host"] = src.host
			}
			if src.typ != "" {
				d["type"] = src.typ
			}
			if src.port != nil {
				d["port"] = *src.port
			}
			for k, v := range map[string]string{"cli_credential_id": src.cli, "cli_credential2_id": src.cli2, "cli_credential3_id": src.cli3, "http_credential_id": src.http, "snmp_credential_id": src.snmp, "jump_server_id": src.jump} {
				if v != "" {
					d[k] = v
				}
			}
			if role == "managed" {
				d["cli_credential_set"] = src.cli != ""
				d["collect"] = collect == nil || *collect
			}
			outcome, at := setupTestOutcome(tr)
			d["test"] = outcome
			if at != "" {
				d["tested_at"] = at
			}
			if tr != nil {
				setupTestDetails(d, tr)
				if role == "controller" && in.Device != "" && matchesDeviceFilter(name, in.Device) {
					all := make([]map[string]any, 0, len(tr.ManagedDevices))
					for _, m := range tr.ManagedDevices {
						all = append(all, map[string]any{"name": m.Name, "address": m.IPAddress})
					}
					d["managed_devices_reported_count"] = len(all)
					if page, om, ok := window(all, in.Limit, in.Offset, 100, 2000, "devices reported by "+name); ok {
						d["managed_devices_reported_page"] = page
						omitted = append(omitted, om...)
					}
				}
			}
			tests[strings.SplitN(outcome, ":", 2)[0]]++
			devs = append(devs, d)
		}
		for _, c := range st.Controllers {
			add(c.Name, "controller", controllerEntry{c.Host, c.Type, c.Port, string(c.CLICredentialID), string(c.CLICredential2ID), string(c.CLICredential3ID), string(c.HTTPCredentialID), string(c.SNMPCredentialID), string(c.JumpServerID)}, nil, c.TestResult)
		}
		for _, m := range st.ManagedDevices {
			add(m.Name, "managed", controllerEntry{m.Host, m.Type, m.Port, string(m.CLICredentialID), string(m.CLICredential2ID), string(m.CLICredential3ID), string(m.HTTPCredentialID), string(m.SNMPCredentialID), string(m.JumpServerID)}, m.Collect, m.TestResult)
		}
		shown, nDevs := result.CapRow(devs, 100)
		row := map[string]any{"name": st.Name, "controllers": len(st.Controllers), "managed_devices": len(st.ManagedDevices), "tests": tests, "devices": shown, "devices_total": nDevs}
		if c := string(st.CollectorID); c != "" {
			row["collector_id"] = c
		}
		rows = append(rows, row)
	}
	return rows, omitted
}

// controllerEntry is what a controller or a managed device of a setup is configured with, in one shape for both.
type controllerEntry struct {
	host, typ                         string
	port                              *int32
	cli, cli2, cli3, http, snmp, jump string
}
