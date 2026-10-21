package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const maxNativeDiffDevices = 25

// nativeDiffs reads Forward's own diff tables for two snapshots, as the GUI's Compare shows them: ARP, MAC and NAT (a count and the devices that changed with their per-type counts), L3 routing (the summary and the changed devices, and with device that device's
// routing diff), the vulnerability diff and the routing-loop diff. Each read stands alone: one that cannot be read (a permission, a licence facet, an organization property) is named in the limits and the others still count. changed lists the tables that differ.
func nativeDiffs(ctx context.Context, s *fwd.Session, before, after, device string) (detail map[string]any, limits []string, changed []string) {
	detail = map[string]any{}
	d := s.Client.Diffs
	unread := func(what, needs string, err error) {
		limits = append(limits, fmt.Sprintf("the %s diff could not be read (%v); it needs %s", what, err, needs))
	}
	type family struct {
		key, label, needs string
		kind              forward.DiffKind
		list              func() ([]forward.DeviceDiffSummary, error)
	}
	families := []family{
		{"arp", "ARP", "VIEW_ARP_DIFFS", "arp", func() ([]forward.DeviceDiffSummary, error) { l, _, e := d.ARPDevices(ctx, before, after); return l, e }},
		{"mac", "MAC", "VIEW_MAC_DIFFS", "mac", func() ([]forward.DeviceDiffSummary, error) { l, _, e := d.MACDevices(ctx, before, after); return l, e }},
		{"nat", "NAT", "VIEW_NAT_DIFFS", "nat", func() ([]forward.DeviceDiffSummary, error) { l, _, e := d.NATDevices(ctx, before, after); return l, e }},
	}
	for _, f := range families {
		c, _, err := d.Count(ctx, before, after, f.kind)
		if err != nil {
			unread(f.label, f.needs, err)
			continue
		}
		row := map[string]any{"count": c.Count, "complete": c.Complete}
		if c.Count > 0 {
			changed = append(changed, f.label)
			if devs, lerr := f.list(); lerr != nil {
				unread(f.label+" device list", f.needs, lerr)
			} else {
				row["devices"] = diffDevices(devs)
			}
		}
		if !c.Complete {
			limits = append(limits, "the "+f.label+" diff count is not complete, so it may be larger")
		}
		detail[f.key] = row
	}
	l3 := map[string]any{}
	if devs, _, err := d.L3Devices(ctx, before, after); err != nil {
		unread("L3 routing", "VIEW_ROUTE_DIFFS", err)
	} else {
		l3["devices"] = diffDevices(devs)
		l3["devices_changed"] = len(devs)
		if len(devs) > 0 {
			changed = append(changed, "L3 routing (Forward's route diff)")
		}
		if sum, _, serr := d.L3Summary(ctx, before, after); serr == nil {
			l3["summary"] = json.RawMessage(sum)
		}
		if device != "" {
			if dd, _, derr := d.DeviceL3(ctx, before, after, device); derr != nil {
				limits = append(limits, fmt.Sprintf("the L3 routing diff of %s could not be read (%v); it needs VIEW_ROUTE_DIFFS and a device you may see", device, derr))
			} else {
				l3["device_detail"] = map[string]any{"device": device, "diff": json.RawMessage(dd)}
			}
		}
		limits = append(limits, "Forward computes and caches the L3 routing diff on its first read, which can be slow on a large network; the BGP and route counts above come from the model, this table from Forward's route diff")
		detail["l3"] = l3
	}
	vul := map[string]any{}
	if v, _, err := d.VulnerabilitiesCounts(ctx, before, after); err != nil {
		unread("vulnerability", "VIEW_CVE_DIFFS (and the security licence facet)", err)
	} else {
		vul["new_cve_count"] = v.NewCVECount
		vul["new_exposed_vulnerable_devices_count"] = v.NewExposedVulnerableDevicesCount // nil means Forward cannot tell (no internet exposure computed)
		if v.NewCVECount > 0 {
			changed = append(changed, "vulnerabilities")
		}
		if b, _, berr := d.VulnerabilitiesBidirectional(ctx, before, after); berr == nil {
			vul["warnings"], vul["improvements"] = b.Warnings, b.Improvements
		} else {
			limits = append(limits, "the improvements side of the vulnerability diff needs the organization property PREDICT_DIFF_INSIGHT_ENHANCEMENTS and was not read")
		}
		detail["vulnerabilities"] = vul
	}
	if lp, _, err := d.RoutingLoopsBidirectional(ctx, before, after); err != nil {
		limits = append(limits, "whether a routing loop appeared or was resolved was not read (needs VIEW_CHECK_DIFFS and the organization property PREDICT_DIFF_INSIGHT_ENHANCEMENTS): "+err.Error())
	} else if !lp.Warnings.Complete && !lp.Improvements.Complete {
		limits = append(limits, "whether a routing loop appeared or was resolved is unknown: Forward did not complete the routing-loop diff")
	} else {
		detail["routing_loops"] = map[string]any{"introduced": lp.Warnings, "resolved": lp.Improvements, "note": "0 or 1: whether a loop was introduced or resolved, not how many loops; complete false means unknown"}
		if lp.Warnings.Count > 0 {
			changed = append(changed, "routing loops (a loop introduced)")
		}
	}
	return detail, limits, changed
}

// diffDevices lists the changed devices, the most changed first, capped, with their per-type counts.
func diffDevices(devs []forward.DeviceDiffSummary) []map[string]any {
	sort.SliceStable(devs, func(i, j int) bool { return changedTotal(devs[i]) > changedTotal(devs[j]) })
	shown, _ := result.CapRow(devs, maxNativeDiffDevices)
	out := make([]map[string]any, 0, len(shown))
	for _, x := range shown {
		out = append(out, map[string]any{"deviceName": x.DeviceName, "hasConfigChange": x.HasConfigChange, "stats": x.Stats})
	}
	return out
}

func changedTotal(d forward.DeviceDiffSummary) int {
	return d.Stats["ADDED"] + d.Stats["DELETED"] + d.Stats["MODIFIED"]
}
