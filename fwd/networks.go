package fwd

import (
	"context"

	forward "github.com/forwardnetworks/forward-go-sdk"
)

// Networks lists every network the login can see.
func (s *Session) Networks(ctx context.Context) ([]forward.Network, error) {
	nets, _, err := s.Client.Networks.List(ctx)
	if err == nil {
		s.Annotate(intp(len(nets)), false)
	}
	return nets, err
}

// CreateWorkspace creates a workspace network under a parent (never under another workspace; Forward refuses).
func (s *Session) CreateWorkspace(ctx context.Context, parentID string, req forward.WorkspaceNetworkRequest) (*forward.Network, error) {
	n, _, err := s.Client.Networks.CreateWorkspace(ctx, parentID, req)
	return n, err
}

// DeleteNetwork deletes a network and returns its last representation. Callers must only pass a workspace they created.
func (s *Session) DeleteNetwork(ctx context.Context, networkID string) error {
	if s.NetworkDeleter != nil {
		return s.NetworkDeleter(ctx, networkID)
	}
	return s.deleteNetworkDirect(ctx, networkID)
}

// AddEndpoints adds endpoints of one type to a network.
func (s *Session) AddEndpoints(ctx context.Context, networkID, endpointType string, eps []forward.Endpoint) error {
	_, err := s.Client.Endpoints.AddBatch(ctx, networkID, endpointType, eps)
	return err
}
