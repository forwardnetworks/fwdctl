package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const startCollectionName = "edit-collection"

func init() { Register(startCollectionName, startCollection) }

type startCollectionInput struct {
	NetworkID string `json:"network_id"`
	Action    string `json:"action"`
	TaskID    string `json:"task_id"`
	StopMode  string `json:"stop_mode"`
	Apply     bool   `json:"apply"`
}

// startCollection starts a collection for a network, or stops a running one. A collection opens management sessions to every
// device, so it is a dry run by default, refuses when one is already running or the attached collector is down, and returns the
// task id at once instead of waiting: the snapshot arrives later, and inspect-collection-status says where it stands.
func startCollection(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in startCollectionInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if in.Action == "" {
		in.Action = "start"
	}
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	switch in.Action {
	case "start":
		return startNow(ctx, s, in, cx)
	case "stop":
		return stopNow(ctx, s, in, cx)
	}
	return result.Result{}, fmt.Errorf("%w: action must be start or stop", ErrInvalidInput)
}

func startNow(ctx context.Context, s *fwd.Session, in startCollectionInput, cx result.Context) (result.Result, error) {
	var limits []string
	state := map[string]any{}
	p, perr := s.CollectionProgress(ctx, in.NetworkID)
	switch {
	case perr != nil:
		limits = append(limits, "whether a collection is already running could not be read: "+perr.Error())
	case p != nil && p.InProgress:
		return refuseStart(in, cx, "a collection is already running for this network", map[string]any{"progress": map[string]any{"finished": p.Finished, "total": p.Total, "active": p.Active}})
	default:
		state["running"] = false
	}
	if a, err := s.CollectorAttachment(ctx, in.NetworkID); err != nil {
		limits = append(limits, "the collector attachment could not be read: "+err.Error())
	} else if a != nil && (a.IsSet || a.CollectorName != "" || a.ConnectionStatus != "") {
		state["collector"] = map[string]any{"name": a.CollectorName, "connection": a.ConnectionStatus}
		if a.ConnectionStatus != "" && !strings.EqualFold(a.ConnectionStatus, "CONNECTED") {
			return refuseStart(in, cx, fmt.Sprintf("the attached collector %q is %s, so a collection would fail", a.CollectorName, a.ConnectionStatus), state)
		}
	}
	devs, derr := s.ClassicDevices(ctx, in.NetworkID)
	eps, eerr := s.Endpoints(ctx, in.NetworkID)
	switch {
	case derr != nil || eerr != nil:
		limits = append(limits, fmt.Sprintf("what is configured to collect could not be read (%v, %v), so a start may collect nothing", derr, eerr))
	case len(devs)+len(eps) == 0:
		return refuseStart(in, cx, "nothing is configured to collect (no collection devices or endpoints); a start would collect nothing", state)
	default:
		state["targets"] = map[string]any{"devices": len(devs), "endpoints": len(eps)}
	}
	if tasks, err := s.RecentTasks(ctx, in.NetworkID, 20); err == nil {
		if last := lastFinished(tasks); last != nil {
			state["last_finished"] = map[string]any{"task": string(last.ID), "status": last.Status, "finished_at": last.FinishedAt}
		}
	}
	ch := result.Change{Action: "start_collection", Target: "network " + in.NetworkID, Before: "no collection running", After: "a collection running", Reversible: true,
		Undo: "run edit-collection with action=stop and the task id, apply=true (CANCEL ends it without a snapshot); a snapshot already made stays"}
	ev := func(mode string, extra map[string]any) []result.Evidence {
		d := map[string]any{"mode": mode, "state": state}
		for k, v := range extra {
			d[k] = v
		}
		return []result.Evidence{result.NewEvidence(result.EvCollection, "startCollection", nil, d, "")}
	}
	next := []string{"inspect-collection", "inspect-snapshots"}
	if !in.Apply {
		return result.Build(startCollectionName, result.OK, "Dry run: a collection can start now (none is running"+collectorPhrase(state)+"). It opens management sessions to every device in scope. Nothing was started; run again with apply=true to start it",
			result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Evidence: ev(result.ModeDryRun, nil), NextActions: next, Limits: limits})
	}
	id, active, err := s.StartCollection(ctx, in.NetworkID)
	if err != nil {
		return result.Result{}, fmt.Errorf("starting the collection failed, nothing is known to have started: %w", err)
	}
	if active {
		return refuseStart(in, cx, "Forward reports a collection already in progress", state)
	}
	ch.Applied = true
	ch.After = map[string]any{"task_id": id}
	ch.Undo = fmt.Sprintf("run edit-collection with action=stop, task_id=%s and apply=true (CANCEL ends it without a snapshot); a snapshot already made stays", id)
	return result.Build(startCollectionName, result.OK, fmt.Sprintf("Started collection task %s. It runs in the background; read inspect-collection-status for progress and inspect-snapshots for the snapshot it makes", id),
		result.Deterministic, cx, result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(result.ModeApplied, map[string]any{"task_id": id}), NextActions: next, Limits: limits})
}

func collectorPhrase(state map[string]any) string {
	if c, ok := state["collector"].(map[string]any); ok {
		return fmt.Sprintf("; collector %v is %v", c["name"], c["connection"])
	}
	return ""
}

// refuseStart is a start that was not made. It is unknown, not failed: nothing about the network was found wrong, and nothing was changed.
func refuseStart(in startCollectionInput, cx result.Context, why string, state map[string]any) (result.Result, error) {
	return result.Build(startCollectionName, result.Unknown, "No collection was started: "+why, result.NoBasis, cx, result.Options{
		Evidence:    []result.Evidence{result.NewEvidence(result.EvCollection, "startCollection", nil, map[string]any{"refused": why, "state": state}, "")},
		Limits:      []string{"refused: " + why + "; nothing was changed"},
		NextActions: []string{"inspect-collection"}})
}

func stopNow(ctx context.Context, s *fwd.Session, in startCollectionInput, cx result.Context) (result.Result, error) {
	if in.TaskID == "" {
		return result.Result{}, fmt.Errorf("%w: stop needs task_id", ErrInvalidInput)
	}
	mode := strings.ToUpper(in.StopMode)
	if mode == "" {
		mode = "CANCEL"
	}
	if mode != "CANCEL" && mode != "SKIP" {
		return result.Result{}, fmt.Errorf("%w: stop_mode must be CANCEL or SKIP", ErrInvalidInput)
	}
	t, err := s.CollectorTask(ctx, in.TaskID)
	if err != nil {
		return result.Result{}, err
	}
	if t == nil {
		return result.NewUnknown(startCollectionName, fmt.Sprintf("There is no collector task %s; nothing was changed", in.TaskID), cx,
			[]string{"no such task (ids are matched exactly)"}, result.Options{NextActions: []string{"inspect-collection"}})
	}
	if in.NetworkID != "" && string(t.NetworkID) != "" && string(t.NetworkID) != in.NetworkID {
		return result.NewUnknown(startCollectionName, fmt.Sprintf("Task %s belongs to another network; nothing was changed", in.TaskID), cx,
			[]string{"the task is not this network's, so it was not touched"}, result.Options{})
	}
	if !taskActive(t.Status) {
		return result.NewUnknown(startCollectionName, fmt.Sprintf("Task %s is already %s; there is nothing to stop", in.TaskID, t.Status), cx,
			[]string{"the task has finished; nothing was changed"}, result.Options{NextActions: []string{"inspect-snapshots"}})
	}
	effect := "ends it without making a snapshot"
	if mode == "SKIP" {
		effect = "ends it and makes a snapshot from what was already collected"
	}
	ch := result.Change{Action: "stop_collection_" + strings.ToLower(mode), Target: "task " + in.TaskID, Before: t.Status, After: "ended", Reversible: false}
	ev := func(m string) []result.Evidence {
		return []result.Evidence{result.NewEvidence(result.EvCollection, "stopCollection", nil, map[string]any{"task_id": in.TaskID, "status_before": t.Status, "stop_mode": mode, "mode": m}, "")}
	}
	if !in.Apply {
		return result.Build(startCollectionName, result.OK, fmt.Sprintf("Dry run: would stop task %s (%s), which %s. A stopped collection cannot be resumed; start a new one. Nothing was changed; run again with apply=true", in.TaskID, t.Status, effect),
			result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Evidence: ev(result.ModeDryRun), NextActions: []string{"inspect-collection"}})
	}
	if err := s.StopCollectorTask(ctx, in.TaskID, mode, "stopped by fwdctl edit-collection"); err != nil {
		return result.Result{}, fmt.Errorf("stopping the task failed, nothing is known to have changed: %w", err)
	}
	ch.Applied = true
	return result.Build(startCollectionName, result.OK, fmt.Sprintf("Stopped task %s (%s): %s", in.TaskID, mode, effect),
		result.Deterministic, cx, result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(result.ModeApplied), NextActions: []string{"inspect-collection", "inspect-snapshots"}})
}

func taskActive(status string) bool {
	switch strings.ToUpper(status) {
	case forward.CollectorTaskQueued, forward.CollectorTaskRunning:
		return true
	}
	return false
}
