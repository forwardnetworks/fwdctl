package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// pathsInput is verify-change view paths: the GUI's "view interesting paths" (alt-click a snapshot). It ranks sampled paths that go through firewalls or load balancers, and the suspicious kinds (blackholes, drops,
// loops, L2 storms), for a snapshot or for a change set's predicted snapshot, so the same question can be asked before and after a change.
type pathsInput struct {
	NetworkID   string `json:"network_id"`
	SnapshotID  string `json:"after_snapshot_id"`
	ChangeSetID string `json:"change_set_id"`
	// Seconds is how long Forward may search each kind (it reuses connectivity_timeout_seconds so the view adds no input).
	Seconds int `json:"connectivity_timeout_seconds"`
}

// pathKinds are the searches run: the two the GUI offers for access through a firewall or a load balancer. The suspicious kinds (blackholes, drops, loops, L2 storms) share the SDK route but answer
// another question and are left to a later view if they are wanted.
var pathKinds = []struct {
	name string
	kind forward.InterestingPathsKind
}{{"firewall", forward.InterestingPathsFirewall}, {"loadbalancer", forward.InterestingPathsLoadBalancer}}

// pathsPerKind is how many ranked paths are shown for each kind; the total found is always stated.
const pathsPerKind = 10

func interestingPaths(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in pathsInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if in.NetworkID == "" {
		return result.Result{}, fmt.Errorf("%w: network_id is required", ErrInvalidInput)
	}
	if in.SnapshotID != "" && in.ChangeSetID != "" {
		return result.Result{}, fmt.Errorf("%w: give after_snapshot_id or change_set_id, not both", ErrInvalidInput)
	}
	maxSeconds := orDefault(in.Seconds, 45)
	if maxSeconds > 300 {
		return result.Result{}, fmt.Errorf("%w: connectivity_timeout_seconds is at most 300 for view paths (Forward runs each search while the call waits)", ErrInvalidInput)
	}
	limit := pathsPerKind

	var snap *forward.Snapshot
	var err error
	label := "the latest processed snapshot"
	switch {
	case in.ChangeSetID != "":
		snap, err = s.PredictedSnapshot(ctx, in.NetworkID, in.ChangeSetID, false)
		label = "the predicted snapshot of change set " + in.ChangeSetID
	case in.SnapshotID != "":
		snap, err = s.Snapshot(ctx, in.NetworkID, in.SnapshotID)
		label = "snapshot " + in.SnapshotID
	default:
		snap, err = s.LatestProcessed(ctx, in.NetworkID)
	}
	if err != nil {
		return result.Result{}, err
	}
	cx := fwd.Context(in.NetworkID, snap)
	if !fwd.IsReady(snap) {
		return result.NewUnknown(verifyChangeName, "There is no processed snapshot to search ("+label+")", cx,
			[]string{"no paths were searched: " + label + " is missing or not processed (a change set needs run_predict through edit-change-set first)"}, result.Options{NextActions: []string{"edit-change-set"}})
	}
	sid := fwd.SnapshotID(cx)

	out := map[string]any{}
	var limits []string
	var omitted []result.Omission
	total := 0
	var kinds []string
	for _, pk := range pathKinds {
		k := pk.name
		kinds = append(kinds, k)
		res, _, serr := s.Client.Networks.InterestingPaths(ctx, in.NetworkID, sid, pk.kind, maxSeconds)
		switch {
		case errors.Is(serr, forward.ErrSnapshotNotProcessed):
			return result.NewUnknown(verifyChangeName, "Interesting paths need advanced reachability on "+label+", which has not been computed", cx,
				[]string{"Forward answered: " + serr.Error() + "; nothing was ranked"}, result.Options{NextActions: []string{advancedReachabilityName}})
		case errors.Is(serr, forward.ErrSnapshotProcessingFailed):
			return result.NewUnknown(verifyChangeName, "Advanced reachability failed on "+label+", so its interesting paths cannot be ranked", cx,
				[]string{"Forward answered: " + serr.Error()}, result.Options{NextActions: []string{advancedReachabilityName}})
		case fwd.Status(serr) == 403:
			return result.NewUnknown(verifyChangeName, "This login or licence cannot view paths, so interesting paths were not searched", cx,
				[]string{"Forward refused the search (403): it needs the VIEW_PATHS operation, which a licence tier without path search lacks; " + serr.Error()}, result.Options{NextActions: []string{"inspect-environment"}})
		case serr != nil:
			return result.Result{}, serr
		}
		paths := res.Paths
		sort.SliceStable(paths, func(i, j int) bool { return paths[i].Score > paths[j].Score }) // Forward joins its slices unordered
		shown, n := result.CapRow(paths, limit)
		rows := make([]map[string]any, 0, len(shown))
		for i, p := range shown {
			row := map[string]any{"rank": i + 1, "score": p.Score, "hops": p.SamplePathGroup.NumHops, "tags": p.SamplePathGroup.Tags, "flow_type": p.SamplePathGroup.FlowType, "id": p.ID}
			if q, qerr := p.PathQuery(); qerr == nil {
				row["path_query"] = q
			}
			rows = append(rows, row)
		}
		out[k] = map[string]any{"paths_found": n, "paths": rows}
		total += n
		if n > len(shown) {
			omitted = append(omitted, result.Omission{What: k + " paths", Total: n, Shown: len(shown), Next: "the rest are not shown; narrow with trace paths from a listed path_query"})
		}
	}
	limits = append(limits,
		"the paths are SAMPLED (IPv4 host-to-host flows between sampled hosts), ranked by Forward's rudimentary score (one point per device hop plus points per processing stage such as ACL, NAT, MPLS, PBR), so a path not listed is not proven absent",
		fmt.Sprintf("Forward ran each search for at most %d seconds (a soft limit); it does not say whether a search was cut short, so a short list on a large network may be incomplete (raise connectivity_timeout_seconds, at most 300)", maxSeconds),
		"tags are Forward's processing stages (IN_ACL, OUT_ACL, XLATE_DNAT, MPLS, ...); there is no firewall tag, the search picks paths through devices of that type; path_query is the question to give a path search to reproduce the path")
	finding := fmt.Sprintf("Interesting paths on %s (%s): %d found; the top %d of each are ranked below", label, strings.Join(kinds, " and "), total, limit)
	return result.Build(verifyChangeName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, Omitted: omitted, NextActions: []string{"investigate-reachability", "verify-change"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvPredict, "interestingPaths", cx.SnapshotID, map[string]any{"view": "paths", "snapshot_id": sid, "searches": out}, finding)}})
}
