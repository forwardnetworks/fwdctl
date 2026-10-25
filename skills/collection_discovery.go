package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// inspectCollectionDiscovery is view discovery: Forward's device discovery as it stands. It lists the discovery jobs, the devices they found that are not yet collection devices (grouped by type and vendor, with why each is not
// ready to be added), and the scheduled discoveries. It starts nothing: a scan touches the customer's network and is a separate, confirmed action.
func inspectCollectionDiscovery(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
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
	for _, k := range []string{"collector", "names_only", "offset", "wait_seconds", "data_file", "data_connector", "include_content", "device"} {
		if _, bad := present[k]; bad {
			return result.Result{}, fmt.Errorf("%w: %s belongs to another view of inspect-collection, not discovery", ErrInvalidInput, k)
		}
	}
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	found, _, err := s.Client.DeviceDiscovery.ListDiscovered(ctx, in.NetworkID, time.Time{})
	if err != nil {
		if r, ok := denialResult("inspect-collection", err, cx, "could not read the discovered devices"); ok {
			return r, nil
		}
		return result.NewUnknown("inspect-collection", "Unknown: Forward's discovered devices could not be read: "+err.Error(), cx, []string{"nothing was read; this is not proof nothing was discovered"}, result.Options{})
	}
	var limits []string
	have := map[string]bool{}
	if classic, cerr := s.ClassicDevices(ctx, in.NetworkID); cerr != nil {
		limits = append(limits, "the collection devices could not be read ("+cerr.Error()+"), so which discovered devices are already collected is not known")
	} else {
		for _, d := range classic {
			have[strings.ToLower(d.Name)] = true
		}
	}
	jobs := make([]map[string]any, 0, len(found.Jobs))
	for _, j := range found.Jobs {
		status := "finished"
		if j.EndedAt == "" {
			status = "running"
		}
		row := map[string]any{"id": j.ID, "type": j.Type, "collector_id": string(j.CollectorID), "status": status, "started_at": j.StartedAt, "ended_at": j.EndedAt,
			"queued": j.Counts.Queued, "discovered": j.Counts.Discovered, "inaccessible": j.Counts.Inaccessible}
		if j.Error != "" && j.Error != "NONE" {
			row["error"] = j.Error
		}
		if j.ScheduledDiscoveryID != "" {
			row["scheduled_discovery_id"] = j.ScheduledDiscoveryID
		}
		jobs = append(jobs, row)
	}
	byType, byVendor := map[string]int{}, map[string]int{}
	var notAdded []map[string]any
	for _, d := range found.Devices {
		typ := d.Type
		if typ == "" {
			typ = "unknown"
		}
		byType[typ]++
		if d.Vendor != "" {
			byVendor[d.Vendor]++
		}
		if have[strings.ToLower(d.Name)] {
			continue
		}
		var why []string
		if typ == "unknown" {
			why = append(why, "type unknown")
		}
		if d.CLICredentialID == "" && d.HTTPCredentialID == "" && d.SNMPCredentialID == "" {
			why = append(why, "no credential")
		}
		if d.Host == "" {
			why = append(why, "no host")
		}
		if len(why) == 0 {
			why = []string{"found, not added"}
		}
		row := map[string]any{"name": d.Name, "host": d.Host, "type": typ, "vendor": d.Vendor, "job_id": d.JobID, "why_not_added": why}
		if j := found.JobFor(d); j != nil {
			row["collector_id"] = string(j.CollectorID)
		}
		notAdded = append(notAdded, row)
	}
	sort.SliceStable(notAdded, func(i, j int) bool { return notAdded[i]["name"].(string) < notAdded[j]["name"].(string) })
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	shown, omitted := result.Cap(notAdded, limit, "discovered devices not yet added", "raise limit")
	var schedules []map[string]any
	if sch, _, serr := s.Client.DeviceDiscovery.ListScheduled(ctx, in.NetworkID); serr != nil {
		limits = append(limits, "the scheduled discoveries could not be read: "+serr.Error())
	} else {
		for _, x := range sch {
			typ := ""
			if g, gerr := fwd.Generic(x.Params); gerr == nil {
				if m, ok := g.(map[string]any); ok {
					typ = fmt.Sprint(m["type"])
				}
			}
			row := map[string]any{"id": x.ID, "name": x.Name, "enabled": x.Enabled, "type": typ, "runs": "after every collection"}
			if x.Schedule != nil {
				g, _ := fwd.Generic(x.Schedule)
				row["runs"] = g
			}
			schedules = append(schedules, row)
		}
	}
	limits = append(limits,
		"this reads discovery; it starts no scan. Starting a scan or enabling a schedule probes the network from a collector, and adding discovered devices changes what is collected",
		"why_not_added is what the discovered record lacks (a type Forward could not infer, a credential, a host); a device with none of those is simply found and not yet added",
		"a discovered device is matched to a collection device by name, case-insensitively")
	detail := map[string]any{"discovery": map[string]any{"jobs": jobs, "devices_found": len(found.Devices), "not_yet_added": len(notAdded), "by_type": byType, "by_vendor": byVendor, "not_added": shown, "schedules": schedules}}
	finding := fmt.Sprintf("%d discovery job(s); %d discovered device(s) not yet collection devices (of %d found); %d scheduled discovery(ies)", len(jobs), len(notAdded), len(found.Devices), len(schedules))
	return result.Build("inspect-collection", result.OK, finding, result.Deterministic, cx, result.Options{
		Limits: limits, Omitted: omitted, Evidence: []result.Evidence{result.NewEvidence(result.EvState, "deviceDiscovery", nil, detail, "")},
	})
}
