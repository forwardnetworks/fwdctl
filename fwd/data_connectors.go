package fwd

import (
	"context"

	forward "github.com/forwardnetworks/forward-go-sdk"
)

// DataConnectors lists a network's data connectors (per-network HTTP sources the Collector polls each collection, stored as network.dataConnectors).
func (s *Session) DataConnectors(ctx context.Context, networkID string, opts forward.DataConnectorReadOptions) (*forward.DataConnectors, error) {
	dcs, _, err := s.Client.DataConnectors.List(ctx, networkID, opts)
	return dcs, err
}

// DataConnector reads one connector, or nil if it does not exist.
func (s *Session) DataConnector(ctx context.Context, networkID, name string, opts forward.DataConnectorReadOptions) (*forward.DataConnector, error) {
	dc, _, err := s.Client.DataConnectors.Get(ctx, networkID, name, opts)
	return dc, err
}

// AddDataConnector creates a connector on a network.
func (s *Session) AddDataConnector(ctx context.Context, networkID string, req forward.NewDataConnector) (*forward.DataConnector, error) {
	dc, _, err := s.Client.DataConnectors.Add(ctx, networkID, req)
	return dc, err
}

// UpdateDataConnector changes the stated parts of an existing connector.
func (s *Session) UpdateDataConnector(ctx context.Context, networkID, name string, patch forward.DataConnectorPatch) (*forward.DataConnector, error) {
	dc, _, err := s.Client.DataConnectors.Update(ctx, networkID, name, patch)
	return dc, err
}

// DeleteDataConnector removes a connector; a 404 counts as success.
func (s *Session) DeleteDataConnector(ctx context.Context, networkID, name string) error {
	_, err := s.Client.DataConnectors.Delete(ctx, networkID, name)
	return err
}

// TestDataConnector runs a connectivity test of the stored connector and blocks until it finishes (its result is also stored by Forward as the
// connector's TestResult). A failed test is a result with Error set, not a Go error; give the call enough deadline (Forward's own ceiling is 60s).
func (s *Session) TestDataConnector(ctx context.Context, networkID, name string) (*forward.DataConnectorTestResult, error) {
	res, _, err := s.Client.DataConnectors.Test(ctx, networkID, name)
	return res, err
}
