package fwd

import (
	"context"

	forward "github.com/forwardnetworks/forward-go-sdk"
)

// DataFiles lists the organization's data files (uploaded datasets a network's queries can join, as network.extensions.<nqeName>).
func (s *Session) DataFiles(ctx context.Context) ([]forward.DataFile, error) {
	fs, _, err := s.Client.DataFiles.List(ctx)
	return fs, err
}

// DataFileSchema reads what Forward infers from a STORED data file: its format, NQE type, and warnings or errors. A read: Forward needs no
// credential for this and stores nothing.
func (s *Session) DataFileSchema(ctx context.Context, name string) (*forward.DataFileInference, error) {
	inf, _, err := s.Client.DataFiles.Schema(ctx, name)
	return inf, err
}

// AddDataFile uploads a new data file, attached to no network yet.
func (s *Session) AddDataFile(ctx context.Context, req forward.DataFileCreateRequest, content []byte) (*forward.DataFile, error) {
	f, _, err := s.Client.DataFiles.Add(ctx, req, content)
	return f, err
}

// AttachDataFile attaches an existing data file to a network: its later snapshots carry it.
func (s *Session) AttachDataFile(ctx context.Context, networkID, name string) error {
	_, err := s.Client.DataFiles.AddToNetwork(ctx, networkID, name)
	return err
}

// DetachDataFile removes a data file from a network: its later snapshots stop carrying it (network.extensions.<nqeName>.status reads MISSING).
func (s *Session) DetachDataFile(ctx context.Context, networkID, name string) error {
	_, err := s.Client.DataFiles.RemoveFromNetwork(ctx, networkID, name)
	return err
}

// DataFilesForNetwork lists the data-file names attached to one network.
func (s *Session) DataFilesForNetwork(ctx context.Context, networkID string) ([]string, error) {
	names, _, err := s.Client.DataFiles.ListForNetwork(ctx, networkID)
	return names, err
}

// DeleteDataFile removes a data file from the organization's library and so from every network that carries it; a file that does not exist counts as success. The content is not kept.
func (s *Session) DeleteDataFile(ctx context.Context, name string) error {
	_, err := s.Client.DataFiles.Delete(ctx, name)
	return err
}
