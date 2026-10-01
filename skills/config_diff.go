package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const configDiffName = "compare-device-config"

func init() { Register(configDiffName, compareDeviceConfig) }

type configDiffInput struct {
	NetworkID        string `json:"network_id"`
	BeforeSnapshotID string `json:"before_snapshot_id"`
	AfterSnapshotID  string `json:"after_snapshot_id"`
	Device           string `json:"device"`
	File             string `json:"file"`
	FileType         string `json:"file_type"`
	MaxLines         int    `json:"max_lines"`
	NoRedact         bool   `json:"no_redact"`
}

const (
	defaultDiffLines = 40
	maxDiffLines     = 200
	maxDiffDevices   = 30
)

func compareDeviceConfig(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in configDiffInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if in.FileType == "" {
		in.FileType = "CONFIG"
	}
	if in.MaxLines <= 0 {
		in.MaxLines = defaultDiffLines
	}
	in.MaxLines = min(in.MaxLines, maxDiffLines)
	before, err := s.Snapshot(ctx, in.NetworkID, in.BeforeSnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	after, err := s.Snapshot(ctx, in.NetworkID, in.AfterSnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	cx := fwd.Context(in.NetworkID, after)
	if !fwd.IsReady(before) || !fwd.IsReady(after) {
		return result.NewUnknown(configDiffName, "Both snapshots must be processed to compare them", cx,
			[]string{fmt.Sprintf("before snapshot is %s, after snapshot is %s; nothing was compared", fwd.StateOf(before), fwd.StateOf(after))},
			result.Options{NextActions: []string{"investigate-collection-failure"}})
	}
	var limits []string
	if fwd.IsPredicted(before) || fwd.IsPredicted(after) {
		limits = append(limits, "one snapshot is a prediction: the difference is between a collected state and a prediction")
	}
	changed, err := s.DiffFiles(ctx, in.BeforeSnapshotID, in.AfterSnapshotID, in.FileType)
	if err != nil {
		return result.Result{}, err
	}
	if in.Device == "" {
		return listChangedDevices(changed, in, cx, limits)
	}
	var dev *fwd.FileDiff
	for i := range changed {
		if changed[i].Device == in.Device {
			dev = &changed[i]
		}
	}
	if dev == nil {
		return result.Build(configDiffName, result.OK, fmt.Sprintf("No %s file of %s changed between the snapshots", in.FileType, in.Device), result.Deterministic, cx,
			result.Options{Limits: append(limits, "Forward lists no changed file for this device; a device absent from a snapshot also appears this way (check with inspect-inventory)"),
				NextActions: []string{"inspect-inventory"}, Evidence: []result.Evidence{result.NewEvidence(result.EvConfig, "diffFiles", fwd.SnapshotIDPtr(after),
					map[string]any{"device": in.Device, "file_type": in.FileType, "changed_files": 0}, "no changed files")}})
	}
	return diffOneDevice(ctx, s, in, *dev, cx, limits)
}

func listChangedDevices(changed []fwd.FileDiff, in configDiffInput, cx result.Context, limits []string) (result.Result, error) {
	if len(changed) == 0 {
		return result.Build(configDiffName, result.OK, fmt.Sprintf("No %s files changed between the snapshots", in.FileType), result.Deterministic, cx,
			result.Options{Limits: limits, NextActions: []string{"verify-change"}, Evidence: []result.Evidence{result.NewEvidence(result.EvConfig, "diffFiles",
				cx.SnapshotID, map[string]any{"file_type": in.FileType, "devices_changed": 0}, "0 devices")}})
	}
	rows := []map[string]any{}
	for i, d := range changed {
		if i == maxDiffDevices {
			limits = append(limits, fmt.Sprintf("%d devices changed; the first %d are listed", len(changed), maxDiffDevices))
			break
		}
		names := make([]string, 0, len(d.Files))
		for _, f := range d.Files {
			if n, ok := f.DeviceFileName(); ok {
				names = append(names, n)
			}
		}
		rows = append(rows, map[string]any{"device": d.Device, "files": names, "has_config_change": d.HasConfigChange})
	}
	return result.Build(configDiffName, result.OK, fmt.Sprintf("%d device(s) have changed %s files", len(changed), in.FileType), result.Deterministic, cx,
		result.Options{Limits: limits, NextActions: []string{"verify-change"},
			Evidence: []result.Evidence{result.NewEvidence(result.EvConfig, "diffFiles", cx.SnapshotID, map[string]any{"file_type": in.FileType, "devices_changed": len(changed), "devices": rows},
				fmt.Sprintf("%d devices", len(changed)))}})
}

func diffOneDevice(ctx context.Context, s *fwd.Session, in configDiffInput, dev fwd.FileDiff, cx result.Context, limits []string) (result.Result, error) {
	var pick forward.DiffFile
	found := false
	var offered []string
	for _, f := range dev.Files {
		name, ok := f.DeviceFileName()
		if !ok {
			continue
		}
		offered = append(offered, name)
		if in.File == "" || in.File == name {
			if !found {
				pick, found = f, true
			}
		}
	}
	if !found {
		return result.NewUnknown(configDiffName, fmt.Sprintf("%s has no changed file %q that can be compared", in.Device, in.File), cx,
			append(limits, "changed files that can be compared: "+strings.Join(offered, ", ")), result.Options{})
	}
	if in.File == "" && len(offered) > 1 {
		limits = append(limits, "several files changed ("+strings.Join(offered, ", ")+"); showing the first. Set file to choose.")
	}
	afterName, _ := pick.DeviceFileName()
	beforeName := afterName
	if pick.NameA != "" {
		if n, ok := (forward.DiffFile{Name: pick.NameA}).DeviceFileName(); ok {
			beforeName = n
		}
	}
	a, ta, err := s.DeviceFile(ctx, in.NetworkID, in.Device, beforeName, in.BeforeSnapshotID, maxFileDownload)
	if err != nil && !fwd.NotFound(err) {
		return result.Result{}, err
	}
	b, tb, err := s.DeviceFile(ctx, in.NetworkID, in.Device, afterName, in.AfterSnapshotID, maxFileDownload)
	if err != nil && !fwd.NotFound(err) {
		return result.Result{}, err
	}
	if len(a) == 0 && len(b) == 0 {
		return result.NewUnknown(configDiffName, "Neither side of the file could be read", cx,
			append(limits, "Forward lists the file as changed but both downloads were empty or missing; the file name conversion may not fit this device type"), result.Options{})
	}
	if ta || tb {
		limits = append(limits, fmt.Sprintf("a file is larger than %d MiB; only the first %d MiB of it was compared", maxFileDownload>>20, maxFileDownload>>20))
	}
	added, removed := lineSetDiff(splitLines(a), splitLines(b))
	redacted := 0
	show := func(ls []numbered) []string {
		out := make([]string, 0, len(ls))
		for _, l := range ls {
			t := l.text
			if !in.NoRedact {
				if r, ok := redact(t); ok {
					redacted++
					t = r
				}
			}
			out = append(out, fmt.Sprintf("%d: %s", l.n, clipLine(t)))
		}
		return out
	}
	total := len(added) + len(removed)
	clip := func(ls []numbered) []numbered {
		if len(ls) > in.MaxLines {
			return ls[:in.MaxLines]
		}
		return ls
	}
	detail := map[string]any{"device": in.Device, "file": afterName, "added_count": len(added), "removed_count": len(removed),
		"added": show(clip(added)), "removed": show(clip(removed))}
	if len(added) > in.MaxLines || len(removed) > in.MaxLines {
		limits = append(limits, fmt.Sprintf("%d added and %d removed lines; the first %d of each are shown (raise max_lines, at most %d)", len(added), len(removed), in.MaxLines, maxDiffLines))
	}
	limits = append(limits, "lines are compared as a set, so a line that only moved is not reported; line numbers are from the file where the line appears")
	if redacted > 0 {
		limits = append(limits, fmt.Sprintf("%d shown line(s) had a secret replaced with <redacted>; a changed secret shows as the same text on both sides", redacted))
	}
	finding := fmt.Sprintf("%s %s: %d line(s) added, %d removed", in.Device, afterName, len(added), len(removed))
	if total == 0 {
		finding = fmt.Sprintf("%s %s: no line differs, though Forward lists the file as changed", in.Device, afterName)
		limits = append(limits, "Forward marks the file changed but no line differs: only ordering or whitespace changed, or the change is past the size cap")
	}
	return result.Build(configDiffName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, NextActions: []string{"inspect-device-files", "verify-change"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvConfig, "diffFiles", cx.SnapshotID, detail, finding)}})
}

type numbered struct {
	n    int
	text string
}

func splitLines(b []byte) []numbered {
	if len(b) == 0 {
		return nil
	}
	var out []numbered
	for i, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		out = append(out, numbered{i + 1, strings.TrimRight(l, " \t\r")})
	}
	return out
}

// lineSetDiff returns the lines present in b beyond their count in a (added) and in a beyond b (removed). Blank
// lines are ignored.
func lineSetDiff(a, b []numbered) (added, removed []numbered) {
	count := map[string]int{}
	for _, l := range a {
		if l.text != "" {
			count[l.text]++
		}
	}
	for _, l := range b {
		if l.text == "" {
			continue
		}
		if count[l.text] > 0 {
			count[l.text]--
			continue
		}
		added = append(added, l)
	}
	count = map[string]int{}
	for _, l := range b {
		if l.text != "" {
			count[l.text]++
		}
	}
	for _, l := range a {
		if l.text == "" {
			continue
		}
		if count[l.text] > 0 {
			count[l.text]--
			continue
		}
		removed = append(removed, l)
	}
	return added, removed
}
