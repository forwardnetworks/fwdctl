package skills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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
	// IncludeContent also returns the first 2000 characters of that data file's stored content. Off by default: an uploaded file can hold anything, secrets included.
	IncludeContent bool `json:"include_content"`
	// DataConnector names one of the network's data connectors (see detail.data_connectors) to also read its endpoints, status and last test result.
	DataConnector string `json:"data_connector"`
	// Collector limits the devices to those pinned to one collector: its id (101291 or C101291 are the same) or "none" for the devices with no pin (they run on the network's default collector).
	Collector string `json:"collector"`
	// NamesOnly returns the devices as compact [name, collector id] pairs, in larger pages, so a whole network is read in a few calls.
	NamesOnly bool `json:"names_only"`
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
	var omitted []result.Omission
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
			if c := string(d.CollectorID); c != "" {
				row["collector_id"] = c
			}
			rows = append(rows, row)
		}
		// Forward returns the device list in no fixed order, so a page is cut from the list sorted by name: two reads of the same list give the same pages
		sort.SliceStable(rows, func(i, j int) bool { return rows[i]["name"].(string) < rows[j]["name"].(string) })
		names := make([]string, len(rows))
		for i, r := range rows {
			names[i] = r["name"].(string)
		}
		sum := sha256.Sum256([]byte(strings.Join(names, "\n")))
		detail["device_list_digest"] = hex.EncodeToString(sum[:8])
		limits = append(limits, "each page re-reads the whole device list and cuts it from the list sorted by name; device_list_digest is a digest of every device name in that read, so pages with the same digest and device_count came from the same list. If they differ, devices were added or removed between the reads")
		all := len(rows)
		if in.Collector != "" {
			want := strings.ToLower(strings.TrimSpace(in.Collector))
			kept := rows[:0:0]
			for _, r := range rows {
				c, _ := r["collector_id"].(string)
				if (want == "none" && c == "") || (want != "none" && c != "" && collectorKey(c) == collectorKey(want)) {
					kept = append(kept, r)
				}
			}
			rows = kept
			detail["devices_matching"] = len(rows)
			detail["devices_in_list"] = all
		}
		var win any
		var wl []result.Omission
		ok := true
		if in.NamesOnly {
			pairs := make([][]string, 0, len(rows))
			for _, r := range rows {
				c, _ := r["collector_id"].(string)
				pairs = append(pairs, []string{r["name"].(string), c})
			}
			var page [][]string
			page, wl, ok = window(pairs, in.Limit, in.Offset, 5000, 20000, "devices")
			win = page
			limits = append(limits, "names_only lists [name, collector id] pairs sorted by name (an empty collector id means no pin: the network's default collector); a page is up to 20,000 devices (default 5,000), offset continues")
		} else {
			var page []map[string]any
			page, wl, ok = window(rows, in.Limit, in.Offset, 50, 500, "devices")
			win = page
		}
		if ok {
			detail["devices"] = win
			omitted = append(omitted, wl...)
		} else {
			detail["devices"] = []map[string]any{}
			if len(rows) > 0 {
				limits = append(limits, fmt.Sprintf("offset %d is past the end of the %d devices", in.Offset, len(rows)))
			}
		}
		detail["device_count"] = len(rows)
		// devices pinned to a collector, by collector id; devices without one use the network's default collector
		byCollector := map[string]int{}
		for _, d := range devs {
			if c := string(d.CollectorID); c != "" {
				byCollector[c]++
			}
		}
		if len(byCollector) > 0 {
			detail["devices_by_collector"] = byCollector
		}
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
			omitted = append(omitted, wl...)
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
		capSeen, membersSeen := false, false
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
			if m := memberAccounts(c.AssumeRoleInfos); m != nil {
				row["member_accounts"] = m
				membersSeen = true
			}
			if c.HasCAPCredential() {
				row["cap_credential"] = true
				row["c2s_accounts"] = c2sAccounts(c.AWSC2SAccountInfos)
				capSeen = true
				// the portal's URL and whether a CA is set, never the client certificate; a login without VIEW_DEVICE_CREDENTIALS gets a limit instead
				if id := c.AWSCapCredentialID; id != "" {
					if cred, _, cerr := s.Client.CloudAccounts.GetAWSCapCredential(ctx, in.NetworkID, id); cerr != nil {
						if op, ok := forward.MissingPermission(cerr); ok {
							limits = append(limits, fmt.Sprintf("the access portal URL of %q could not be read: this login lacks %s", c.Name, op))
						} else {
							limits = append(limits, fmt.Sprintf("the access portal URL of %q could not be read: %v", c.Name, cerr))
						}
					} else {
						row["cap_server_url"] = cred.BaseCapServerURL
						row["cap_ca_set"] = cred.HasCertificateAuthority
						limits = append(limits, capCertificateFindings(c.Name, cred, row, time.Now())...)
					}
				}
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
		if capSeen {
			limits = append(limits, "cap_credential marks an AWS setup collected through a C2S Access Portal credential: it collects the accounts in c2s_accounts (an empty list does not mean all: Forward's collection step requires at least one account, so a setup with none fails to collect; the setup test and discovery treat empty as no filter); the enabled flags in member_accounts are not used for it (from Forward's source, not confirmed on a live setup), and the credential's client certificate is never shown (the portal URL and whether a CA is set are)")
		}
		if membersSeen {
			limits = append(limits, "member_accounts (a setup that discovers accounts, AWS assume-role): each account's enabled flag and Forward's last error for it (accounts that are on and erroring come first, then disabled ones, then the rest, so a row cap hides none of them). Forward keeps the last error message, so an error on a disabled account may be old, except on a cap_credential setup, where the enabled flags are not used and an account shown as off may still be requested. The role ARN and external id are not shown. Change the flags with edit-source object cloud_account action update")
		}
		limits = append(limits, "cloud_setups lists each cloud collection source with its configured regions and the result of its last connectivity TEST (not of the last collection: Forward keeps no per-setup collection outcome here). The regions decide which zones Forward keeps from the cloud's aggregated lists, so a missing instance can be a region that is not listed; collector errors during a collection are in investigate-collection-failure view exceptions, and inspect-inventory kind cloud_accounts shows whether each account was collected. Credentials are never read or shown")
	}
	if setups, _, err := s.Client.ControllerManagedSetups.ListWithTestResults(ctx, in.NetworkID); err != nil {
		limits = append(limits, "the controller-managed setups could not be read: "+err.Error())
	} else if len(setups) > 0 {
		read++
		total += len(setups)
		rows := make([]map[string]any, 0, len(setups))
		for _, st := range setups {
			var devs []map[string]any
			tests := map[string]int{}
			add := func(name, role, host, cred string, collect *bool, tr *forward.DeviceTestResult) {
				d := map[string]any{"name": name, "role": role}
				if host != "" {
					d["host"] = host
				}
				if role == "managed" {
					d["cli_credential_set"] = cred != ""
					d["collect"] = collect == nil || *collect
				}
				outcome, at := setupTestOutcome(tr)
				d["test"] = outcome
				if at != "" {
					d["tested_at"] = at
				}
				if tr != nil {
					setupTestDetails(d, tr)
				}
				tests[strings.SplitN(outcome, ":", 2)[0]]++
				devs = append(devs, d)
			}
			for _, c := range st.Controllers {
				add(c.Name, "controller", c.Host, c.CLICredentialID, nil, c.TestResult)
			}
			for _, m := range st.ManagedDevices {
				add(m.Name, "managed", m.Host, m.CLICredentialID, m.Collect, m.TestResult)
			}
			shown, nDevs := result.CapRow(devs, 100)
			row := map[string]any{"name": st.Name, "controllers": len(st.Controllers), "managed_devices": len(st.ManagedDevices), "tests": tests, "devices": shown, "devices_total": nDevs}
			if c := string(st.CollectorID); c != "" {
				row["collector_id"] = c
			}
			rows = append(rows, row)
		}
		detail["controller_setups"] = rows
		limits = append(limits, "controller_setups lists each controller-managed setup with its controllers and managed devices and each device's last saved connectivity test (a result outlives the test that wrote it, so right after a test is queued you may still see the previous one: compare tested_at with when you queued it). A device with no error and no time is not a pass. The collector is the setup's, one for all its devices")
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
			// who made and last changed the file, and when: how stale a file a producer is meant to refresh is
			for k, v := range map[string]string{"created_at": f.CreatedAt, "created_by": f.CreatedBy, "updated_at": f.UpdatedAt, "updated_by": f.UpdatedBy, "uploaded_at": f.UploadedAt, "uploaded_by": f.UploadedBy} {
				if v != "" {
					row[k] = v
				}
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
				if !in.IncludeContent {
					preview["content_preview"] = "not shown: give include_content:true to read the first 2000 characters of the file (an uploaded file can hold secrets)"
				} else if len(inf.Content) > 2000 {
					preview["content_preview"] = inf.Content[:2000] + "..."
					limits = append(limits, fmt.Sprintf("data_file %q: the content preview is truncated at 2000 of %d characters; it is the file as uploaded, unredacted. To read every row: foreach r in network.extensions.%s.value select r (fwdctl nqe run --network %s --file q.nqe; the file must be attached to the network and a snapshot collected since)", in.DataFile, len(inf.Content), nqeNameOf(dfs, in.DataFile), in.NetworkID))
				} else {
					preview["content_preview"] = inf.Content
					limits = append(limits, fmt.Sprintf("data_file %q: the content preview is the file as uploaded, unredacted", in.DataFile))
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
	return result.Build(collectionConfigName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, Omitted: omitted,
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
	var assessErrs []string
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
			row["name_detector_command"] = nilIfEmpty(p.NameDetectorCommand)
			var cmds []string
			cmds = append(cmds, p.CustomCommands...)
			for _, c := range []string{p.DetectorCommand, p.NameDetectorCommand} {
				if c != "" {
					cmds = append(cmds, c)
				}
			}
			if a, _, aerr := s.Client.Endpoints.AssessCLICommands(ctx, cmds); aerr != nil {
				row["collects"] = "unknown"
				assessErrs = append(assessErrs, p.Name+": "+aerr.Error())
			} else {
				row["collects"], row["unapproved_commands"] = len(a.Unapproved) == 0, a.Unapproved
			}
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
			limits = append(limits, "CLI profile commands run only if the organization approved them (Forward's approved-command list, patterns not matched here): a profile is collected only if ALL its commands (custom, detector, name detector) match the approved patterns; if any one fails, the whole endpoint is excluded from collection. The signed-upload route needs a Forward-signed file, so a custom list cannot be self-authored")
		}
	}
	if len(assessErrs) > 0 {
		limits = append(limits, "whether a CLI profile collects is unknown where Forward's approved-command check was refused or failed (it needs the manage-endpoint-profiles permission even though it changes nothing): "+strings.Join(assessErrs, "; "))
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

const maxMemberAccountRows = 25

// memberAccounts summarises the member accounts a cloud setup discovered (AWS assume-role): how many are on, how many are on and carry an error, and each account's
// id, name, enabled flag and Forward's last error (capped). Nil when the setup has none. The role ARN and external id are left out on purpose.
func memberAccounts(infos []forward.AWSAssumeRoleInfo) map[string]any {
	if len(infos) == 0 {
		return nil
	}
	enabled, withError := 0, 0
	for _, x := range infos {
		if x.Enabled {
			enabled++
			if x.ErrorMsg != "" {
				withError++
			}
		}
	}
	// the accounts worth a look come first, so the row cap never hides them: enabled with an error, then disabled, then the rest
	rank := func(x forward.AWSAssumeRoleInfo) int {
		switch {
		case x.Enabled && x.ErrorMsg != "":
			return 0
		case !x.Enabled:
			return 1
		}
		return 2
	}
	sorted := append([]forward.AWSAssumeRoleInfo(nil), infos...)
	sort.SliceStable(sorted, func(i, j int) bool { return rank(sorted[i]) < rank(sorted[j]) })
	shown, total := result.CapRow(sorted, maxMemberAccountRows)
	rows := make([]map[string]any, 0, len(shown))
	for _, x := range shown {
		row := map[string]any{"account_id": x.AccountID, "enabled": x.Enabled}
		if x.AccountName != "" {
			row["name"] = x.AccountName
		}
		if x.ErrorMsg != "" {
			row["error"] = oneLineNote(x.ErrorMsg)
		}
		rows = append(rows, row)
	}
	return map[string]any{"total": total, "enabled": enabled, "disabled": total - enabled, "enabled_with_error": withError, "rows": rows, "rows_shown": len(rows)}
}

// c2sAccounts is a C2S Access Portal setup's account list as the answer shows it: how many accounts it holds and (capped) their ids and roles. An empty list means Forward
// is rejected by Forward's collection step (source reading), so it says collection fails.
func c2sAccounts(accounts []forward.AWSC2SAccountInfo) map[string]any {
	if len(accounts) == 0 {
		return map[string]any{"count": 0, "note": "empty: Forward's collection step rejects an empty list for a CAP setup (read in its source, not run live), so AWS collection for this setup is expected to fail with an incomplete snapshot; the setup test and account discovery treat empty as no filter and request credentials for every portal account"}
	}
	shown, total := result.CapRow(accounts, maxMemberAccountRows)
	rows := make([]map[string]any, 0, len(shown))
	for _, a := range shown {
		rows = append(rows, map[string]any{"account_id": a.AccountID, "role": a.AccountRole})
	}
	return map[string]any{"count": total, "rows": rows, "rows_shown": len(rows)}
}

// nqeNameOf is the NQE name of the data file with the given name, for the recipe that reads all its rows.
func nqeNameOf(files []forward.DataFile, name string) string {
	for _, f := range files {
		if f.Name == name {
			return f.NQEName
		}
	}
	return "<nqe_name>"
}

// capCertificateFindings shows the CAP setup's CA expiry on its row and says what an expired or soon-expiring CA does: the portal rejects every account at once, which is how a whole AWS collection drops. The
// client certificate's expiry cannot be read (a .p12 is encrypted under a password Forward never returns), and that is said rather than implied healthy.
func capCertificateFindings(name string, cred *forward.AWSCapCredential, row map[string]any, now time.Time) []string {
	var out []string
	row["cap_ca_count"] = cred.CertificateAuthorityCount
	switch {
	case cred.HasCertificateAuthority && cred.CertificateAuthorityCount == 0:
		out = append(out, fmt.Sprintf("the C2S Access Portal CA of %q is stored but none of it parses as a PEM certificate Forward can load, so the portal's certificate cannot be verified", name))
	case !cred.CertificateAuthorityNotAfter.IsZero():
		days := int(cred.CertificateAuthorityNotAfter.Sub(now).Hours() / 24)
		row["cap_ca_not_after"], row["cap_ca_days_left"] = cred.CertificateAuthorityNotAfter.UTC().Format(time.RFC3339), days
		switch {
		case days < 0:
			out = append(out, fmt.Sprintf("the C2S Access Portal CA of %q EXPIRED %d day(s) ago (%s): Forward cannot verify the portal, so every account of the setup fails at once and the whole AWS collection drops. Renew the CA in the Forward UI, then run the setup test", name, -days, cred.CertificateAuthorityNotAfter.UTC().Format("2006-01-02")))
		case days <= 30:
			out = append(out, fmt.Sprintf("the C2S Access Portal CA of %q expires in %d day(s) (%s): after that every account of the setup fails at once. Renew it in the Forward UI before then", name, days, cred.CertificateAuthorityNotAfter.UTC().Format("2006-01-02")))
		}
	}
	out = append(out, fmt.Sprintf("the client certificate of %q has an expiry that cannot be read (%s): a .p12 is encrypted under a password Forward does not return. The portal's own server certificate is not dated by Forward either; Forward documents no collection error for an expired portal server certificate; the log line may carry a certificate error such as CERTIFICATE_CHECK_FAILED, which investigate-collection-failure view cloud recognises by pattern, a heuristic and not a documented rule", name, orNone(cred.ClientCertificateNotAfterReason)))
	return out
}

// setupTestOutcome says in words what a device's last saved connectivity test shows, and when it ended.
func setupTestOutcome(tr *forward.DeviceTestResult) (string, string) {
	switch {
	case tr == nil:
		return "never tested", ""
	case tr.Error != "":
		o := "failed: " + tr.Error
		if tr.ErrorPhase != "" {
			o += " (" + tr.ErrorPhase + ")"
		}
		return o, tr.EndTime
	case tr.EndTime == "":
		return "no error recorded but no end time: not a pass", ""
	}
	return "no error recorded", tr.EndTime
}

// setupTestDetails adds what a device's last saved test carries beyond its outcome: when it started, the addresses reached, the commands the account may not run, the credential the test found working and, for a
// controller, the devices its query phase reported. Forward does not return the guest list its SETUP phase used, which is what HOST_NOT_REPORTING_ANY_GUEST is raised from.
func setupTestDetails(d map[string]any, tr *forward.DeviceTestResult) {
	if tr.StartTime != "" {
		d["started_at"] = tr.StartTime
	}
	if len(tr.HostIPs) > 0 {
		d["host_ips"] = tr.HostIPs
	}
	if len(tr.UnauthorizedCommands) > 0 {
		cmds, n := result.CapRow(tr.UnauthorizedCommands, 10)
		d["unauthorized_commands"], d["unauthorized_commands_total"] = cmds, n
	}
	if tr.DiscoveredCLICredentialID != "" {
		d["discovered_cli_credential"] = tr.DiscoveredCLICredentialID
	}
	if len(tr.ManagedDevices) > 0 {
		rep := make([]map[string]any, 0, len(tr.ManagedDevices))
		for _, m := range tr.ManagedDevices {
			rep = append(rep, map[string]any{"name": m.Name, "address": m.IPAddress})
		}
		shown, n := result.CapRow(rep, 10)
		d["managed_devices_reported"], d["managed_devices_reported_total"] = shown, n
	}
	if tr.Error == "HOST_NOT_REPORTING_ANY_GUEST" {
		d["meaning"] = "the controller's test found no guests (a setup-phase result, not a login failure); Forward's API does not return the guest list that phase used, so managed_devices_reported is only what the query phase listed"
	}
}
