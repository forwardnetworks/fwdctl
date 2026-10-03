package fwd

import (
	"context"

	forward "github.com/forwardnetworks/forward-go-sdk"
)

// Alias reads one alias active at a snapshot; nil with no error means none is active under that name.
func (s *Session) Alias(ctx context.Context, snapshotID, name string) (*forward.Alias, error) {
	a, _, err := s.Client.Aliases.Get(ctx, snapshotID, name)
	return a, err
}

// PutAlias creates an alias or replaces the definition of the one with that name. It applies to the snapshot and every later one.
func (s *Session) PutAlias(ctx context.Context, snapshotID string, b forward.AliasBuilder) (*forward.Alias, error) {
	a, _, err := s.Client.Aliases.Put(ctx, snapshotID, b)
	return a, err
}

// DeactivateAlias ends an alias at a snapshot (and so for every later one); an alias not active there counts as success.
func (s *Session) DeactivateAlias(ctx context.Context, snapshotID, name string) (*forward.Alias, error) {
	a, _, err := s.Client.Aliases.Deactivate(ctx, snapshotID, name)
	return a, err
}
