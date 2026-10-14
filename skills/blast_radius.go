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

const blastRadiusName = verifyChangeName

func analyzeBlastRadius(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in changeInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	rc, err := resolveChange(ctx, s, in)
	if err == errNoSelector {
		return result.NewError(blastRadiusName, err.Error(), fwd.Context(in.NetworkID, nil)), nil
	}
	if err != nil {
		return result.Result{}, err
	}
	cx := fwd.Context(in.NetworkID, rc.After)
	limits := append([]string(nil), rc.Limits...)
	if rc.BeforeID == "" || !fwd.IsReady(rc.Before) || !fwd.IsReady(rc.After) {
		if len(limits) == 0 {
			limits = []string{"a snapshot is missing or not processed; nothing was compared"}
		}
		return result.NewUnknown(blastRadiusName, "There is nothing to compare, so the extent of the change is unknown", cx, limits, result.Options{})
	}
	afterID := fwd.SnapshotID(cx)
	sid := fwd.SnapshotIDPtr(rc.After)
	var evidence []result.Evidence
	var touched, incomplete []string
	measured := false

	counts, err := s.AreaCounts(ctx, rc.BeforeID, afterID)
	if err != nil {
		return result.Result{}, err
	}
	var complete []fwd.AreaCount
	for _, c := range counts {
		if c.Unavailable || !c.Complete {
			incomplete = append(incomplete, c.Area)
			continue
		}
		complete = append(complete, c)
	}
	if len(incomplete) > 0 {
		sort.Strings(incomplete)
		limits = append(limits, "difference counts not complete for: "+strings.Join(incomplete, ", "))
	}
	if len(counts) > 0 {
		areas := make([]string, 0, len(counts))
		for _, c := range counts {
			areas = append(areas, c.Area)
		}
		limits = append(limits, "only these difference areas were compared: "+strings.Join(areas, ", ")+"; any other area was not measured, so say nothing about it")
	}
	if len(complete) > 0 {
		measured = true
	}
	sort.SliceStable(complete, func(i, j int) bool { return complete[i].Count > complete[j].Count })
	areas := make([]string, 0, len(complete))
	for _, c := range complete {
		areas = append(areas, c.Area)
		if c.Count > 0 {
			touched = append(touched, fmt.Sprintf("%s (%d)", c.Area, c.Count))
			evidence = append(evidence, result.NewEvidence(result.EvPredict, "getDiffCount:"+c.Area, sid,
				map[string]any{"area": c.Area, "count": c.Count}, fmt.Sprintf("%s: %d differences", c.Area, c.Count)))
		}
	}

	// ACL changes at the rule level: which devices' ACLs changed (counts), and with `device`, that device's changed rules. Only when the ACL area changed.
	if aclCount(complete) > 0 {
		ev, lim := aclChangeEvidence(ctx, s, rc.BeforeID, afterID, in.Device, sid)
		evidence = append(evidence, ev...)
		limits = append(limits, lim...)
	} else if in.Device != "" {
		limits = append(limits, "device was given but the acl area has no complete, non-zero difference count, so no ACL rules were listed for "+in.Device+": that is not evidence its ACLs did not change")
	}
	conn, err := s.SubnetConnectivity(ctx, rc.BeforeID, afterID, time.Duration(orDefault(in.ConnectivitySecs, 300))*time.Second)
	if err != nil {
		return result.Result{}, err
	}
	var next []string
	switch {
	case conn.Unavailable != "":
		limits = append(limits, "the connectivity comparison is not available: "+conn.Unavailable+"; connectivity impact is unmeasured")
	case !conn.Settled:
		limits = append(limits, "the connectivity comparison did not settle in time; its counts are not evidence")
	case !conn.Compared:
		limits = append(limits, "no subnet pairs were compared (no locations defined?); connectivity impact is unmeasured")
	default:
		measured = true
		evidence = append(evidence, result.NewEvidence(result.EvPredict, "getSubnetConnectivityDiff", sid, map[string]any{
			"newly_isolated": conn.NewlyIsolated, "newly_connected": conn.NewlyConnected, "modified": conn.Modified, "total_pairs": conn.TotalPairs},
			fmt.Sprintf("%d pairs newly isolated, %d newly connected of %d", conn.NewlyIsolated, conn.NewlyConnected, conn.TotalPairs)))
		if conn.NewlyIsolated > 0 {
			next = append(next, "investigate-reachability")
		}
	}
	next = append(next, "verify-change")

	if !measured {
		return result.NewUnknown(blastRadiusName, "No comparison completed, so the extent of the change is unknown", cx, limits, result.Options{})
	}
	var parts []string
	if len(touched) > 0 {
		parts = append(parts, "differs in "+strings.Join(touched, ", "))
	}
	if conn.Settled && conn.Compared && (conn.NewlyIsolated > 0 || conn.NewlyConnected > 0) {
		parts = append(parts, fmt.Sprintf("%d subnet pair(s) lost connectivity, %d gained", conn.NewlyIsolated, conn.NewlyConnected))
	}
	finding := "No observable difference between the snapshots"
	if len(parts) > 0 {
		finding = strings.Join(parts, "; ")
	}
	if len(evidence) == 0 {
		evidence = append(evidence, result.NewEvidence(result.EvPredict, "getDiffCount", sid,
			map[string]any{"areas_compared": areas, "differences": 0}, "no differences in any complete area"))
	}
	return result.Build(blastRadiusName, result.OK, finding, result.Deterministic, cx,
		result.Options{Limits: limits, Evidence: evidence, NextActions: next})
}

const (
	maxACLDevicesShown = 15
	maxACLRulesShown   = 30
)

func aclCount(complete []fwd.AreaCount) int64 {
	for _, c := range complete {
		if c.Area == "acl" {
			return c.Count
		}
	}
	return 0
}

// aclChangeEvidence adds what the acl count alone cannot say. Without a device: the devices whose ACLs changed (the most changed first, capped) with their added, deleted and
// modified rule counts. With one: that device's changed rules, each with its action, name, where it applies, its match conditions and the config lines it came from.
// A failure to read either is a limit, never a failed result: the counts above still stand.
func aclChangeEvidence(ctx context.Context, s *fwd.Session, beforeID, afterID, device string, snapshot *string) ([]result.Evidence, []string) {
	if device == "" {
		devs, err := s.ACLChangedDevices(ctx, beforeID, afterID)
		if err != nil {
			return nil, []string{"the devices whose ACLs changed could not be listed: " + err.Error() + "; the acl count above stands"}
		}
		shown, _ := result.CapRow(devs, maxACLDevicesShown)
		rows := make([]map[string]any, 0, len(shown))
		for _, d := range shown {
			rows = append(rows, map[string]any{"device": d.Device, "added": d.Added, "deleted": d.Deleted, "modified": d.Modified})
		}
		lim := []string{"acl_devices lists the devices whose ACLs changed with their rule counts; give device to list that device's changed rules"}
		if len(devs) > len(shown) {
			lim = append(lim, fmt.Sprintf("acl_devices shows %d of %d devices (most changed first)", len(shown), len(devs)))
		}
		return []result.Evidence{result.NewEvidence(result.EvPredict, "getAclDiffStats", snapshot,
			map[string]any{"acl_devices": rows, "devices_changed": len(devs)}, fmt.Sprintf("ACLs changed on %d device(s)", len(devs)))}, lim
	}
	changes, stats, err := s.ACLRuleChanges(ctx, beforeID, afterID, device)
	if err != nil {
		return nil, []string{"the changed ACL rules of " + device + " could not be read: " + err.Error()}
	}
	view := func(r *fwd.ACLRule) map[string]any {
		if r == nil {
			return nil
		}
		m := map[string]any{"action": r.Action}
		if r.Name != "" {
			m["name"] = r.Name
		}
		if r.Context != "" {
			m["acl_context"] = r.Context
		}
		if len(r.InputInterfaces) > 0 {
			m["input_interfaces"] = r.InputInterfaces
		}
		if len(r.OutputInterfaces) > 0 {
			m["output_interfaces"] = r.OutputInterfaces
		}
		if len(r.Matches) > 0 {
			m["matches"] = r.Matches
		}
		if len(r.FileLines) > 0 {
			fl := map[string]any{}
			for k, v := range r.FileLines {
				fl[k] = v
			}
			m["file_lines"] = fl
		}
		return m
	}
	shown, _ := result.CapRow(changes, maxACLRulesShown)
	rows := make([]map[string]any, 0, len(shown))
	for _, c := range shown {
		row := view(c.After)
		if row == nil {
			row = view(c.Before)
		}
		if row == nil { // a diff with neither side carries nothing to show
			continue
		}
		row["change"] = c.Change
		if c.Change == "MODIFIED" && c.Before != nil {
			row["was"] = view(c.Before)
		}
		rows = append(rows, row)
	}
	lim := []string{}
	if len(rows) == 0 {
		lim = append(lim, "no changed ACL rules were returned for "+device+": that does not show the device exists or has no ACLs (check the name; acl_devices lists the devices that changed)")
	}
	lim = append(lim, "acl_changes lists each changed rule: ADDED rules have only the new side, DELETED only the old, MODIFIED shows what it was; file_lines point back to the ACL in the stored configuration (read them with inspect-device-files)")
	if len(changes) > len(shown) {
		lim = append(lim, fmt.Sprintf("acl_changes shows %d of %d changed rules", len(shown), len(changes)))
	}
	return []result.Evidence{result.NewEvidence(result.EvPredict, "getDeviceAclDiff", snapshot,
		map[string]any{"device": device, "acl_changes": rows, "stats": stats}, fmt.Sprintf("%s: %d ACL rule change(s)", device, len(changes)))}, lim
}
