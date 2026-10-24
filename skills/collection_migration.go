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

// sdwanVendors are the connection-type vendors Forward's UI treats as SD-WAN controllers (a CONTROLLER_MANAGED type of one of these, and the
// types it lists as related, are the devices the UI says need to be migrated to a controller-managed setup).
var sdwanVendors = map[string]bool{
	"128t": true, "128t_conductor": true, "cloud_genix": true, "cloud_genix_portal": true, "silver_peak_orchestrator": true,
	"silver_peak_edgeconnect": true, "versa_sase": true, "versa_switch": true, "viptela": true, "viptela_smart": true,
}

// inspectCollectionMigration reproduces the UI's "N devices need to be migrated" count: devices collected as classic devices whose
// effective type (the configured type, else the one the last connectivity test discovered) belongs to a controller-managed ACI or SD-WAN
// type. A device on a cisco_ios_xe type counts only when its test reported a controller (a managed edge); a standalone IOS-XE router is not a candidate.
func inspectCollectionMigration(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in struct {
		NetworkID string `json:"network_id"`
		Limit     int    `json:"limit"`
		View      string `json:"view"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if in.NetworkID == "" {
		return result.Result{}, fmt.Errorf("%w: network_id is required", ErrInvalidInput)
	}
	var present map[string]json.RawMessage
	_ = json.Unmarshal(raw, &present)
	for _, k := range []string{"collector", "names_only", "offset", "wait_seconds", "data_file", "data_connector", "include_content"} {
		if _, bad := present[k]; bad {
			return result.Result{}, fmt.Errorf("%w: %s belongs to another view of inspect-collection, not migration", ErrInvalidInput, k)
		}
	}
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	types, err := s.DeviceConnTypes(ctx)
	if err != nil {
		return result.NewUnknown("inspect-collection", "Unknown: Forward's device connection types could not be read, so no device can be classified: "+err.Error(), cx, []string{"nothing was counted; this is not a pass"}, result.Options{})
	}
	statuses, err := s.DeviceTestStatuses(ctx, in.NetworkID)
	if err != nil {
		return result.NewUnknown("inspect-collection", "Unknown: the network's classic devices with their connectivity tests could not be read: "+err.Error(), cx, []string{"nothing was counted; this is not a pass"}, result.Options{})
	}
	category := map[string]string{} // type id -> ACI | SD_WAN
	iosXE := map[string]bool{}      // type ids whose vendor is cisco_ios_xe
	for _, t := range types {
		if t.Vendor == "cisco_ios_xe" {
			iosXE[t.ID] = true
		}
	}
	for _, t := range types {
		if t.CollectionSourceType != "CONTROLLER_MANAGED" {
			continue
		}
		cat := ""
		switch {
		case t.ID == "cisco_apic_api":
			cat = "ACI"
		case sdwanVendors[t.Vendor]:
			cat = "SD_WAN"
		}
		if cat == "" {
			continue
		}
		for _, id := range append([]string{t.ID}, t.RelatedTypes...) {
			if _, taken := category[id]; !taken {
				category[id] = cat
			}
		}
	}
	classicByName := map[string]forward.ClassicDevice{}
	classicErr := ""
	if cl, _, cerr := s.Client.ClassicDevices.List(ctx, in.NetworkID); cerr != nil {
		classicErr = cerr.Error()
	} else {
		for _, c := range cl {
			classicByName[c.Name] = c
		}
	}
	controllerTypes := map[string]bool{} // type ids that are themselves a controller (a Director, vManage or APIC record), not a managed device
	for _, t := range types {
		if t.CollectionSourceType == "CONTROLLER_MANAGED" {
			controllerTypes[t.ID] = true
		}
	}
	var devRows []map[string]any
	byCat, byType := map[string]int{}, map[string]int{}
	type ctl struct{ name, address string }
	byCtl := map[ctl]int{}
	counted, noTest := 0, 0
	for _, d := range statuses {
		if !d.TestResultPresent {
			noTest++
			continue
		}
		eff := d.Type
		if eff == "" && d.DiscoveredType != "unknown" {
			eff = d.DiscoveredType
		}
		cat := category[eff]
		if cat == "" || (iosXE[eff] && len(d.ControllerInfos) == 0) {
			continue
		}
		counted++
		byCat[cat]++
		byType[eff]++
		for _, c := range d.ControllerInfos {
			byCtl[ctl{c.Name, c.IPAddress}]++
		}
		devRows = append(devRows, migrationDeviceRow(d, eff, cat, classicByName[d.Name]))
	}
	_ = byCtl
	vendorOf := map[string]string{}
	for _, t := range types {
		vendorOf[t.ID] = t.Vendor
	}
	ctls := migrationControllers(devRows, vendorOf, controllerTypes, category, classicByName, 25)
	sort.SliceStable(devRows, func(i, j int) bool { return fmt.Sprint(devRows[i]["name"]) < fmt.Sprint(devRows[j]["name"]) })
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	shown, omitted := result.Cap(ctls, limit, "controllers", "raise limit")
	devShown, devOmitted := result.Cap(devRows, max(limit, 50), "devices", "raise limit")
	omitted = append(omitted, devOmitted...)
	limits := []string{
		"rule (Forward's UI): effective type = the configured type, else the type the last connectivity test discovered ('unknown' counts as absent); a device counts when that type is a CONTROLLER_MANAGED ACI (cisco_apic_api) or SD-WAN type or one of its related types; a cisco_ios_xe type counts only when the test reported a controller",
		fmt.Sprintf("%d of %d devices have no connectivity test result yet and could not be classified; a device never tested is not counted", noTest, len(statuses)),
		"field-reported symptom (not checked here): an SSH-collected SD-WAN device whose only collected file is snapshot_time.txt (inspect-device-files list) is one that needs its controller; the controller that already collects its peers has a record with the login and jump server (classic_record)",
		"counted devices are collected as classic devices but belong in a controller-managed setup (inspect-collection view config lists the setups; edit-source objects create them): this is a finding, not a collection failure",
	}
	if classicErr != "" {
		limits = append(limits, "the classic device records could not be read ("+classicErr+"), so the devices carry no host, login, jump server or collector and no controller record was matched")
	}
	mig := map[string]any{
		"by_category": byCat, "by_effective_type": byType, "controllers": shown, "devices": devShown, "devices_counted": counted, "devices_read": len(statuses), "devices_without_test_result": noTest,
	}
	if counted > 0 {
		if creds, _, cerr := s.Client.Credentials.ListCLI(ctx, in.NetworkID); cerr != nil {
			limits = append(limits, fmt.Sprintf("the CLI logins could not be read (%v), so no credential candidates are listed", cerr))
		} else {
			vendorTokens := map[string]bool{}
			for typ := range byType {
				if v := strings.Split(vendorOf[typ], "_")[0]; v != "" {
					vendorTokens[v] = true
				}
			}
			var cands []map[string]any
			for _, c := range creds {
				if c.AutoAssociate != nil && *c.AutoAssociate {
					cand := map[string]any{"id": c.ID, "name": c.Name, "username": c.Username, "has_enable_password": c.PrivilegedModePasswordID != ""}
					var likely []string
					for v := range vendorTokens {
						if strings.Contains(strings.ToLower(c.Name), v) {
							likely = append(likely, v)
						}
					}
					if len(likely) > 0 {
						sort.Strings(likely)
						cand["name_suggests_vendor"] = likely
					}
					cands = append(cands, cand)
				}
			}
			mig["credential_candidates"] = cands
			limits = append(limits, "credential_candidates are the CLI logins set to auto-associate, which Forward may have tried on these devices; the connectivity test result does not record which login worked, so the working one cannot be inferred. A migrated guest inherits nothing from the controller: give each guest a CLI credential and run a connectivity test on a small pilot before moving the rest")
		}
	}
	detail := map[string]any{"migration": mig}
	finding := fmt.Sprintf("%d of %d classic devices need to be migrated to a controller-managed setup (ACI %d, SD-WAN %d)", counted, len(statuses), byCat["ACI"], byCat["SD_WAN"])
	status := result.Failed
	if counted == 0 {
		status = result.OK
		finding = fmt.Sprintf("No classic device needs to be migrated: none of the %d devices read (%d without a test result) has an ACI or SD-WAN controller-managed type", len(statuses), noTest)
	}
	return result.Build("inspect-collection", status, finding, result.Deterministic, cx, result.Options{
		Limits: limits, Omitted: omitted, Evidence: []result.Evidence{result.NewEvidence(result.EvState, "classicDevices", nil, detail, "")},
	})
}

// migrationDeviceRow is one counted device with what a migrate needs from its classic record: host, the CLI login, jump server and collector it uses now.
func migrationDeviceRow(d forward.SourceTestStatus, eff, cat string, c forward.ClassicDevice) map[string]any {
	ctls := make([]map[string]any, 0, len(d.ControllerInfos))
	for _, ci := range d.ControllerInfos {
		ctls = append(ctls, map[string]any{"name": ci.Name, "address": ci.IPAddress})
	}
	row := map[string]any{"name": d.Name, "effective_type": eff, "category": cat, "controllers": ctls}
	if c.Name != "" {
		row["host"] = c.Host
		row["cli_credential_id"] = nilIfEmpty(string(c.CLICredentialID))
		row["http_credential_id"] = nilIfEmpty(string(c.HTTPCredentialID))
		row["snmp_credential_id"] = nilIfEmpty(string(c.SNMPCredentialID))
		if opts := classicOptions(c); len(opts) > 0 {
			row["options"] = opts
		}
		row["cli_credential2_id"] = nilIfEmpty(string(c.CLICredential2ID))
		row["cli_credential3_id"] = nilIfEmpty(string(c.CLICredential3ID))
		if c.Port != nil {
			row["port"] = *c.Port
		}
		row["jump_server_id"] = nilIfEmpty(string(c.JumpServerID))
		row["collector_id"] = nilIfEmpty(string(c.CollectorID))
	}
	return row
}

// migrationControllerKind names the controller product from the category and the vendor of the devices' effective types.
func migrationControllerKind(cat string, vendors map[string]bool) string {
	switch {
	case cat == "ACI":
		return "Cisco ACI (APIC)"
	case hasVendorPrefix(vendors, "versa"):
		return "Versa Director"
	case vendors["viptela"] || vendors["cisco_ios_xe"] || vendors["cisco"]:
		return "Cisco SD-WAN (vManage)"
	case vendors["silver_peak_orchestrator"] || vendors["silver_peak_edgeconnect"]:
		return "Silver Peak Orchestrator"
	}
	return "SD-WAN controller (product not told by the types)"
}

// migrationControllers groups the counted devices by the controller their connectivity test reported, and for each says what kind it is, which devices point at it, whether a classic record has that address
// (a controller record already carrying a login and jump server), and a ready migrate input for a pilot. Devices whose test reported no controller (Versa edges) are linked to the network's one controller
// record of their category, when there is exactly one, and the entry says that is how they were linked.
func migrationControllers(rows []map[string]any, vendorOf map[string]string, controllerTypes map[string]bool, category map[string]string, classicByName map[string]forward.ClassicDevice, pilot int) []map[string]any {
	type key struct{ name, address string }
	order := []key{}
	group := map[key][]map[string]any{}
	linkedBy := map[key]string{}
	var loose []map[string]any
	for _, r := range rows {
		cs := r["controllers"].([]map[string]any)
		if controllerTypes[fmt.Sprint(r["effective_type"])] {
			continue // a controller record itself, not a device it manages
		}
		if len(cs) == 0 {
			loose = append(loose, r)
			continue
		}
		for _, c := range cs {
			k := key{fmt.Sprint(c["name"]), fmt.Sprint(c["address"])}
			if _, ok := group[k]; !ok {
				order = append(order, k)
			}
			group[k] = append(group[k], r)
		}
	}
	// devices with no reported controller: the one controller record of their category
	byCat := map[string][]map[string]any{}
	for _, r := range loose {
		byCat[fmt.Sprint(r["category"])] = append(byCat[fmt.Sprint(r["category"])], r)
	}
	for cat, devs := range byCat {
		var recs []forward.ClassicDevice
		for _, c := range classicByName {
			if controllerTypes[c.Type] && category[c.Type] == cat {
				recs = append(recs, c)
			}
		}
		if len(recs) != 1 {
			continue
		}
		k := key{recs[0].Name, recs[0].Host}
		if _, ok := group[k]; !ok {
			order = append(order, k)
		}
		group[k] = append(group[k], devs...)
		linkedBy[k] = "no connectivity test reported a controller for these devices; they are linked to the network's only " + cat + " controller record"
	}
	sort.SliceStable(order, func(i, j int) bool {
		if len(group[order[i]]) != len(group[order[j]]) {
			return len(group[order[i]]) > len(group[order[j]])
		}
		return order[i].address < order[j].address
	})
	var out []map[string]any
	for _, k := range order {
		devs := group[k]
		vendors := map[string]bool{}
		cat := ""
		for _, d := range devs {
			vendors[vendorOf[fmt.Sprint(d["effective_type"])]] = true
			cat = fmt.Sprint(d["category"])
		}
		names := make([]string, 0, len(devs))
		for _, d := range devs {
			names = append(names, fmt.Sprint(d["name"]))
		}
		sort.Strings(names)
		foundBy := "the devices' last connectivity test (controllerInfos), not from a configuration record"
		if s, ok := linkedBy[k]; ok {
			foundBy = s
		}
		entry := map[string]any{"name": nilIfEmpty(k.name), "address": nilIfEmpty(k.address), "devices": len(devs), "controller_kind": migrationControllerKind(cat, vendors), "found_by": foundBy}
		shown, _ := result.CapRow(names, 15)
		entry["device_names"] = shown
		var rec *forward.ClassicDevice
		for _, c := range classicByName {
			c := c
			if k.address != "" && c.Host == k.address || (controllerTypes[c.Type] && k.address != "" && strings.Contains(c.Host, k.address)) {
				rec = &c
				break
			}
		}
		controller := map[string]any{"name": firstNonEmpty(k.name, "controller"), "host": k.address}
		if rec != nil {
			entry["classic_record"] = map[string]any{"name": rec.Name, "type": rec.Type, "host": rec.Host, "cli_credential_id": nilIfEmpty(string(rec.CLICredentialID)), "http_credential_id": nilIfEmpty(string(rec.HTTPCredentialID)),
				"snmp_credential_id": nilIfEmpty(string(rec.SNMPCredentialID)), "jump_server_id": nilIfEmpty(string(rec.JumpServerID)), "collector_id": nilIfEmpty(string(rec.CollectorID))}
			entry["address_confirmed_by_a_record"] = true
			controller["name"], controller["type"] = rec.Name, rec.Type
			for k, v := range classicOptions(*rec) {
				controller[k] = v
			}
			for field, v := range map[string]string{"cliCredentialId": string(rec.CLICredentialID), "cliCredential2Id": string(rec.CLICredential2ID), "cliCredential3Id": string(rec.CLICredential3ID), "httpCredentialId": string(rec.HTTPCredentialID), "snmpCredentialId": string(rec.SNMPCredentialID), "jumpServerId": string(rec.JumpServerID)} {
				if v != "" {
					controller[field] = v
				}
			}
			if rec.Port != nil {
				controller["port"] = *rec.Port
			}
		} else {
			entry["address_confirmed_by_a_record"] = false
		}
		// the logins the siblings of a device (same /24) already use, for the devices with none recorded
		subnetLogins := map[string]map[string]int{}
		for _, d := range devs {
			if v, ok := d["cli_credential_id"].(string); ok && v != "" {
				p := subnet24(fmt.Sprint(d["host"]))
				if subnetLogins[p] == nil {
					subnetLogins[p] = map[string]int{}
				}
				subnetLogins[p][v]++
			}
		}
		managed := []map[string]any{}
		hints := map[string]any{}
		for _, d := range devs {
			if len(managed) >= pilot {
				break
			}
			m := map[string]any{"name": d["name"], "type": d["effective_type"]}
			if h, ok := d["host"]; ok {
				m["host"] = h
			}
			if v, ok := d["cli_credential_id"].(string); ok && v != "" {
				m["cliCredentialId"] = v
			} else if sib := subnetLogins[subnet24(fmt.Sprint(d["host"]))]; len(sib) > 0 {
				hints[fmt.Sprint(d["name"])] = sib // credential id -> how many same-subnet devices use it
			}
			if opts, ok := d["options"].(map[string]any); ok {
				for k, v := range opts {
					m[k] = v
				}
			}
			for field, key := range map[string]string{"jumpServerId": "jump_server_id", "cliCredential2Id": "cli_credential2_id", "cliCredential3Id": "cli_credential3_id", "httpCredentialId": "http_credential_id", "snmpCredentialId": "snmp_credential_id", "port": "port"} {
				if v := d[key]; v != nil {
					m[field] = v
				}
			}
			managed = append(managed, m)
		}
		sm := map[string]any{"object": "controller_setup", "action": "migrate",
			"definition": map[string]any{"name": "<choose the setup name>", "controllers": []map[string]any{controller}, "managedDevices": managed},
			"warning":    "migrate DELETES each classic device it names (its settings, test results and tags) in the same call, and needs confirm equal to the setup name; there is no undo command: recreate the classic devices from the before list the dry run prints. A migrate carries nothing over from the deleted records, so this suggestion restates each one's logins (CLI, HTTP, SNMP), jump server and port; the collection options on a classic record (note, BGP advertisement collection, SNMP options, pagination, prompts, thresholds) are restated too, read from the record; test results and tags cannot be carried A device with no cliCredentialId is refused: nothing is inherited from the controller or the classic record",
			"note":       fmt.Sprintf("a pilot of %d of %d devices; run the dry run, then a connectivity test, then the rest", len(managed), len(devs))}
		if len(hints) > 0 {
			sm["credential_hints"] = map[string]any{"devices_without_a_recorded_login": hints, "meaning": "for each device with no login recorded, the login ids its same-/24 siblings use and how many do; Forward does not record which login works, so a pilot connectivity test decides. The org's auto-associate logins are in credential_candidates"}
		}
		entry["suggested_migrate"] = sm
		out = append(out, entry)
	}
	return out
}

// subnet24 is the /24 (or the whole text when it is not an IPv4 address) a host sits in, to find a device's neighbours.
func subnet24(host string) string {
	if p := strings.Split(host, "."); len(p) == 4 {
		return p[0] + "." + p[1] + "." + p[2]
	}
	return host
}

func hasVendorPrefix(vendors map[string]bool, prefix string) bool {
	for v := range vendors {
		if strings.HasPrefix(v, prefix) {
			return true
		}
	}
	return false
}

// migrationOptionKeys are the collection options a classic record holds that a controller or managed entry takes under the same wire names (SDK v0.7.0): a migrate restates them or loses them.
var migrationOptionKeys = []string{"note", "collect", "enableSnmpCollection", "collectBgpAdvertisements", "bgpTableType", "bgpPeerType", "bgpAndBmpFailureHandling", "bgpSubnetsToCollect", "bmpStationHost",
	"disableIpv6Collection", "fullCollectionLog", "paginationMode", "terminalWidth", "largeRtt", "minBytesReadPerSec", "maxConnectionsPerMin", "useFileTransferForLargeOutput", "prompt", "promptResponse",
	"highCpuUsage", "highMemoryUsage", "highInputUtilization", "highOutputUtilization", "mediumCpuUsage", "mediumMemoryUsage", "mediumInputUtilization", "mediumOutputUtilization"}

// classicOptions reads those options off a classic record's wire object (Raw), so a value the SDK does not type is still restated.
func classicOptions(c forward.ClassicDevice) map[string]any {
	out := map[string]any{}
	for _, k := range migrationOptionKeys {
		raw, ok := c.Raw[k]
		if !ok || string(raw) == "null" {
			continue
		}
		var v any
		if json.Unmarshal(raw, &v) == nil {
			out[k] = v
		}
	}
	return out
}
