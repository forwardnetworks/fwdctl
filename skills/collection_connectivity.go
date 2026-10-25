package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// inspectCollectionConnectivity is view connectivity: the last connectivity-test result of every classic device, counted by error and phase, with the devices (capped by limit) and their types per error. A connectivity test is not
// a collection: a device can fail its test and never appear in a snapshot's failures (or the reverse), so the two are read side by side, not merged.
func inspectCollectionConnectivity(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in struct {
		NetworkID string `json:"network_id"`
		Limit     int    `json:"limit"`
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
			return result.Result{}, fmt.Errorf("%w: %s belongs to another view of inspect-collection, not connectivity", ErrInvalidInput, k)
		}
	}
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	statuses, err := s.DeviceTestStatuses(ctx, in.NetworkID)
	if err != nil {
		return result.NewUnknown("inspect-collection", "Unknown: the devices' connectivity test results could not be read: "+err.Error(), cx, []string{"nothing was counted; this is not a pass"}, result.Options{})
	}
	type key struct{ err, phase string }
	type group struct {
		names    []string
		byType   map[string]int
		from, to string // the earliest and latest saved time of the group's tests (ISO strings sort as times)
	}
	groups := map[key]*group{}
	tested, noErr, never := 0, 0, 0
	for _, st := range statuses {
		if !st.TestResultPresent {
			never++
			continue
		}
		tested++
		if st.ConnectivityError == "" {
			noErr++
			continue
		}
		k := key{st.ConnectivityError, st.ErrorPhase}
		g := groups[k]
		if g == nil {
			g = &group{byType: map[string]int{}}
			groups[k] = g
		}
		g.names = append(g.names, st.Name)
		if at := firstNonEmpty(st.TestSavedAt, st.TestEndTime); at != "" {
			if g.from == "" || at < g.from {
				g.from = at
			}
			if at > g.to {
				g.to = at
			}
		}
		typ := st.Type
		if typ == "" {
			typ = st.DiscoveredType
		}
		if typ == "" {
			typ = "(none)"
		}
		g.byType[typ]++
	}
	keys := make([]key, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := groups[keys[i]], groups[keys[j]]
		if len(a.names) != len(b.names) {
			return len(a.names) > len(b.names)
		}
		return keys[i].err < keys[j].err
	})
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	rows := make([]map[string]any, 0, len(keys))
	failed := 0
	sawAuthz := false
	var groupIdx map[string][]string
	for _, k := range keys {
		g := groups[k]
		sort.Strings(g.names)
		shown, total := result.CapRow(g.names, limit)
		failed += total
		var details []map[string]any
		if strings.Contains(k.err, "AUTHORIZATION") {
			if groupIdx == nil {
				groupIdx = customCommandIndex(ctx, s, in.NetworkID)
			}
			for i, n := range shown {
				if i == maxAuthzDetails {
					break
				}
				if d := authzDetail(ctx, s, in.NetworkID, n, groupIdx); d != nil {
					details = append(details, d)
				}
			}
			sawAuthz = true
		}
		rows = append(rows, map[string]any{"error": k.err, "phase": k.phase, "count": total, "by_type": g.byType, "devices": shown, "devices_total": total, "tested_from": g.from, "tested_to": g.to, "device_details": details})
	}
	limits := []string{
		"a connectivity test is not a collection: a device can fail its test and never appear in a snapshot's failures (the snapshot views of investigate-collection-failure), or the reverse, so a failed test here is not a failed collection and a clean test is not a collected device",
		fmt.Sprintf("%d of %d devices have no stored test result and are in no count above", never, len(statuses)),
		"each error group lists up to limit devices (default 20) with the group's total and the range of times its results were saved (tested_from, tested_to)",
	}
	if sawAuthz {
		limits = append(limits, authzLimit, fmt.Sprintf("device_details covers the first %d devices of an authorization group", maxAuthzDetails))
	}
	detail := map[string]any{"connectivity": map[string]any{"devices_read": len(statuses), "tested": tested, "no_error": noErr, "never_tested": never, "with_error": failed, "by_error": rows}}
	finding := fmt.Sprintf("%d of %d tested devices have a failed connectivity test (%d clean, %d never tested)", failed, tested, noErr, never)
	status := result.Failed
	if failed == 0 {
		status = result.OK
		finding = fmt.Sprintf("No stored connectivity test shows an error: %d tested devices are clean, %d have never been tested", noErr, never)
	}
	return result.Build("inspect-collection", status, finding, result.Deterministic, cx, result.Options{
		Limits: limits, Evidence: []result.Evidence{result.NewEvidence(result.EvState, "classicDevices", nil, detail, "")},
	})
}

// maxAuthzDetails bounds the per-device reads of an authorization failure's stored result.
const maxAuthzDetails = 20

// authzDetail reads one device's stored test result and returns what an authorization failure needs: the commands the account may not run, the login the test found, the addresses reached and when. It returns nil when the
// result cannot be read (the caller says so).
func authzDetail(ctx context.Context, s *fwd.Session, networkID, name string, groups map[string][]string) map[string]any {
	r, _, err := s.Client.ConnectivityTests.Get(ctx, networkID, name)
	if err != nil || r == nil {
		return nil
	}
	d := map[string]any{"name": name}
	if len(r.UnauthorizedCommands) > 0 {
		cmds, n := result.CapRow(r.UnauthorizedCommands, 10)
		d["unauthorized_commands"], d["unauthorized_commands_total"] = cmds, n
	}
	// an unauthorized command that a custom command group holds is the network's own command, not a Forward default
	held := map[string]bool{}
	for _, c := range r.UnauthorizedCommands {
		for _, g := range groups[strings.ToLower(strings.TrimSpace(c))] {
			held[g] = true
		}
	}
	if len(held) > 0 {
		names := make([]string, 0, len(held))
		for g := range held {
			names = append(names, g)
		}
		sort.Strings(names)
		d["defined_by_custom_group"] = names
		d["meaning"] = "an unauthorized command is a custom command defined by " + strings.Join(names, ", ") + " (a custom command group of this network), not a Forward default: authorize the account for it on the device or in the AAA system, or disable or remove that group (edit-source object custom_command_group)"
	}
	if r.DiscoveredCLICredentialID != "" {
		d["discovered_cli_credential"] = r.DiscoveredCLICredentialID
	}
	if len(r.HostIPs) > 0 {
		d["host_ips"] = r.HostIPs
	}
	if at := firstNonEmpty(r.SavedAt, r.EndTime); at != "" {
		d["tested_at"] = at
	}
	return d
}

// customCommandIndex maps each lowercased command of the network's custom command groups to the groups that hold it (nil when the groups cannot be read: the explanation is then simply not made).
func customCommandIndex(ctx context.Context, s *fwd.Session, networkID string) map[string][]string {
	groups, _, err := s.Client.CustomCommandGroups.List(ctx, networkID)
	if err != nil {
		return nil
	}
	idx := map[string][]string{}
	for _, g := range groups {
		for _, c := range g.Commands {
			k := strings.ToLower(strings.TrimSpace(c))
			idx[k] = append(idx[k], g.Name)
		}
	}
	return idx
}

const authzLimit = "for an authorization failure the stored test result lists the commands the account may not run (unauthorized_commands, when Forward recorded them) and the login the test found; it does not carry the credential that was tried, the privilege level or the command that was rejected first, and this tool knows no API that returns them"
