package fwd

import (
	"context"

	forward "github.com/forwardnetworks/forward-go-sdk"
)

// Links lists a snapshot's physical links as port pairs (each port is "<device> <interface>").
func (s *Session) Links(ctx context.Context, snapshotID string) ([]forward.TopologyLink, error) {
	links, _, err := s.Client.Topology.List(ctx, snapshotID)
	if err == nil {
		s.Annotate(intp(len(links)), false)
	}
	return links, err
}

// Locations lists the network's defined locations (sites).
func (s *Session) Locations(ctx context.Context, networkID string) ([]forward.Location, error) {
	locs, _, err := s.Client.Locations.List(ctx, networkID)
	if err == nil {
		s.Annotate(intp(len(locs)), false)
	}
	return locs, err
}

// Tags lists the network's device tags with the devices that carry them.
func (s *Session) Tags(ctx context.Context, networkID string) ([]forward.DeviceTag, error) {
	tags, _, err := s.Client.DeviceTags.List(ctx, networkID, "devices")
	if err == nil {
		s.Annotate(intp(len(tags)), false)
	}
	return tags, err
}

// Aliases lists a snapshot's aliases (named groups of hosts, devices, interfaces or headers).
func (s *Session) Aliases(ctx context.Context, snapshotID string) ([]forward.Alias, error) {
	a, _, err := s.Client.Aliases.List(ctx, snapshotID)
	if err == nil {
		s.Annotate(intp(len(a)), false)
	}
	return a, err
}
