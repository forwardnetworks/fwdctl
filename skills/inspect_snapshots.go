package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const inspectSnapshotsName = "inspect-snapshots"

func init() { Register(inspectSnapshotsName, inspectSnapshots) }

type inspectSnapshotsInput struct {
	NetworkID  string `json:"network_id"`
	SnapshotID string `json:"snapshot_id"`
	Kind       string `json:"kind"`
	Limit      int    `json:"limit"`
	Offset     int    `json:"offset"`
	// Retention reads the network's snapshot thinning policy and what the next cleanup would delete, instead of listing snapshots.
	Retention bool `json:"retention"`
}

// inspectSnapshots is how a caller picks the right before and after: which snapshots exist, which is the newest one worth
// reading, which are predictions, and how complete one is.
func inspectSnapshots(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in inspectSnapshotsInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	if in.NetworkID == "" {
		if in.SnapshotID != "" || in.Kind != "" || in.Limit != 0 || in.Offset != 0 || in.Retention {
			return result.Result{}, fmt.Errorf("%w: without network_id this skill answers one question, which snapshots are in progress anywhere; snapshot_id, kind, limit and offset need a network_id", ErrInvalidInput)
		}
		return busyAcrossOrg(ctx, s, result.Context{Scope: "account", State: "current"})
	}
	if in.Retention {
		if in.SnapshotID != "" || in.Kind != "" || in.Offset != 0 {
			return result.Result{}, fmt.Errorf("%w: retention reads the network's policy; snapshot_id, kind and offset do not apply (limit caps the snapshots listed)", ErrInvalidInput)
		}
		return snapshotRetention(ctx, s, in, cx)
	}
	all, err := s.Snapshots(ctx, in.NetworkID)
	if err != nil {
		return result.Result{}, err
	}
	if len(all) == 0 {
		return result.NewUnknown(inspectSnapshotsName, "The network has no snapshots", cx,
			[]string{"no snapshots: nothing has been collected or uploaded, or the network id is wrong"}, result.Options{NextActions: []string{"investigate-collection-failure"}})
	}
	if in.SnapshotID != "" {
		return snapshotDetail(ctx, s, in, all, cx)
	}
	// newest processed collected snapshot (what the other skills read by default), and newest of any kind
	sort.SliceStable(all, func(i, j int) bool { return snapCreated(all[i]) > snapCreated(all[j]) })
	latestReadable := ""
	for _, sn := range all {
		if sn.State == "PROCESSED" && !sn.Predicted() && !sn.IsDraft {
			latestReadable = string(sn.ID)
			break
		}
	}
	rows := make([]map[string]any, 0, len(all))
	predicted := 0
	for _, sn := range all {
		if sn.Predicted() {
			predicted++
		}
		rows = append(rows, snapshotRow(sn, latestReadable))
	}
	win, limits, ok := window(rows, in.Limit, in.Offset, 10, 100, "snapshots")
	if !ok {
		return result.NewUnknown(inspectSnapshotsName, fmt.Sprintf("Offset %d is beyond the %d snapshots", in.Offset, len(rows)), cx,
			[]string{fmt.Sprintf("offset %d is past the end of the %d snapshots", in.Offset, len(rows))}, result.Options{})
	}
	byID := map[string]map[string]any{}
	for _, r := range win {
		byID[str(r["id"])] = r
	}
	shownProcessing := 0
	for _, sn := range all {
		row := byID[string(sn.ID)]
		if finalState(sn.State) || row == nil {
			continue
		}
		if shownProcessing == maxProcessingReads {
			limits = append(limits, fmt.Sprintf("processing progress is read for the first %d snapshots that are not in a final state; read the others with snapshot_id", maxProcessingReads))
			break
		}
		shownProcessing++
		p, more := processingDetail(ctx, s, sn)
		row["processing"] = p
		for _, l := range more {
			limits = append(limits, "snapshot "+string(sn.ID)+": "+l)
		}
	}
	limits = append(limits, advancedLimits(ctx, s, win, byID, all)...)
	finding := fmt.Sprintf("%d snapshots (%d predicted); the newest processed collected one is %s", len(all), predicted, orNone(latestReadable))
	if latestReadable == "" {
		limits = append(limits, "no snapshot is processed and collected, so the other skills have nothing to read by default")
	}
	detail := map[string]any{"total": len(all), "predicted": predicted, "latest_readable": latestReadable, "offset": in.Offset, "snapshots": win}
	return result.Build(inspectSnapshotsName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits,
		NextActions: []string{"verify-change", "verify-change", "compare-device-config", "investigate-collection-failure"},
		Evidence:    []result.Evidence{result.NewEvidence(result.EvCollection, "listSnapshots", nil, detail, finding)}})
}

func snapshotDetail(ctx context.Context, s *fwd.Session, in inspectSnapshotsInput, all []forward.Snapshot, cx result.Context) (result.Result, error) {
	var sn *forward.Snapshot
	for i := range all {
		if string(all[i].ID) == in.SnapshotID {
			sn = &all[i]
		}
	}
	if sn == nil {
		return result.NewUnknown(inspectSnapshotsName, fmt.Sprintf("The network holds no snapshot %s", in.SnapshotID), cx,
			[]string{"no such snapshot in this network (ids are matched exactly); list the snapshots first"}, result.Options{})
	}
	sid := string(sn.ID)
	cx = fwd.Context(in.NetworkID, sn)
	detail := snapshotRow(*sn, "")
	var limits []string
	if sn.State != "PROCESSED" {
		limits = append(limits, "the snapshot is "+sn.State+", not PROCESSED, so its metrics are not final")
	}
	if !finalState(sn.State) {
		p, more := processingDetail(ctx, s, *sn)
		detail["processing"] = p
		limits = append(limits, more...)
	}
	if a, ok := detail["advanced_reachability"].(map[string]any); ok {
		limits = append(limits, readAdvancedStage(ctx, s, sid, a)...)
		limits = append(limits, advancedMeaning(advancedState(*sn)))
	}
	m, merr := s.SnapshotMetrics(ctx, sid)
	if merr != nil {
		limits = append(limits, "collection metrics were not readable: "+merr.Error())
	} else {
		detail["metrics"] = m
	}
	ex, readable, xerr := s.SnapshotExceptions(ctx, sid)
	switch {
	case xerr != nil:
		limits = append(limits, "exceptions could not be read, so their absence is not health: "+xerr.Error())
	case !readable:
		limits = append(limits, "exceptions were not read: this login lacks the DEBUG_SNAPSHOTS permission, so their absence is not health")
	default:
		detail["exception_count"] = len(ex)
		// the kinds that exist, most frequent first: type, occurrences, some devices and the head of the stack trace
		sort.SliceStable(ex, func(i, j int) bool { return ex[i].Occurrences > ex[j].Occurrences })
		var rows []map[string]any
		for i, e := range ex {
			if i == 10 {
				limits = append(limits, fmt.Sprintf("%d kinds of exception; the 10 most frequent are listed", len(ex)))
				break
			}
			devs := e.Devices
			if len(devs) > 10 {
				devs = devs[:10]
			}
			rows = append(rows, map[string]any{"type": e.Type, "occurrences": e.Occurrences, "devices": devs, "devices_total": len(e.Devices), "message": firstLine(e.StackTrace)})
		}
		if len(rows) > 0 {
			detail["exceptions"] = rows
		}
	}
	finding := fmt.Sprintf("Snapshot %s is %s (%s)", sid, sn.State, kindOf(*sn))
	return result.Build(inspectSnapshotsName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits,
		NextActions: []string{"investigate-collection-failure", "inspect-inventory"},
		Evidence:    []result.Evidence{result.NewEvidence(result.EvCollection, "getSnapshot", fwd.SnapshotIDPtr(sn), detail, finding)}})
}

func snapshotRow(sn forward.Snapshot, latestReadable string) map[string]any {
	row := map[string]any{"id": string(sn.ID), "state": sn.State, "kind": kindOf(sn)}
	row["advanced_reachability"] = map[string]any{"state": advancedState(sn)}
	// at is when the data was collected (createdAt); a reprocess changes processedAt but never createdAt, so a week-old snapshot reprocessed today is still a week old
	if t := snapCreated(sn); t != "" {
		row["at"] = t
	}
	if sn.ProcessedAt != "" && sn.ProcessedAt != sn.CreatedAt {
		row["processed_at"] = sn.ProcessedAt
	}
	if sn.TotalDevices > 0 {
		row["devices"] = sn.TotalDevices
	}
	if sn.Note != "" {
		row["note"] = oneLineNote(sn.Note)
	}
	if sn.FavoritedAt != "" {
		row["favorite"] = true
	}
	if sn.ParentSnapshotID != "" {
		row["parent_snapshot_id"] = string(sn.ParentSnapshotID)
	}
	if sn.ChangeSetID != "" {
		row["change_set_id"] = string(sn.ChangeSetID)
	}
	if sn.Message != "" {
		row["message"] = oneLineNote(sn.Message)
	}
	if age, ok := snapAge(sn.CreatedAt); ok {
		row["age_seconds"] = age
	}
	if latestReadable != "" && string(sn.ID) == latestReadable {
		row["latest_readable"] = true
	}
	return row
}

func kindOf(sn forward.Snapshot) string {
	switch {
	case sn.Predicted():
		return "predicted"
	case sn.IsDraft:
		return "draft"
	case sn.ProcessingTrigger != "":
		return "collected (" + sn.ProcessingTrigger + ")"
	}
	return "collected"
}

// snapCreated is when the snapshot's data was collected: createdAt, which a reprocess keeps. Forward orders snapshots, and a backdate reaches "from the snapshot's
// creation instant", by this, so "newest" and "which snapshots a backdate affects" use it, never processedAt.
func snapCreated(sn forward.Snapshot) string {
	if sn.CreatedAt != "" {
		return sn.CreatedAt
	}
	return sn.ProcessedAt
}

// snapTime is the last time the snapshot was processed (createdAt when it never was): what a snapshot that is being worked on right now has been at since.
func snapTime(sn forward.Snapshot) string {
	if sn.ProcessedAt != "" {
		return sn.ProcessedAt
	}
	return sn.CreatedAt
}

// snapAge is the seconds since createdAt. A reprocess keeps the original createdAt, so for a recomputed snapshot this is the age of the snapshot, not of the run.
func snapAge(createdAt string) (int64, bool) {
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return 0, false
	}
	return max(0, int64(time.Since(t).Seconds())), true
}

func finalState(state string) bool {
	switch state {
	case "PROCESSED", "FAILED", "CANCELED", "CANCELLED", "ABORTED":
		return true
	}
	return false
}

// maxProcessingReads bounds the progress reads of one listing (two calls each).
const maxProcessingReads = 5

// nowFunc is the clock for elapsed time (replaced in tests).
var nowFunc = time.Now

const estimateNote = "an estimate from earlier runs of this network, not a promise"

// processingDetail reads Forward's progress for a snapshot that is not in a final state: when processing started (the earliest stage start, because a REPROCESS keeps the snapshot's
// original createdAt), how long it has run, the stage computing now, every stage with its state, and Forward's own estimate of the total. A call that fails or is not served is
// said in the limits, never left out.
func processingDetail(ctx context.Context, s *fwd.Session, sn forward.Snapshot) (map[string]any, []string) {
	out := map[string]any{}
	var limits []string
	prog, err := s.SnapshotProgress(ctx, string(sn.ID))
	if err != nil || prog == nil {
		why := "no answer"
		if err != nil {
			why = err.Error()
		}
		return nil, []string{"processing progress could not be read (GET /api/snapshots/{id}/progress: " + why + "); age_seconds is since createdAt, which a reprocess keeps from the original creation, so it is not the time spent in the current run"}
	}
	var earliest time.Time
	stages := make([]map[string]any, 0, len(prog.Stages))
	for _, st := range prog.Stages {
		row := map[string]any{"stage": st.Stage, "state": st.OperationState}
		if t, ok := st.StartedAt(); ok {
			row["started_at"] = t.UTC().Format(time.RFC3339)
			if earliest.IsZero() || t.Before(earliest) {
				earliest = t
			}
		}
		if st.NumObjects != nil {
			row["objects"] = *st.NumObjects
		}
		if st.OperationState == "COMPUTING" {
			if _, set := out["stage"]; !set {
				out["stage"], out["stage_state"] = st.Stage, st.OperationState
			}
		}
		stages = append(stages, row)
	}
	out["stages"], out["done"] = stages, prog.Done
	if _, set := out["stage"]; !set {
		out["stage"], out["stage_state"] = nil, nil
	}
	if earliest.IsZero() {
		out["started_at"], out["elapsed_seconds"] = nil, nil
		limits = append(limits, "no processing stage has started yet, so there is no start time or elapsed time")
	} else {
		out["started_at"] = earliest.UTC().Format(time.RFC3339)
		out["elapsed_seconds"] = max(int64(nowFunc().Sub(earliest).Seconds()), 0)
		limits = append(limits, "elapsed_seconds is since the earliest stage started, not since createdAt (a reprocess keeps its original createdAt)")
	}
	if est, err := s.SnapshotProcessEstimate(ctx, string(sn.ID)); err != nil {
		limits = append(limits, "Forward's processing-time estimate could not be read (GET /api/snapshots/{id}/processEstimate, a preview endpoint this Forward may not serve: "+err.Error()+"), so there is no estimate_total_seconds")
	} else if est != nil && len(est.StageToDurationMillis) > 0 {
		var total int64
		for _, ms := range est.StageToDurationMillis {
			total += ms
		}
		out["estimate_total_seconds"] = total / 1000
		limits = append(limits, "estimate_total_seconds is the sum of Forward's per-stage durations, "+estimateNote)
	} else {
		limits = append(limits, "Forward gave no processing-time estimate for this snapshot")
	}
	return out, limits
}

// advancedState is the snapshot's advanced reachability state exactly as Forward reports it (the analysis that internet exposure needs). An empty field is said, not
// shown as a state.
func advancedState(sn forward.Snapshot) string {
	if sn.AdvancedReachabilityState == "" {
		return "NOT_REPORTED"
	}
	return sn.AdvancedReachabilityState
}

// advancedMeaning says what a state means for the answers that depend on it. UNPROCESSED is never "still running": Forward reports PROCESSING while it runs.
func advancedMeaning(state string) string {
	const lead = "advanced_reachability.state "
	switch state {
	case forward.AdvancedReachabilityUnprocessed:
		return lead + "UNPROCESSED means advanced reachability was never computed for this snapshot (it is not 'still running': that is PROCESSING), so internet exposure (internet_addressable) is unavailable for it until it is computed; a month-old snapshot that is still UNPROCESSED never ran it. Whether it starts by itself depends on the organization property ADVANCED_REACHABILITY_ANALYSIS (ASYNC starts it after processing; ON_DEMAND, the default, only on request). A reprocess clears it back to UNPROCESSED"
	case forward.AdvancedReachabilityProcessing:
		return lead + "PROCESSING means it is computing now; internet exposure appears once it is PROCESSED"
	case forward.AdvancedReachabilityProcessed:
		return lead + "PROCESSED means it was computed, so internet exposure can be read for this snapshot"
	case forward.AdvancedReachabilityFailed, forward.AdvancedReachabilityCanceled, forward.AdvancedReachabilityTimedOut:
		return lead + state + " is a final state: it will not run again by itself, and asking again is ignored by Forward until the snapshot is reprocessed"
	}
	return lead + state + " is not a state this skill knows; read it as unknown"
}

// readAdvancedStage adds the ADVANCED_REACHABILITY stage of Forward's processing progress (its state, when it started and when it last reported) to the snapshot's
// advanced_reachability item. A failed read is said, never dropped.
func readAdvancedStage(ctx context.Context, s *fwd.Session, sid string, a map[string]any) []string {
	prog, err := s.SnapshotProgress(ctx, sid)
	if err != nil || prog == nil {
		why := "no answer"
		if err != nil {
			why = err.Error()
		}
		return []string{"snapshot " + sid + ": the ADVANCED_REACHABILITY stage could not be read (GET /api/snapshots/{id}/progress: " + why + "), so only the snapshot's own advanced_reachability.state is shown"}
	}
	for _, st := range prog.Stages {
		if st.Stage != "ADVANCED_REACHABILITY" {
			continue
		}
		a["stage_state"] = st.OperationState
		if t, ok := st.StartedAt(); ok {
			a["started_at"] = t.UTC().Format(time.RFC3339)
		}
		if t, ok := st.UpdatedAt(); ok {
			a["updated_at"] = t.UTC().Format(time.RFC3339)
		}
		return nil
	}
	return []string{"snapshot " + sid + ": Forward's progress has no ADVANCED_REACHABILITY stage, so only the snapshot's own advanced_reachability.state is shown"}
}

// advancedLimits reads the stage for the first rows of a listing (one call each) and says what UNPROCESSED means when any listed snapshot has it.
func advancedLimits(ctx context.Context, s *fwd.Session, win []map[string]any, byID map[string]map[string]any, all []forward.Snapshot) []string {
	var limits []string
	state := map[string]string{}
	for _, sn := range all {
		state[string(sn.ID)] = advancedState(sn)
	}
	seen := map[string]bool{}
	unprocessed := false
	for i, r := range win {
		id := str(r["id"])
		if state[id] == forward.AdvancedReachabilityUnprocessed {
			unprocessed = true
		}
		a, ok := byID[id]["advanced_reachability"].(map[string]any)
		if i >= maxProcessingReads || !ok || seen[id] {
			continue
		}
		seen[id] = true
		limits = append(limits, readAdvancedStage(ctx, s, id, a)...)
	}
	if len(win) > maxProcessingReads {
		limits = append(limits, fmt.Sprintf("the ADVANCED_REACHABILITY stage detail is read for the first %d snapshots of a listing; every row carries its advanced_reachability.state, and snapshot_id reads one in full", maxProcessingReads))
	}
	if unprocessed {
		limits = append(limits, advancedMeaning(forward.AdvancedReachabilityUnprocessed))
	}
	return limits
}
