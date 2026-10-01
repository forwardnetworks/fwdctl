package fwd

import (
	"bytes"
	"context"

	forward "github.com/forwardnetworks/forward-go-sdk"
)

// NotFound reports whether Forward answered 404 (no such device, file or snapshot).
func NotFound(err error) bool { return forwardNotFound(err) }

// DeviceFiles lists the raw files Forward collected for one device.
func (s *Session) DeviceFiles(ctx context.Context, networkID, device, snapshotID string) ([]forward.DeviceFile, error) {
	files, _, err := s.Client.Devices.ListFiles(ctx, networkID, device, snapshotID)
	if err == nil {
		s.Annotate(intp(len(files)), false)
	}
	return files, err
}

// DeviceFile downloads one raw file, at most maxBytes of it. truncated says the file was longer; the transfer
// is stopped at the cap rather than downloaded and discarded.
func (s *Session) DeviceFile(ctx context.Context, networkID, device, file, snapshotID string, maxBytes int) (data []byte, truncated bool, err error) {
	var buf bytes.Buffer
	_, truncated, _, err = s.Client.Devices.DownloadFileHead(ctx, networkID, device, file, snapshotID, int64(maxBytes), &buf)
	if truncated {
		s.Annotate(nil, true)
	}
	return buf.Bytes(), truncated, err
}

// FileDiff is one device's changed files between two snapshots.
type FileDiff struct {
	Device          string
	HasConfigChange *bool
	Files           []forward.DiffFile
}

// DiffFiles lists which devices' collected files changed between two snapshots. fileType is CONFIG, STATE, CUSTOM or "".
func (s *Session) DiffFiles(ctx context.Context, before, after, fileType string) ([]FileDiff, error) {
	list, _, err := s.Client.Diffs.Files(ctx, before, after, fileType)
	if err != nil {
		return nil, err
	}
	out := make([]FileDiff, 0, len(list))
	for _, d := range list {
		out = append(out, FileDiff{Device: d.Name, HasConfigChange: d.HasConfigChange, Files: d.Files})
	}
	s.Annotate(intp(len(out)), false)
	return out, nil
}
