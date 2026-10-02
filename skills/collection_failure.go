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

const collectionFailureName = "investigate-collection-failure"

func init() { Register(collectionFailureName, investigateCollectionFailure) }

type collectionInput struct {
	NetworkID       string `json:"network_id"`
	SnapshotID      string `json:"snapshot_id"`
	CollectorTaskID string `json:"collector_task_id"`
	// View is "summary" (the default: counts by cause), "devices" (which devices failed, filtered by category, type or device) or
	// "neighbors" (unmodelled neighbours, BGP peers first), "slow" (per-device collection duration and slowest command) or "logs" (one
	// device's collection log; failure is then the minimum level).
	View string `json:"view"`
	// Failure keeps the devices whose failure category (credentials, network_path, device_session, unclassified, processing) or type
	// (CONNECTION_REFUSED, PARSER_EXCEPTION, ...) is this.
	Failure string `json:"failure"`
	Device  string `json:"device"`
	Limit   int    `json:"limit"`
	Offset  int    `json:"offset"`
	// GroupBy and CompareTo are set by the platforms and changes views; they are not inputs.
	GroupBy   string `json:"-"`
	CompareTo string `json:"-"`
}

func sum(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func typesText(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s x%d", k, m[k]))
	}
	return strings.Join(parts, ", ")
}

func investigateCollectionFailure(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in collectionInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	switch in.View {
	case "", "summary", "devices", "platforms", "changes", "exceptions", "neighbors", "slow", "logs":
	default:
		return result.Result{}, fmt.Errorf("%w: view must be summary, devices, platforms, changes, exceptions, neighbors, slow or logs", ErrInvalidInput)
	}
	view := in.View
	if view == "" {
		view = "summary"
	}
	if err := rejectForeignInputs(raw, collectionFailureName, "view", view, map[string][]string{
		"summary":    {"collector_task_id"},
		"devices":    {"failure", "device", "limit", "offset"},
		"platforms":  {"failure", "device", "limit", "offset"},
		"changes":    {"limit", "offset"},
		"exceptions": {"device", "limit", "offset"},
		"neighbors":  {"limit", "offset"},
		"slow":       {"device", "limit", "offset"},
		"logs":       {"failure", "device", "limit", "offset"},
	}); err != nil {
		return result.Result{}, err
	}
	var snap *forward.Snapshot
	var snapErr error
	if in.SnapshotID != "" {
		snap, snapErr = s.Snapshot(ctx, in.NetworkID, in.SnapshotID)
	} else {
		snap, snapErr = s.NewestSnapshot(ctx, in.NetworkID)
	}
	if snapErr != nil {
		return result.Result{}, snapErr
	}
	cx := fwd.Context(in.NetworkID, snap)
	if snap == nil {
		return result.NewUnknown(collectionFailureName, "There is no snapshot to investigate", cx,
			[]string{"the network has no snapshots (or the given one does not exist)"}, result.Options{})
	}
	sid := fwd.SnapshotIDPtr(snap)
	state := fwd.StateOf(snap)
	if fwd.InProgress(state) {
		return result.NewUnknown(collectionFailureName, fmt.Sprintf("The snapshot is still %s; failures are not final", strings.ToLower(state)), cx,
			[]string{fmt.Sprintf("snapshot state is %s; counts are incomplete until processing ends", state)}, result.Options{})
	}
	switch view {
	case "devices":
		return failureDevices(ctx, s, in, cx)
	case "platforms":
		in.GroupBy = "os_version"
		return failureRollup(ctx, s, in, cx)
	case "changes":
		in.CompareTo = "previous"
		return failureCompare(ctx, s, in, cx)
	case "exceptions":
		return collectorExceptions(ctx, s, in, cx)
	case "neighbors":
		return unmodelledNeighbors(ctx, s, in, cx)
	case "slow":
		return slowCollection(ctx, s, in, cx)
	case "logs":
		return deviceLog(ctx, s, in, cx)
	}

	var evidence []result.Evidence
	var problems, limits []string

	if fwd.ProcessFailed(state) {
		problems = append(problems, "the snapshot ended "+state)
		evidence = append(evidence, result.NewEvidence(result.EvCollection, "getSnapshot", sid, map[string]any{"snapshot_state": state}, "snapshot "+state))
	}
	if in.CollectorTaskID != "" {
		t, err := s.Task(ctx, in.CollectorTaskID)
		if err != nil {
			return result.Result{}, err
		}
		detail := map[string]any{"id": t.ID, "status": t.Status, "type": t.Type, "started_at": t.StartedAt, "finished_at": t.FinishedAt}
		switch t.Status {
		case "QUEUED", "RUNNING":
			return result.NewUnknown(collectionFailureName, fmt.Sprintf("The collection task is still %s", strings.ToLower(t.Status)), cx,
				[]string{fmt.Sprintf("collector task %s is %s; the outcome is not known yet", t.ID, t.Status)},
				result.Options{Evidence: []result.Evidence{result.NewEvidence(result.EvCollection, "getCollectorTask", sid, detail, "")}})
		case "FAILED", "TIMED_OUT", "CANCELED":
			problems = append(problems, "the collection task "+strings.ToLower(strings.ReplaceAll(t.Status, "_", " ")))
			evidence = append(evidence, result.NewEvidence(result.EvCollection, "getCollectorTask", sid, detail, "task "+t.Status))
		}
	}

	m, err := s.SnapshotMetrics(ctx, fwd.SnapshotID(cx))
	if err != nil {
		return result.Result{}, err
	}
	byCategory := map[string]map[string]int{}
	for t, n := range m.CollectionFailures {
		c := fwd.FailureCategory(t)
		if byCategory[c] == nil {
			byCategory[c] = map[string]int{}
		}
		byCategory[c][t] = n
	}
	cats := make([]string, 0, len(byCategory))
	for c := range byCategory {
		cats = append(cats, c)
	}
	sort.SliceStable(cats, func(i, j int) bool {
		a, b := sum(byCategory[cats[i]]), sum(byCategory[cats[j]])
		if a != b {
			return a > b
		}
		return cats[i] < cats[j]
	})
	for _, c := range cats {
		total := sum(byCategory[c])
		problems = append(problems, fmt.Sprintf("%d device collection failure(s): %s", total, c))
		evidence = append(evidence, result.NewEvidence(result.EvCollection, "getSnapshotMetrics", sid,
			map[string]any{"category": c, "types": byCategory[c], "devices": total}, c+": "+typesText(byCategory[c])))
	}
	if len(m.ProcessingFailures) > 0 {
		total := sum(m.ProcessingFailures)
		problems = append(problems, fmt.Sprintf("%d device processing failure(s) (parsing or modeling)", total))
		evidence = append(evidence, result.NewEvidence(result.EvCollection, "getSnapshotMetrics", sid,
			map[string]any{"category": "processing", "types": m.ProcessingFailures, "devices": total}, "processing: "+typesText(m.ProcessingFailures)))
	}

	missing, err := s.MissingDevices(ctx, in.NetworkID, fwd.SnapshotID(cx))
	if err != nil {
		return result.Result{}, err
	}
	if len(missing) > 0 {
		names := make([]string, 0, len(missing))
		for i, d := range missing {
			if i == 25 {
				break
			}
			names = append(names, d.Name)
		}
		evidence = append(evidence, result.NewEvidence(result.EvCollection, "getMissingDevices", sid,
			map[string]any{"devices": names, "count": len(missing)}, fmt.Sprintf("%d neighbour device(s) seen but not modeled", len(missing))))
		limits = append(limits, fmt.Sprintf("%d neighbour device(s) are seen but not modeled; paths through them are incomplete", len(missing)))
	}

	if len(problems) > 0 {
		exc, readable, err := s.SnapshotExceptions(ctx, fwd.SnapshotID(cx))
		if err != nil {
			return result.Result{}, err
		}
		if !readable {
			limits = append(limits, "snapshot exceptions were not read (they need a permission the caller lacks)")
		}
		sort.SliceStable(exc, func(i, j int) bool { return exc[i].Occurrences > exc[j].Occurrences })
		if len(exc) > 10 {
			limits = append(limits, fmt.Sprintf("%d kinds of processing exception; the 10 most frequent are cited", len(exc)))
			exc = exc[:10]
		}
		for _, e := range exc {
			devs := e.Devices
			if len(devs) > 25 {
				devs = devs[:25]
			}
			evidence = append(evidence, result.NewEvidence(result.EvCollection, "getSnapshotExceptions", sid,
				map[string]any{"type": e.Type, "occurrences": e.Occurrences, "devices": devs}, fmt.Sprintf("%s x%d", e.Type, e.Occurrences)))
		}
		limits = append(limits, "these are counts: view devices names the failed devices (filter by category, type or device) and view neighbors lists the unmodelled neighbours with BGP peers first")
		return result.Build(collectionFailureName, result.Failed, strings.Join(problems, "; "), result.Deterministic, cx,
			result.Options{Limits: limits, Evidence: evidence})
	}

	if m.SuccessfulDevices == 0 {
		return result.NewUnknown(collectionFailureName, "The snapshot holds no collected devices, so collection health is undetermined", cx,
			append(limits, "no devices were collected and no failure was recorded; nothing was measured"), result.Options{Evidence: evidence})
	}
	evidence = append(evidence, result.NewEvidence(result.EvCollection, "getSnapshotMetrics", sid,
		map[string]any{"successful_devices": m.SuccessfulDevices, "state": state}, fmt.Sprintf("%d devices collected, no failures", m.SuccessfulDevices)))
	return result.Build(collectionFailureName, result.OK, fmt.Sprintf("Collection is healthy: %d devices collected with no failures", m.SuccessfulDevices),
		result.Deterministic, cx, result.Options{Limits: limits, Evidence: evidence, NextActions: []string{"check-network-compliance"}})
}
