package skills

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// collectionTriage is view triage: first contact with a network's collection health in ONE call. It runs the summary, the slowest
// devices and the worst-failing platform group, and merges them into one finding and one evidence list, so an SE does not run three
// skills to get oriented. It takes no inputs of its own beyond network_id and snapshot_id.
func collectionTriage(ctx context.Context, s *fwd.Session, in collectionInput, cx result.Context) (result.Result, error) {
	sumRaw, _ := json.Marshal(map[string]any{"network_id": in.NetworkID, "snapshot_id": in.SnapshotID})
	summary, err := investigateCollectionFailure(ctx, s, sumRaw)
	if err != nil {
		return result.Result{}, err
	}
	// No snapshot, or still processing: that is the whole answer, nothing else to add.
	if summary.Status == result.Unknown {
		return summary, nil
	}

	finding := summary.Finding
	limits := append([]string{}, summary.Limits...)
	evidence := append([]result.Evidence{}, summary.Evidence...)
	next := summary.NextActions

	slowIn := collectionInput{NetworkID: in.NetworkID, SnapshotID: in.SnapshotID, Limit: 5}
	if slow, serr := slowCollection(ctx, s, slowIn, cx); serr == nil && slow.Status != result.Unknown && len(slow.Evidence) > 0 {
		if rows, ok := slow.Evidence[0].Detail["devices"].([]map[string]any); ok && len(rows) > 0 {
			evidence = append(evidence, result.NewEvidence(result.EvCollection, "triageSlowest", cx.SnapshotID,
				map[string]any{"slowest_devices": rows}, slow.Finding))
			finding += fmt.Sprintf("; slowest: %v", rows[0]["device"])
		}
	} else if serr == nil {
		limits = append(limits, "the slowest devices could not be read: "+slow.Finding)
	}

	rollIn := collectionInput{NetworkID: in.NetworkID, SnapshotID: in.SnapshotID, GroupBy: "os_version", Limit: 1}
	if roll, rerr := failureRollup(ctx, s, rollIn, cx); rerr == nil && roll.Status == result.Failed && len(roll.Evidence) > 0 {
		if groups, ok := roll.Evidence[0].Detail["groups"].([]map[string]any); ok && len(groups) > 0 {
			evidence = append(evidence, result.NewEvidence(result.EvCollection, "triageWorstPlatform", cx.SnapshotID,
				map[string]any{"worst_platform": groups[0]}, roll.Finding))
			finding += fmt.Sprintf("; worst platform: %v (%v failed of %v)", groups[0]["group"], groups[0]["failed"], groups[0]["devices"])
		}
	}

	limits = append(limits, "triage merges the default view, the 5 slowest devices and the worst-failing platform group in one call; it does not read the organization's license capacity (no SDK route reads it) or per-device check compliance. For the full detail behind any line read the matching view (devices, platforms, slow) on its own")
	return result.Build(collectionFailureName, summary.Status, finding, result.Deterministic, cx, result.Options{Limits: limits, Evidence: evidence, NextActions: next})
}
