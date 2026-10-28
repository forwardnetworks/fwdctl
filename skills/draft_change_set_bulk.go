package skills

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// changeSetBulkDelete is edit-change-set bulk_delete: delete several change sets of one network at once. Irreversible, so apply needs confirm equal to how many are named. A dry run reads
// each one and lists names the network does not have.
func changeSetBulkDelete(ctx context.Context, s *fwd.Session, in draftChangeSetInput) (result.Result, error) {
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	if len(in.Devices) > 0 || len(in.BGP) > 0 || len(in.Firewall) > 0 || in.Run || in.SnapshotID != "" || in.ChangeSetID != "" || in.Lifecycle != nil || in.DirectoryOp != nil {
		return result.Result{}, fmt.Errorf("%w: bulk_delete does not combine with a change set, lifecycle, directory_op, devices, bgp_advertisements, firewall_rules, snapshot_id or run", ErrInvalidInput)
	}
	ids := map[string]bool{}
	for _, id := range in.BulkDelete {
		if id = strings.TrimSpace(id); id != "" {
			ids[id] = true
		}
	}
	list, err := s.ChangeSets(ctx, in.NetworkID)
	if err != nil {
		if r, ok := denialResult(draftChangeSetName, err, cx, "listing the network's change sets"); ok {
			return r, nil
		}
		return result.Result{}, err
	}
	have := map[string]string{}
	for _, c := range list {
		have[string(c.ID)] = c.Name
	}
	var found, missing []string
	rows := []map[string]any{}
	for id := range ids {
		if n, ok := have[id]; ok {
			found = append(found, id)
			rows = append(rows, map[string]any{"id": id, "name": n})
		} else {
			missing = append(missing, id)
		}
	}
	sort.Strings(found)
	sort.Strings(missing)
	sort.Slice(rows, func(i, j int) bool { return rows[i]["id"].(string) < rows[j]["id"].(string) })
	if len(missing) > 0 {
		return result.NewUnknown(draftChangeSetName, fmt.Sprintf("%d of the %d named change sets are not in network %s; nothing was deleted", len(missing), len(ids), in.NetworkID), cx,
			[]string{"missing ids: " + strings.Join(missing, ", ") + "; verify-change view list shows the ids"}, result.Options{NextActions: []string{"verify-change"}})
	}
	confirmWith := strconv.Itoa(len(found))
	ch := result.Change{Action: "delete_change_sets", Target: "change sets of network " + in.NetworkID, Before: map[string]any{"change_sets": rows}, After: map[string]any{"deleted": len(found)}, Reversible: false, Undo: "none: the change sets and their versions are gone (predicted snapshots they made stay; edit-snapshot deletes those)"}
	limits := []string{"nothing here reaches a device; Preview route, source-read, not run on a live Forward"}
	if !in.Apply {
		return result.Build(draftChangeSetName, result.OK, fmt.Sprintf("Dry run: would delete %d change sets. Nothing was changed; run again with apply=true and confirm=%q", len(found), confirmWith), result.Deterministic, cx,
			result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Limits: limits, NextActions: []string{"verify-change"},
				Evidence: []result.Evidence{result.NewEvidence(result.EvPredict, "changeSetBulkDelete", nil, map[string]any{"mode": result.ModeDryRun, "change_sets": rows}, "")}})
	}
	if strings.TrimSpace(in.Confirm) != confirmWith {
		return result.Build(draftChangeSetName, result.Failed, fmt.Sprintf("Refused, nothing was changed: deleting %d change sets needs confirm=%q", len(found), confirmWith), result.Deterministic, cx,
			result.Options{Mode: result.ModeApplied, Limits: limits, Evidence: []result.Evidence{result.NewEvidence(result.EvPredict, "changeSetBulkDelete", nil, map[string]any{"mode": result.ModeApplied, "changed": false}, "")}})
	}
	if _, err := s.Client.Predict.DeleteChangeSets(ctx, in.NetworkID, found); err != nil {
		return result.Result{}, fmt.Errorf("deleting %d change sets failed, some may be gone: %w", len(found), err)
	}
	ch.Applied = true
	finding := fmt.Sprintf("Applied: deleted %d change sets", len(found))
	return result.Build(draftChangeSetName, result.OK, finding, result.Deterministic, cx, result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Limits: limits, NextActions: []string{"verify-change"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvPredict, "changeSetBulkDelete", nil, map[string]any{"mode": result.ModeApplied, "change_sets": rows}, finding)}})
}
