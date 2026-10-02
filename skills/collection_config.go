package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const collectionConfigName = collectionName

type collectionConfigInput struct {
	NetworkID string `json:"network_id"`
	Limit     int    `json:"limit"`
	Offset    int    `json:"offset"`
	// DataFile names one of the org's data files (see detail.data_files) to also read its inferred NQE schema and a content preview.
	// A read, not a write: Forward's inference needs no credential and stores nothing.
	DataFile string `json:"data_file"`
	// DataConnector names one of the network's data connectors (see detail.data_connectors) to also read its endpoints, status and last test result.
	DataConnector string `json:"data_connector"`
}

// inspectCollectionConfig states what Forward is told to collect: devices, endpoints, jump servers and proxies. It never reads a
// credential: a device shows only whether a credential is set, and no secret, credential id or username is copied out.
func inspectCollectionConfig(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in collectionConfigInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	detail := map[string]any{}
	var limits []string
	read, total := 0, 0

	if devs, err := s.ClassicDevices(ctx, in.NetworkID); err != nil {
		limits = append(limits, "the collection devices could not be read: "+err.Error())
	} else {
		read++
		total += len(devs)
		rows := make([]map[string]any, 0, len(devs))
		for _, d := range devs {
			row := map[string]any{"name": d.Name, "host": d.Host, "type": d.Type, "cli_credential_set": d.CLICredentialID != "",
				"http_credential_set": d.HTTPCredentialID != "", "collect": d.Collect == nil || *d.Collect}
			if d.Port != nil {
				row["port"] = *d.Port
			}
			rows = append(rows, row)
		}
		win, wl, ok := window(rows, in.Limit, in.Offset, 50, 500, "devices")
		if ok {
			detail["devices"] = win
			limits = append(limits, wl...)
		} else {
			detail["devices"] = []map[string]any{}
			if len(rows) > 0 {
				limits = append(limits, fmt.Sprintf("offset %d is past the end of the %d devices", in.Offset, len(rows)))
			}
		}
		detail["device_count"] = len(rows)
		// a summary of the WHOLE list, whatever page is shown: a network of tens of thousands of devices is read by its totals first
		byType := map[string]int{}
		noCLI, snmpOn, snmpUnknown := 0, 0, 0
		for _, d := range devs {
			t := d.Type
			if t == "" {
				t = "(none)"
			}
			byType[t]++
			if d.CLICredentialID == "" {
				noCLI++
			}
			switch {
			case d.EnableSNMPCollection == nil:
				snmpUnknown++
			case *d.EnableSNMPCollection:
				snmpOn++
			}
		}
		summary := map[string]any{"by_type": topDeviceCounts(byType, 15), "without_cli_credential": noCLI, "snmp_collection_on": snmpOn}
		if snmpUnknown > 0 {
			summary["snmp_collection_unstated"] = snmpUnknown
		}
		detail["summary"] = summary
		limits = append(limits, "summary counts the whole device list, not the page shown. Forward's device records carry no vendor, jump server or creation time, so a count by vendor or jump server, and when a device was added, cannot be read here: the device count of each collection over time is in investigate-collection-failure view history")
		notCollected := 0
		for _, r := range rows {
			if r["collect"] == false {
				notCollected++
			}
		}
		detail["devices_not_collected"] = notCollected
	}
	if eps, err := s.Endpoints(ctx, in.NetworkID); err != nil {
		limits = append(limits, "the endpoints could not be read: "+err.Error())
	} else {
		read++
		total += len(eps)
		rows := make([]map[string]any, 0, len(eps))
		for _, e := range eps {
			rows = append(rows, map[string]any{"name": e.Name, "type": e.Type, "host": e.Host, "protocol": e.Protocol,
				"profile_id": nilIfEmpty(e.ProfileID), "credential_set": e.CredentialID != "", "via_jump_server": e.JumpServerID != "", "collect": e.Collect == nil || *e.Collect})
		}
		byProfile := map[string]int{}
		for _, e := range eps {
			byProfile[e.Type+" "+orDash(e.ProfileID)]++
		}
		win, wl, ok := window(rows, in.Limit, in.Offset, 50, 500, "endpoints")
		if ok {
			detail["endpoints"] = win
			limits = append(limits, wl...)
		} else {
			detail["endpoints"] = []map[string]any{}
		}
		detail["endpoint_count"], detail["endpoints_by_type_and_profile"] = len(rows), byProfile
		limits = append(limits, endpointProfiles(ctx, s, eps, detail)...)
	}
	if js, err := s.JumpServers(ctx, in.NetworkID); err != nil {
		limits = append(limits, "the jump servers could not be read: "+err.Error())
	} else {
		read++
		total += len(js)
		rows := make([]map[string]any, 0, len(js))
		for _, j := range js {
			rows = append(rows, map[string]any{"id": string(j.ID), "host": j.Host, "port": j.Port})
		}
		detail["jump_servers"] = rows
	}
	if px, err := s.Proxies(ctx, in.NetworkID); err != nil {
		limits = append(limits, "the proxies could not be read: "+err.Error())
	} else {
		read++
		total += len(px)
		rows := make([]map[string]any, 0, len(px))
		for _, p := range px {
			rows = append(rows, map[string]any{"name": p.Name, "host": p.Host, "port": p.Port, "protocol": p.Protocol, "cert_checking_disabled": p.DisableCertChecking})
		}
		detail["proxies"] = rows
	}
	if sc, _, err := s.Client.CollectionSchedules.List(ctx, in.NetworkID); err != nil {
		limits = append(limits, "the collection schedules could not be read: "+err.Error())
	} else {
		read++
		rows := make([]map[string]any, 0, len(sc))
		for _, c := range sc {
			row := map[string]any{"id": string(c.ID), "enabled": c.Enabled, "time_zone": nilIfEmpty(c.TimeZone), "start_at": nilIfEmpty(c.StartAt), "end_at": nilIfEmpty(c.EndAt)}
			if c.Periodic() {
				row["kind"], row["every_seconds"] = "periodic", *c.PeriodInSeconds
			} else {
				row["kind"], row["times"], row["days_of_week"] = "times", c.Times, weekdays(c.DaysOfTheWeek)
			}
			rows = append(rows, row)
		}
		detail["collection_schedules"] = rows
		if len(rows) == 0 {
			limits = append(limits, "no collection schedule is configured: collections run only when started (or by uploads)")
		} else {
			limits = append(limits, "schedules are as configured: Forward does not return a next run time, and an empty time zone means the organization's preferred zone. days_of_week is empty when every day applies or none was set")
		}
	}
	if ca, _, err := s.Client.CloudAccounts.List(ctx, in.NetworkID); err != nil {
		limits = append(limits, "the cloud setups could not be read: "+err.Error())
	} else if len(ca) > 0 {
		read++
		total += len(ca)
		rows := make([]map[string]any, 0, len(ca))
		for _, c := range ca {
			row := map[string]any{"name": c.Name, "type": c.Type, "collect": c.Collect}
			var regions []map[string]any
			failing := 0
			for _, name := range sortedKeys(c.Regions) {
				r := c.Regions[name]
				entry := map[string]any{"region": name, "last_test": testOutcome(r.Error, r.TestInstant)}
				if r.TestInstant > 0 {
					entry["tested_at"] = time.UnixMilli(r.TestInstant).UTC().Format(time.RFC3339)
				}
				if r.Error != "" && r.Error != "NONE" {
					failing++
				}
				regions = append(regions, entry)
			}
			for _, name := range sortedKeys(c.TestResults) { // Azure carries its test per subscription
				r := c.TestResults[name]
				entry := map[string]any{"subscription": name, "last_test": testOutcome(r.Error, r.TestInstant)}
				if r.Error != "" && r.Error != "NONE" {
					failing++
				}
				regions = append(regions, entry)
			}
			if len(regions) > 0 {
				row["regions"] = regions
			} else {
				row["regions"] = "none configured or none reported: the setup's own region list is not in the answer"
			}
			if failing > 0 {
				row["failing_tests"] = failing
			}
			if c.ProxyServerID != "" {
				row["proxy_server_id"] = c.ProxyServerID
			}
			if c.Concurrency != nil {
				row["concurrency"] = *c.Concurrency
			}
			rows = append(rows, row)
		}
		detail["cloud_setups"] = rows
		limits = append(limits, "cloud_setups lists each cloud collection source with its configured regions and the result of its last connectivity TEST (not of the last collection: Forward keeps no per-setup collection outcome here). The regions decide which zones Forward keeps from the cloud's aggregated lists, so a missing instance can be a region that is not listed; collector errors during a collection are in investigate-collection-failure view exceptions, and inspect-inventory kind cloud_accounts shows whether each account was collected. Credentials are never read or shown")
	}
	if dfs, _, err := s.Client.DataFiles.List(ctx); err != nil {
		limits = append(limits, "the organization's data files could not be read: "+err.Error())
	} else if len(dfs) > 0 {
		read++
		total += len(dfs)
		rows := make([]map[string]any, 0, len(dfs))
		attachedHere := 0
		for _, f := range dfs {
			here := false
			for _, nid := range f.NetworkIDs {
				if nid == in.NetworkID {
					here = true
				}
			}
			if here {
				attachedHere++
			}
			row := map[string]any{"name": f.Name, "nqe_name": f.NQEName, "type": f.Type, "attached_to_this_network": here, "attached_to_networks": len(f.NetworkIDs)}
			if f.Description != "" {
				row["description"] = f.Description
			}
			if f.IsEmpty {
				row["empty"] = true
			}
			rows = append(rows, row)
		}
		detail["data_files"] = rows
		limits = append(limits, fmt.Sprintf("data_files: %d org-wide (%d attached to this network); a query reads an attached one as network.extensions.<nqe_name>, a record {status: OK|MISSING|INVALID_DATA, value}. MISSING means no snapshot of this network has carried it yet (attaching a file affects only later snapshots). inspect-inventory has no kind for this, since the shape of value is per-file, not a fixed schema root; give data_file to see one's inferred fields", len(dfs), attachedHere))
		if in.DataFile != "" {
			found := false
			for _, f := range dfs {
				if f.Name == in.DataFile {
					found = true
				}
			}
			if !found {
				limits = append(limits, fmt.Sprintf("data_file %q does not match any of the %d listed names; names are exact", in.DataFile, len(dfs)))
			} else if inf, _, err := s.Client.DataFiles.Schema(ctx, in.DataFile); err != nil {
				limits = append(limits, fmt.Sprintf("the inferred schema of data_file %q could not be read: %v", in.DataFile, err))
			} else {
				preview := map[string]any{"name": in.DataFile, "data_format": inf.Inference.DataFormat, "warnings": inf.Inference.Warnings, "errors": inf.Inference.Errors}
				if len(inf.Inference.Schema) > 0 {
					preview["schema"] = json.RawMessage(inf.Inference.Schema)
				}
				if len(inf.Content) > 2000 {
					preview["content_preview"] = inf.Content[:2000] + "..."
					limits = append(limits, fmt.Sprintf("data_file %q: the content preview is truncated at 2000 of %d characters", in.DataFile, len(inf.Content)))
				} else {
					preview["content_preview"] = inf.Content
				}
				detail["data_file_schema"] = preview
				if len(inf.Inference.Errors) > 0 {
					limits = append(limits, fmt.Sprintf("data_file %q: Forward's own inference reports errors reading it as stored: %v", in.DataFile, inf.Inference.Errors))
				}
			}
		}
	} else if in.DataFile != "" {
		limits = append(limits, fmt.Sprintf("data_file %q was given but this organization has no data files", in.DataFile))
	}
	if dcs, _, err := s.Client.DataConnectors.List(ctx, in.NetworkID, forward.DataConnectorReadOptions{Status: true}); err != nil {
		limits = append(limits, "the network's data connectors could not be read: "+err.Error())
	} else if len(dcs.Connectors) > 0 {
		read++
		total += len(dcs.Connectors)
		rows := make([]map[string]any, 0, len(dcs.Connectors))
		for _, c := range dcs.Connectors {
			row := map[string]any{"name": c.Name, "base_url": c.BaseURL, "endpoints": len(c.Endpoints), "collect": c.Collects()}
			if c.CredentialID != "" {
				row["credential_set"] = true
			}
			switch {
			case dcs.SnapshotID == "":
				row["status"] = "unknown (no processed snapshot)"
			case c.Status == nil:
				row["status"] = "missing (not in the latest snapshot: never collected, or excluded)"
			case c.Status.Error == "":
				row["status"] = "ok"
			default:
				row["status"] = c.Status.Error
			}
			rows = append(rows, row)
		}
		detail["data_connectors"] = rows
		limits = append(limits, fmt.Sprintf("data_connectors: %d on this network; a connector is a per-network HTTP source the Collector polls each collection, stored as network.dataConnectors (unlike a data file, which is organization-wide). status reflects snapshot %s; give data_connector to see one's endpoints and last connectivity test result. Credentials are never read or shown, only whether one is set", len(dcs.Connectors), dcs.SnapshotID))
		if in.DataConnector != "" {
			var found *forward.DataConnector
			for i := range dcs.Connectors {
				if dcs.Connectors[i].Name == in.DataConnector {
					found = &dcs.Connectors[i]
				}
			}
			if found == nil {
				limits = append(limits, fmt.Sprintf("data_connector %q does not match any of the %d listed names; names are exact", in.DataConnector, len(dcs.Connectors)))
			} else if c, _, err := s.Client.DataConnectors.Get(ctx, in.NetworkID, in.DataConnector, forward.DataConnectorReadOptions{Status: true, TestResult: true}); err != nil {
				limits = append(limits, fmt.Sprintf("data_connector %q could not be read: %v", in.DataConnector, err))
			} else if c == nil {
				limits = append(limits, fmt.Sprintf("data_connector %q was listed but is gone now", in.DataConnector))
			} else {
				eps := make([]map[string]any, 0, len(c.Endpoints))
				for _, e := range c.Endpoints {
					eps = append(eps, map[string]any{"name": e.Name, "path": e.Path})
				}
				view := map[string]any{"name": c.Name, "base_url": c.BaseURL, "endpoints": eps, "collect": c.Collects()}
				if c.ProxyServerID != "" {
					view["proxy_set"] = true
				}
				if c.CollectorID != "" {
					view["collector_id"] = c.CollectorID
				}
				if c.TestResult != nil {
					view["last_test"] = map[string]any{"started_at": c.TestResult.StartedAt, "ended_at": c.TestResult.EndedAt, "error": c.TestResult.Error, "error_desc": c.TestResult.ErrorDesc}
				} else {
					limits = append(limits, fmt.Sprintf("data_connector %q has no stored test result; edit-data-connector can run one", in.DataConnector))
				}
				detail["data_connector_detail"] = view
			}
		}
	} else if in.DataConnector != "" {
		limits = append(limits, fmt.Sprintf("data_connector %q was given but this network has no data connectors", in.DataConnector))
	}
	limits = append(limits, "credentials are never read or shown; a device lists only whether one is set")
	if read == 0 {
		return result.NewUnknown(collectionConfigName, "The collection configuration could not be read", cx, limits, result.Options{})
	}
	if total == 0 {
		return result.NewUnknown(collectionConfigName, "No collection targets are configured for this network", cx,
			append(limits, "nothing to collect is configured through these routes; devices may arrive by snapshot upload or a cloud setup instead"), result.Options{})
	}
	finding := fmt.Sprintf("%v devices, %v endpoints, %d jump servers, %d proxies configured", detail["device_count"], detail["endpoint_count"], lenAny(detail["jump_servers"]), lenAny(detail["proxies"]))
	if cs := lenAny(detail["cloud_setups"]); cs > 0 {
		finding += fmt.Sprintf(", %d cloud setups", cs)
	}
	return result.Build(collectionConfigName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits,
		NextActions: []string{"inspect-collection", "investigate-collection-failure"},
		Evidence:    []result.Evidence{result.NewEvidence(result.EvCollection, "inspectCollectionConfig", nil, detail, finding)}})
}

func lenAny(v any) int {
	if r, ok := v.([]map[string]any); ok {
		return len(r)
	}
	return 0
}

// endpointProfiles adds the definitions of the profiles this network's endpoints use (org-wide objects: the other profiles are counted,
// not listed). An SNMP profile lists the OIDs it collects, a CLI profile its commands, an HTTP profile its requests; HTTP header values
// are never shown. It returns limits; a failed read is a limit, not an empty list.
func endpointProfiles(ctx context.Context, s *fwd.Session, eps []forward.Endpoint, detail map[string]any) []string {
	used := map[string]bool{}
	for _, e := range eps {
		if e.ProfileID != "" {
			used[e.ProfileID] = true
		}
	}
	if len(eps) == 0 {
		return nil
	}
	profiles, _, err := s.Client.Endpoints.ListProfiles(ctx, "")
	if err != nil {
		return []string{"the endpoint profiles could not be read, so what the endpoints collect is not shown: " + err.Error()}
	}
	var rows []map[string]any
	cli := false
	for _, p := range profiles {
		if !used[string(p.ID)] {
			continue
		}
		row := map[string]any{"id": string(p.ID), "name": p.Name, "type": p.Type}
		switch p.Type {
		case "SNMP":
			oids := make([]map[string]string, 0, len(p.CustomOIDs))
			for _, o := range p.CustomOIDs {
				oids = append(oids, map[string]string{"name": o.Name, "oid": o.OID})
			}
			row["oid_sets"], row["custom_oids"], row["detector_oid"] = p.OIDSets, oids, nilIfEmpty(p.DetectorOID)
		case "CLI":
			cli = true
			row["command_sets"], row["custom_commands"], row["detector_command"] = p.CommandSets, p.CustomCommands, nilIfEmpty(p.DetectorCommand)
		case "HTTP":
			reqs := make([]map[string]string, 0, len(p.Endpoints))
			for _, h := range p.Endpoints {
				reqs = append(reqs, map[string]string{"name": h.Name, "path": h.Path})
			}
			hdrs := make([]string, 0, len(p.Headers))
			for k := range p.Headers {
				hdrs = append(hdrs, k)
			}
			sort.Strings(hdrs)
			row["https"], row["auth_type"], row["requests"], row["header_names"] = p.HTTPS, p.AuthType, reqs, hdrs
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i]["id"].(string) < rows[j]["id"].(string) })
	detail["endpoint_profiles"] = rows
	limits := []string{fmt.Sprintf("%d endpoint profile(s) exist in the organization; %d used by this network's endpoints are shown. Profiles are organization-wide, and each type's standard set (oid_sets, command_sets) is Forward's own list, not repeated here. Only what a profile asks for is shown: what a device returned is in the model (NQE network.endpoints)", len(profiles), len(rows))}
	if cli {
		if ap, _, aerr := s.Client.Endpoints.ApprovedCLICommands(ctx); aerr != nil {
			limits = append(limits, "CLI profile commands run only if the organization approved them; the approved list could not be read: "+aerr.Error())
		} else if ap != nil {
			detail["approved_cli_commands"] = map[string]any{"count": len(ap.Commands), "custom_list_uploaded": ap.SignedAt != "" || ap.UploadedAt != ""}
			limits = append(limits, "CLI profile commands run only if the organization approved them (Forward's approved-command list, patterns not matched here): an unapproved command is skipped at collection")
		}
	}
	return limits
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// weekdays names Forward's day numbers (Sunday is 0).
func weekdays(d []int) []string {
	names := []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	out := make([]string, 0, len(d))
	for _, n := range d {
		if n >= 0 && n < 7 {
			out = append(out, names[n])
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// testOutcome names a connectivity test's result: Forward stores the collection-error name ("NONE" is success) and when it ran; none stored means it was never tested.
func testOutcome(errName string, at int64) string {
	switch {
	case errName == "" && at == 0:
		return "never tested"
	case errName == "" || errName == "NONE":
		return "ok"
	}
	return errName
}

// topDeviceCounts is the n largest groups of a count map, largest first (ties by name), as rows; the rest are folded into one "(other N groups)" row so the total still adds up.
func topDeviceCounts(m map[string]int, n int) []map[string]any {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Slice(names, func(i, j int) bool {
		if m[names[i]] != m[names[j]] {
			return m[names[i]] > m[names[j]]
		}
		return names[i] < names[j]
	})
	var out []map[string]any
	rest, restN := 0, 0
	for i, k := range names {
		if i < n {
			out = append(out, map[string]any{"name": k, "devices": m[k]})
		} else {
			rest += m[k]
			restN++
		}
	}
	if restN > 0 {
		out = append(out, map[string]any{"name": fmt.Sprintf("(other %d groups)", restN), "devices": rest})
	}
	return out
}
