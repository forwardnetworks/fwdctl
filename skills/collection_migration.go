package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

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
	}
	ctls := make([]map[string]any, 0, len(byCtl))
	for c, n := range byCtl {
		ctls = append(ctls, map[string]any{"name": c.name, "address": c.address, "devices": n})
	}
	sort.Slice(ctls, func(i, j int) bool {
		if ctls[i]["devices"].(int) != ctls[j]["devices"].(int) {
			return ctls[i]["devices"].(int) > ctls[j]["devices"].(int)
		}
		return fmt.Sprint(ctls[i]["address"]) < fmt.Sprint(ctls[j]["address"])
	})
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	shown, omitted := result.Cap(ctls, limit, "controllers", "raise limit")
	limits := []string{
		"rule (Forward's UI): effective type = the configured type, else the type the last connectivity test discovered ('unknown' counts as absent); a device counts when that type is a CONTROLLER_MANAGED ACI (cisco_apic_api) or SD-WAN type or one of its related types; a cisco_ios_xe type counts only when the test reported a controller",
		fmt.Sprintf("%d of %d devices have no connectivity test result yet and could not be classified; a device never tested is not counted", noTest, len(statuses)),
		"counted devices are collected as classic devices but belong in a controller-managed setup (inspect-collection view config lists the setups; edit-source objects create them): this is a finding, not a collection failure",
	}
	mig := map[string]any{
		"by_category": byCat, "by_effective_type": byType, "controllers": shown, "devices_counted": counted, "devices_read": len(statuses), "devices_without_test_result": noTest,
	}
	if counted > 0 {
		if creds, _, cerr := s.Client.Credentials.ListCLI(ctx, in.NetworkID); cerr != nil {
			limits = append(limits, fmt.Sprintf("the CLI logins could not be read (%v), so no credential candidates are listed", cerr))
		} else {
			var cands []map[string]any
			for _, c := range creds {
				if c.AutoAssociate != nil && *c.AutoAssociate {
					cands = append(cands, map[string]any{"id": c.ID, "name": c.Name, "username": c.Username, "has_enable_password": c.PrivilegedModePasswordID != ""})
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
