package skills

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// changeSetHistory is verify-change view history: a change set's saved versions (note, who and when, devices edited), its summary (base snapshot, tags, linked ServiceNow requests) and the
// directory it is filed in. Read only. The routes are Preview and need the change-set permission; the directory is read separately and left out, with a limit saying so, when the
// organization has directories switched off or the read is refused.
func changeSetHistory(ctx context.Context, s *fwd.Session, in verifyChangeInput) (result.Result, error) {
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	if in.ChangeSetID == "" {
		return result.Result{}, fmt.Errorf("%w: view history needs change_set_id (view list shows the ids)", ErrInvalidInput)
	}
	commits, _, err := s.Client.Predict.ListCommits(ctx, in.NetworkID, in.ChangeSetID)
	if err != nil {
		if r, ok := denialResult(verifyChangeName, err, cx, "reading the change set's versions"); ok {
			return r, nil
		}
		return result.Result{}, err
	}
	var limits []string
	rows := make([]map[string]any, 0, len(commits))
	for _, c := range commits {
		if in.CommitID != "" && string(c.CommitID) != in.CommitID {
			continue
		}
		rows = append(rows, map[string]any{"commit_id": string(c.CommitID), "note": c.Note, "committed_at": c.CommittedAt, "committed_by_id": string(c.CommittedByID), "devices_edited": len(c.DeviceToChanges), "tags": c.Tags, "base_snapshot_id": string(c.SnapshotID)})
	}
	sort.SliceStable(rows, func(i, j int) bool { return fmt.Sprint(rows[i]["committed_at"]) > fmt.Sprint(rows[j]["committed_at"]) })
	if in.CommitID != "" && len(rows) == 0 {
		return result.NewUnknown(verifyChangeName, fmt.Sprintf("Change set %s has no version %s", in.ChangeSetID, in.CommitID), cx,
			[]string{"no version with that commit id was listed; run view history without commit_id for the list"}, result.Options{})
	}
	detail := map[string]any{"change_set_id": in.ChangeSetID, "versions_total": len(commits)}
	shown, om := result.Cap(rows, 50, "versions", "pass commit_id for one version")
	detail["versions"] = shown
	if len(commits) > 0 {
		head := commits[0]
		for _, c := range commits {
			if c.CommittedAt > head.CommittedAt {
				head = c
			}
		}
		detail["newest_version"] = string(head.CommitID)
	}
	if sum, _, e := s.Client.Predict.GetChangeSetSummary(ctx, in.NetworkID, in.ChangeSetID); e == nil && sum != nil {
		detail["summary"] = map[string]any{"name": sum.Name, "description": sum.Description, "base_snapshot_id": string(sum.SnapshotID), "tags": sum.Tags, "servicenow_change_requests": len(sum.ServiceNowChangeRequests)}
	} else if e != nil {
		limits = append(limits, "the change set summary could not be read ("+e.Error()+")")
	}
	if dirs, _, e := s.Client.Predict.ListChangeSetDirectories(ctx, in.NetworkID); e == nil {
		where := "/"
		for path, ids := range dirs {
			for _, id := range ids {
				if string(id) == in.ChangeSetID {
					where = path
				}
			}
		}
		detail["directory"] = where
	} else if errors.Is(e, forward.ErrFeatureGated) || errors.Is(e, forward.ErrPermissionDenied) {
		limits = append(limits, "the change set's directory is not shown: directories are off for this organization or the login may not read them")
	} else {
		limits = append(limits, "the change set's directory could not be read ("+e.Error()+")")
	}
	limits = append(limits, "versions are what a change set's commit saved; an uncommitted draft is not a version (view describe shows the draft); the routes are Preview, source-read, not run on a live Forward")
	finding := fmt.Sprintf("Change set %s has %d saved version(s)", in.ChangeSetID, len(commits))
	return result.Build(verifyChangeName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, Omitted: om, NextActions: []string{"edit-change-set"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvPredict, "changeSetHistory", nil, detail, finding)}})
}

// changeSetConfig is verify-change view config: one device's configuration in a change set, the draft (default) or a saved version (commit_id), as lines.
func changeSetConfig(ctx context.Context, s *fwd.Session, in verifyChangeInput) (result.Result, error) {
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	if in.ChangeSetID == "" || in.Device == "" {
		return result.Result{}, fmt.Errorf("%w: view config needs change_set_id and device", ErrInvalidInput)
	}
	var text string
	var err error
	which := "the uncommitted draft"
	if in.CommitID != "" {
		which = "version " + in.CommitID
		text, _, err = s.Client.Predict.GetCommittedConfig(ctx, in.NetworkID, in.ChangeSetID, in.CommitID, in.Device)
	} else {
		text, _, err = s.Client.Predict.GetDraftConfig(ctx, in.NetworkID, in.ChangeSetID, in.Device)
	}
	if err != nil {
		if r, ok := denialResult(verifyChangeName, err, cx, "reading the change set's device configuration"); ok {
			return r, nil
		}
		return result.Result{}, err
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	shown, om := result.Cap(lines, 300, "config lines", "the first 300 lines are shown")
	detail := map[string]any{"change_set_id": in.ChangeSetID, "device": in.Device, "source": which, "lines_total": len(lines), "config": shown}
	finding := fmt.Sprintf("Configuration of %s in %s of change set %s: %d line(s)", in.Device, which, in.ChangeSetID, len(lines))
	return result.Build(verifyChangeName, result.OK, finding, result.Deterministic, cx, result.Options{Omitted: om, NextActions: []string{"edit-change-set"},
		Limits:   []string{"this is the configuration Predict will use for the device after the change set's edits; a device the change set does not edit has no draft or version of its own (Forward answers 404). Preview route, source-read, not run on a live Forward"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvPredict, "changeSetConfig", nil, detail, finding)}})
}

// predictedAffected is verify-change view affected: the devices a prediction changed, from Forward's protocol-modelling job on a predicted snapshot.
func predictedAffected(ctx context.Context, s *fwd.Session, in verifyChangeInput) (result.Result, error) {
	cx := result.Context{NetworkID: in.NetworkID, State: "predicted"}
	if in.AfterSnapshotID == "" {
		return result.Result{}, fmt.Errorf("%w: view affected needs after_snapshot_id (a predicted snapshot; view describe lists them)", ErrInvalidInput)
	}
	names, _, err := s.Client.Predict.AffectedDevices(ctx, in.AfterSnapshotID)
	if err != nil {
		if errors.Is(err, forward.ErrNoAffectedDevices) {
			return result.NewUnknown(verifyChangeName, fmt.Sprintf("Snapshot %s has no list of affected devices", in.AfterSnapshotID), cx,
				[]string{"Forward keeps this only for a predicted snapshot whose modelling has finished; a base snapshot has none. Nothing was inferred"}, result.Options{NextActions: []string{"verify-change"}})
		}
		if r, ok := denialResult(verifyChangeName, err, cx, "reading the devices a prediction affected"); ok {
			return r, nil
		}
		return result.Result{}, err
	}
	sort.Strings(names)
	shown, om := result.Cap(names, 200, "affected devices", "the first 200 are listed")
	finding := fmt.Sprintf("Prediction %s affected %d device(s)", in.AfterSnapshotID, len(names))
	return result.Build(verifyChangeName, result.OK, finding, result.Deterministic, cx, result.Options{Omitted: om, NextActions: []string{"verify-change"},
		Limits:   []string{"affected means the protocol-modelling job re-computed the device, which can be wider than the devices the change set edits; Preview route, source-read, not run on a live Forward"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvPredict, "predictedAffected", nil, map[string]any{"snapshot_id": in.AfterSnapshotID, "count": len(names), "devices": shown}, finding)}})
}
