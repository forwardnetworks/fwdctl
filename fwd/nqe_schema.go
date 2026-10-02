package fwd

import (
	"context"
	"encoding/json"

	forward "github.com/forwardnetworks/forward-go-sdk"
)

// NQESchema reads the live NQE data model as this organization sees it: filtered by its feature settings and license tier, and with
// network.extensions.<nqe_name> for each of its data files. A read: needs VIEW_NQE_LIBRARY, nothing else.
func (s *Session) NQESchema(ctx context.Context) (json.RawMessage, error) {
	raw, _, err := s.Client.NQE.Schema(ctx)
	return raw, err
}

// NQESchemaAt is NQESchema extended with one snapshot's context: the structure Forward inferred from that snapshot's endpoint and data
// connector responses. snapshotID should be the network's latest processed snapshot (NewestProcessedSnapshot).
func (s *Session) NQESchemaAt(ctx context.Context, snapshotID string) (json.RawMessage, error) {
	raw, _, err := s.Client.NQE.SchemaAt(ctx, snapshotID)
	return raw, err
}

// NewestProcessedSnapshot is the network's current snapshot for schema and query purposes (Snapshots.LatestProcessed).
func (s *Session) NewestProcessedSnapshot(ctx context.Context, networkID string) (*forward.Snapshot, error) {
	sn, _, err := s.Client.Snapshots.LatestProcessed(ctx, networkID)
	return sn, err
}

// MissingPermission reports the org or network permission Forward's error names, if any (forward.MissingPermission).
func MissingPermission(err error) (string, bool) { return forward.MissingPermission(err) }
