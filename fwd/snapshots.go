package fwd

import (
	"context"
	"errors"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/result"
)

const stateProcessed = "PROCESSED"

// IsReady reports whether a snapshot is fully processed.
func IsReady(s *forward.Snapshot) bool { return s != nil && s.State == stateProcessed }

// IsPredicted reports whether a snapshot was computed by Predict. Some NQE signals (live counters, for
// one) come back empty on these whatever the network state, so a zero there is not a pass.
func IsPredicted(s *forward.Snapshot) bool { return s != nil && s.Predicted() }

// StateOf names a snapshot's state, or "missing".
func StateOf(s *forward.Snapshot) string {
	if s == nil {
		return "missing"
	}
	return s.State
}

func processedAt(s forward.Snapshot) string {
	if s.ProcessedAt != "" {
		return s.ProcessedAt
	}
	return s.CreatedAt
}

// LatestProcessed is the newest PROCESSED snapshot that is not a prediction (a prediction is not a state the
// network was ever in). nil means "nothing to read", NOT "empty network".
func (s *Session) LatestProcessed(ctx context.Context, networkID string) (*forward.Snapshot, error) {
	snap, _, err := s.Client.Snapshots.LatestProcessed(ctx, networkID)
	if errors.Is(err, forward.ErrNoSnapshots) {
		s.Annotate(intp(0), false)
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.Annotate(intp(1), false)
	return snap, nil
}

// Snapshot finds one snapshot by id, or nil when the network holds no such snapshot.
func (s *Session) Snapshot(ctx context.Context, networkID, snapshotID string) (*forward.Snapshot, error) {
	list, _, err := s.Client.Snapshots.List(ctx, networkID, forward.SnapshotListOptions{})
	if err != nil {
		return nil, err
	}
	s.Annotate(intp(len(list)), false)
	for i := range list {
		if string(list[i].ID) == snapshotID {
			return &list[i], nil
		}
	}
	return nil, nil
}

// Context builds the `context` block every result carries.
func Context(networkID string, s *forward.Snapshot) result.Context {
	c := result.Context{NetworkID: networkID, State: "current"}
	if s == nil {
		return c
	}
	id := string(s.ID)
	c.SnapshotID = &id
	if t := processedAt(*s); t != "" {
		c.SnapshotTime = &t
	}
	if IsPredicted(s) {
		c.State = "predicted"
	}
	return c
}

// SnapshotID is the context's snapshot id, or "".
func SnapshotID(c result.Context) string {
	if c.SnapshotID == nil {
		return ""
	}
	return *c.SnapshotID
}

// SnapshotIDPtr returns the id as an evidence-source pointer, or nil.
func SnapshotIDPtr(s *forward.Snapshot) *string {
	if s == nil {
		return nil
	}
	id := string(s.ID)
	return &id
}

// SetSnapshotNote replaces a snapshot's note and returns the snapshot as Forward now holds it.
func (s *Session) SetSnapshotNote(ctx context.Context, snapshotID, note string) (*forward.Snapshot, error) {
	sn, _, err := s.Client.Snapshots.SetNote(ctx, snapshotID, note)
	return sn, err
}

// ReprocessSnapshot recomputes a snapshot's derived data from what was collected, and returns the state change Forward reports.
func (s *Session) ReprocessSnapshot(ctx context.Context, snapshotID string) (*forward.SnapshotStateTransition, error) {
	tr, _, err := s.Client.Snapshots.Reprocess(ctx, snapshotID)
	return tr, err
}

// SnapshotProgress reads how far a snapshot's processing has got (GET /api/snapshots/{id}/progress): one entry per stage with its state and, once started, when.
func (s *Session) SnapshotProgress(ctx context.Context, snapshotID string) (*forward.SnapshotProgress, error) {
	p, _, err := s.Client.Snapshots.Progress(ctx, snapshotID)
	return p, err
}

// SnapshotProcessEstimate reads Forward's per-stage processing-time estimate for a snapshot (GET /api/snapshots/{id}/processEstimate; a preview endpoint).
func (s *Session) SnapshotProcessEstimate(ctx context.Context, snapshotID string) (*forward.SnapshotProcessEstimate, error) {
	e, _, err := s.Client.Snapshots.ProcessEstimate(ctx, snapshotID)
	return e, err
}

// ComputeAdvancedReachability asks Forward to compute advanced reachability (the flow analysis internet exposure is read from) for a processed snapshot: POST
// /api/snapshots/{id}?action=computeAdvancedReachability. Forward answers as soon as it accepts and runs the work asynchronously.
func (s *Session) ComputeAdvancedReachability(ctx context.Context, snapshotID string) error {
	_, err := s.Client.Snapshots.ComputeAdvancedReachability(ctx, snapshotID)
	return err
}
