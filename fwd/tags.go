package fwd

import (
	"context"
	"sort"

	forward "github.com/forwardnetworks/forward-go-sdk"
)

// DeviceTags lists the network's tag definitions with the devices that carry each.
func (s *Session) DeviceTags(ctx context.Context, networkID string) ([]forward.DeviceTag, error) {
	tags, _, err := s.Client.DeviceTags.List(ctx, networkID, "devices")
	if err == nil {
		s.Annotate(intp(len(tags)), false)
	}
	for i := range tags {
		sort.Strings(tags[i].Devices)
	}
	return tags, err
}

// AddDeviceTags puts existing tags on devices (it takes effect in the network's next snapshot).
func (s *Session) AddDeviceTags(ctx context.Context, networkID string, devices, tags []string) error {
	_, err := s.Client.DeviceTags.AddBatchTo(ctx, networkID, devices, tags)
	return err
}

// RemoveDeviceTags takes tags off devices; the tag definitions stay.
func (s *Session) RemoveDeviceTags(ctx context.Context, networkID string, devices, tags []string) error {
	_, err := s.Client.DeviceTags.RemoveBatchFrom(ctx, networkID, devices, tags)
	return err
}

// EditLinkOverrides applies additions and removals of manual and suppressed links to one snapshot's topology.
func (s *Session) EditLinkOverrides(ctx context.Context, snapshotID string, e forward.TopologyOverridesEdit) error {
	_, err := s.Client.Topology.EditOverrides(ctx, snapshotID, e)
	return err
}

// DeviceTagsAt lists the tag definitions with the devices that carry each as of one snapshot (an empty snapshot id reads the staging instant, as DeviceTags does).
func (s *Session) DeviceTagsAt(ctx context.Context, networkID, snapshotID string) ([]forward.DeviceTag, error) {
	if snapshotID == "" {
		return s.DeviceTags(ctx, networkID)
	}
	tags, _, err := s.Client.DeviceTags.ListAt(ctx, networkID, snapshotID)
	if err == nil {
		s.Annotate(intp(len(tags)), false)
	}
	for i := range tags {
		sort.Strings(tags[i].Devices)
	}
	return tags, err
}

// AddDeviceTagsAt puts existing tags on devices from one snapshot on (an empty snapshot id writes at the staging instant: the next snapshot). The devices must be names in the snapshot.
func (s *Session) AddDeviceTagsAt(ctx context.Context, networkID, snapshotID string, devices, tags []string) error {
	if snapshotID == "" {
		return s.AddDeviceTags(ctx, networkID, devices, tags)
	}
	_, err := s.Client.DeviceTags.AddBatchToAt(ctx, networkID, snapshotID, devices, tags)
	return err
}

// RemoveDeviceTagsAt takes tags off devices from one snapshot on (an empty snapshot id writes at the staging instant).
func (s *Session) RemoveDeviceTagsAt(ctx context.Context, networkID, snapshotID string, devices, tags []string) error {
	if snapshotID == "" {
		return s.RemoveDeviceTags(ctx, networkID, devices, tags)
	}
	_, err := s.Client.DeviceTags.RemoveBatchFromAt(ctx, networkID, snapshotID, devices, tags)
	return err
}
