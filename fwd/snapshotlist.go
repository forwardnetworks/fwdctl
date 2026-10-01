package fwd

import (
	"context"

	forward "github.com/forwardnetworks/forward-go-sdk"
)

// Snapshots lists every snapshot of a network, newest first as Forward returns them.
func (s *Session) Snapshots(ctx context.Context, networkID string) ([]forward.Snapshot, error) {
	list, _, err := s.Client.Snapshots.List(ctx, networkID, forward.SnapshotListOptions{})
	if err == nil {
		s.Annotate(intp(len(list)), false)
	}
	return list, err
}
