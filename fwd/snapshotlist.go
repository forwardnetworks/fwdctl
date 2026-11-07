package fwd

import (
	"context"

	forward "github.com/forwardnetworks/forward-go-sdk"
)

// Snapshots lists every snapshot of a network, predicted ones included, newest first as Forward returns them.
func (s *Session) Snapshots(ctx context.Context, networkID string) ([]forward.Snapshot, error) {
	list, _, err := s.Client.Snapshots.List(ctx, networkID, withPredicted())
	if err == nil {
		s.Annotate(intp(len(list)), false)
	}
	return list, err
}

// withPredicted asks the snapshot listing for predicted snapshots too: from Forward 26.11 they are left out unless asked for (an older
// build ignores the flag and lists them anyway).
func withPredicted() forward.SnapshotListOptions {
	yes := true
	return forward.SnapshotListOptions{IncludePredicted: &yes}
}
