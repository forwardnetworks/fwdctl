package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

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
	for _, k := range []string{"collector", "names_only", "offset", "wait_seconds", "data_file", "data_connector", "include_content"} {
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
		names  []string
		byType map[string]int
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
	for _, k := range keys {
		g := groups[k]
		sort.Strings(g.names)
		shown, total := result.CapRow(g.names, limit)
		failed += total
		rows = append(rows, map[string]any{"error": k.err, "phase": k.phase, "count": total, "by_type": g.byType, "devices": shown, "devices_total": total})
	}
	limits := []string{
		"a connectivity test is not a collection: a device can fail its test and never appear in a snapshot's failures (the snapshot views of investigate-collection-failure), or the reverse, so a failed test here is not a failed collection and a clean test is not a collected device",
		fmt.Sprintf("%d of %d devices have no stored test result and are in no count above", never, len(statuses)),
		"each error group lists up to limit devices (default 20) with the group's total; the time a result was saved is not read here",
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
