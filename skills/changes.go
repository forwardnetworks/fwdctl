package skills

import (
	"context"
	"errors"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
)

// changeInput is the selector the change skills share: a Predict change set, or two snapshots.
type changeInput struct {
	NetworkID        string `json:"network_id"`
	ChangeSetID      string `json:"change_set_id"`
	RunPredict       bool   `json:"run_predict"`
	BeforeSnapshotID string `json:"before_snapshot_id"`
	AfterSnapshotID  string `json:"after_snapshot_id"`
	ConnectivitySecs int    `json:"connectivity_timeout_seconds"`
}

// resolvedChange is the before/after pair a change skill compares.
type resolvedChange struct {
	BeforeID string
	Before   *forward.Snapshot
	After    *forward.Snapshot
	Limits   []string
}

var errNoSelector = errors.New("give change_set_id, or both before_snapshot_id and after_snapshot_id")

// resolveChange finds the before and after snapshots. After may be nil (no prediction yet) with a limit
// saying why; the caller reports that as unknown, never as "no change".
func resolveChange(ctx context.Context, s *fwd.Session, in changeInput) (resolvedChange, error) {
	var rc resolvedChange
	switch {
	case in.ChangeSetID != "":
		base, err := s.BaseSnapshotID(ctx, in.NetworkID, in.ChangeSetID)
		if err != nil {
			return rc, err
		}
		rc.BeforeID = base
		rc.After, err = s.PredictedSnapshot(ctx, in.NetworkID, in.ChangeSetID, in.RunPredict)
		if err != nil {
			return rc, err
		}
		if rc.After == nil {
			rc.Limits = append(rc.Limits, "no processed predicted snapshot for this change set; pass run_predict to compute one")
		}
	case in.BeforeSnapshotID != "" && in.AfterSnapshotID != "":
		rc.BeforeID = in.BeforeSnapshotID
		var err error
		rc.After, err = s.Snapshot(ctx, in.NetworkID, in.AfterSnapshotID)
		if err != nil {
			return rc, err
		}
	default:
		return rc, errNoSelector
	}
	if rc.BeforeID != "" {
		var err error
		rc.Before, err = s.Snapshot(ctx, in.NetworkID, rc.BeforeID)
		if err != nil {
			return rc, err
		}
	}
	return rc, nil
}
