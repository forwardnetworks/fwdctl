package skills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// snapshotExport writes one snapshot (or a subset of its devices) to a ZIP file on this machine; snapshotImport uploads ZIP files as a new snapshot of a network. Neither changes an
// existing snapshot. Export writes a file of network configuration, so it never overwrites, creates the file readable by its owner only, and an obfuscation key comes from a
// secret, never the input.
func snapshotExport(ctx context.Context, s *fwd.Session, in editSnapshotInput) (result.Result, error) {
	if in.NetworkID == "" || in.SnapshotID == "" {
		return result.Result{}, fmt.Errorf("%w: export needs network_id and snapshot_id", ErrInvalidInput)
	}
	var def struct {
		Path           string   `json:"path"`
		OnlyConfig     bool     `json:"only_config"`
		IncludeDevices []string `json:"include_devices"`
		ExcludeDevices []string `json:"exclude_devices"`
		ObfuscateNames bool     `json:"obfuscate_names"`
	}
	if err := decodeDefinition(in.Definition, &def, "path (a new .zip file), only_config, include_devices, exclude_devices, obfuscate_names (the obfuscation key comes from secret_file or secret_env)"); err != nil {
		return result.Result{}, err
	}
	if def.Path == "" {
		return result.Result{}, fmt.Errorf("%w: export needs definition.path, the new file to write", ErrInvalidInput)
	}
	if len(def.IncludeDevices) > 0 && len(def.ExcludeDevices) > 0 {
		return result.Result{}, fmt.Errorf("%w: give include_devices or exclude_devices, not both", ErrInvalidInput)
	}
	withKey := in.SecretFile != "" || in.SecretEnv != ""
	if def.ObfuscateNames && !withKey {
		return result.Result{}, fmt.Errorf("%w: obfuscate_names needs the obfuscation key: secret_file or secret_env", ErrInvalidInput)
	}
	if _, err := os.Stat(def.Path); err == nil {
		return result.Result{}, fmt.Errorf("%w: %s already exists; export never overwrites a file", ErrInvalidInput, def.Path)
	}
	if st, err := os.Stat(filepath.Dir(def.Path)); err != nil || !st.IsDir() {
		return result.Result{}, fmt.Errorf("%w: the directory of %s does not exist", ErrInvalidInput, def.Path)
	}
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	sn, err := s.Snapshot(ctx, in.NetworkID, in.SnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	if sn == nil {
		return result.NewUnknown(editSnapshotName, fmt.Sprintf("The network holds no snapshot %s", in.SnapshotID), cx,
			[]string{"no such snapshot in this network; nothing was written"}, result.Options{NextActions: []string{"inspect-snapshots"}})
	}
	cx = fwd.Context(in.NetworkID, sn)
	mode := result.ModeDryRun
	if in.Apply {
		mode = result.ModeApplied
	}
	after := map[string]any{"path": def.Path, "only_config": def.OnlyConfig, "include_devices": def.IncludeDevices, "exclude_devices": def.ExcludeDevices, "obfuscated": withKey}
	ch := result.Change{Action: "export_snapshot", Target: fmt.Sprintf("snapshot %s to %s", in.SnapshotID, def.Path), After: after, Reversible: true, Undo: "delete the file (nothing changes in Forward)"}
	limits := []string{"the file holds the network's configuration: keep it private" + map[bool]string{true: "; it is obfuscated with the key you gave, and un-obfuscating needs that key", false: "; it is NOT obfuscated"}[withKey],
		"Forward may reject a snapshot that is still processing"}
	ev := func(extra map[string]any) []result.Evidence {
		d := map[string]any{"action": "export", "mode": mode, "snapshot_id": in.SnapshotID, "state": sn.State, "after": after}
		for k, v := range extra {
			d[k] = v
		}
		return []result.Evidence{result.NewEvidence(result.EvState, "editSnapshot", cx.SnapshotID, d, "")}
	}
	if !in.Apply {
		return result.Build(editSnapshotName, result.OK, fmt.Sprintf("Dry run: would write snapshot %s to %s. Nothing was written; run again with apply=true", in.SnapshotID, def.Path), result.Deterministic, cx,
			result.Options{Mode: mode, Changes: []result.Change{ch}, Evidence: ev(nil), Limits: limits, NextActions: []string{"inspect-snapshots"}})
	}
	opts := forward.SnapshotExportOptions{IncludeDevices: def.IncludeDevices, ExcludeDevices: def.ExcludeDevices, ObfuscateNames: def.ObfuscateNames, Timeout: -1}
	if def.OnlyConfig {
		opts.Only = "CONFIG"
	}
	if withKey {
		sec, err := fwd.ReadSecret(in.SecretFile, in.SecretEnv)
		if err != nil {
			return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
		opts.ObfuscationKey = strings.TrimSpace(sec.Reveal())
	}
	tmp, err := os.CreateTemp(filepath.Dir(def.Path), ".export-*.part")
	if err != nil {
		return result.Result{}, fmt.Errorf("%w: cannot write beside %s: %v", ErrInvalidInput, def.Path, err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		return result.Result{}, err
	}
	h := sha256.New()
	n, _, err := s.Client.Snapshots.Export(ctx, in.SnapshotID, opts, io.MultiWriter(tmp, h))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return result.Result{}, fmt.Errorf("the export failed and no file was kept: %w", err)
	}
	if err := os.Link(tmp.Name(), def.Path); err != nil {
		return result.Result{}, fmt.Errorf("%w: %s appeared while exporting or cannot be created: %v", ErrInvalidInput, def.Path, err)
	}
	ch.Applied = true
	ch.After = map[string]any{"path": def.Path, "bytes": n, "sha256": hex.EncodeToString(h.Sum(nil))}
	return result.Build(editSnapshotName, result.OK, fmt.Sprintf("Wrote snapshot %s to %s (%d bytes)", in.SnapshotID, def.Path, n), result.Deterministic, cx,
		result.Options{Mode: mode, Changes: []result.Change{ch}, Evidence: ev(map[string]any{"bytes": n, "sha256": hex.EncodeToString(h.Sum(nil))}), Limits: limits, NextActions: []string{"inspect-snapshots"}})
}

func snapshotImport(ctx context.Context, s *fwd.Session, in editSnapshotInput) (result.Result, error) {
	if in.NetworkID == "" {
		return result.Result{}, fmt.Errorf("%w: import needs network_id", ErrInvalidInput)
	}
	if in.SnapshotID != "" {
		return result.Result{}, fmt.Errorf("%w: import creates a new snapshot; snapshot_id does not apply", ErrInvalidInput)
	}
	var def struct {
		Files                []string `json:"files"`
		ExcludeFailedDevices bool     `json:"exclude_failed_devices"`
		SkipProcessing       bool     `json:"skip_processing"`
	}
	if err := decodeDefinition(in.Definition, &def, "files (one or more .zip paths), exclude_failed_devices, skip_processing"); err != nil {
		return result.Result{}, err
	}
	if len(def.Files) == 0 {
		return result.Result{}, fmt.Errorf("%w: import needs definition.files", ErrInvalidInput)
	}
	var total int64
	for _, f := range def.Files {
		st, err := os.Stat(f)
		if err != nil || st.IsDir() {
			return result.Result{}, fmt.Errorf("%w: %s is not a readable file", ErrInvalidInput, f)
		}
		total += st.Size()
	}
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	mode := result.ModeDryRun
	if in.Apply {
		mode = result.ModeApplied
	}
	after := map[string]any{"files": def.Files, "bytes": total, "note": in.Note, "exclude_failed_devices": def.ExcludeFailedDevices, "skip_processing": def.SkipProcessing}
	ch := result.Change{Action: "import_snapshot", Target: fmt.Sprintf("%d file(s) into network %s", len(def.Files), in.NetworkID), After: after, Reversible: true,
		Undo: "delete the new snapshot (edit-snapshot action delete, confirm = its id)"}
	limits := []string{"this adds a snapshot; it changes no existing one. Several files are merged into one snapshot", "the snapshot then processes in Forward: inspect-snapshots shows its state"}
	ev := func(extra map[string]any) []result.Evidence {
		d := map[string]any{"action": "import", "mode": mode, "after": after}
		for k, v := range extra {
			d[k] = v
		}
		return []result.Evidence{result.NewEvidence(result.EvState, "editSnapshot", nil, d, "")}
	}
	if !in.Apply {
		return result.Build(editSnapshotName, result.OK, fmt.Sprintf("Dry run: would upload %d file(s) (%d bytes) as a new snapshot of network %s. Nothing was uploaded; run again with apply=true", len(def.Files), total, in.NetworkID), result.Deterministic, cx,
			result.Options{Mode: mode, Changes: []result.Change{ch}, Evidence: ev(nil), Limits: limits, NextActions: []string{"inspect-snapshots"}})
	}
	var files []forward.SnapshotUploadFile
	for _, f := range def.Files {
		fh, err := os.Open(f)
		if err != nil {
			return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
		defer fh.Close()
		files = append(files, forward.SnapshotUploadFile{Name: filepath.Base(f), Reader: fh})
	}
	snap, _, err := s.Client.Snapshots.Upload(ctx, in.NetworkID, files, forward.SnapshotUploadOptions{Note: in.Note, ExcludeFailedDevices: def.ExcludeFailedDevices, SkipSnapshotProcessing: def.SkipProcessing, Timeout: -1})
	if err != nil {
		return result.Result{}, fmt.Errorf("the import failed, nothing is known to have been added: %w", err)
	}
	ch.Applied = true
	ch.After = map[string]any{"snapshot_id": string(snap.ID), "state": snap.State}
	ch.Undo = fmt.Sprintf("delete snapshot %s (edit-snapshot action delete, confirm = %s)", snap.ID, snap.ID)
	return result.Build(editSnapshotName, result.OK, fmt.Sprintf("Imported %d file(s) as snapshot %s (state %s)", len(def.Files), snap.ID, snap.State), result.Deterministic, fwd.Context(in.NetworkID, snap),
		result.Options{Mode: mode, Changes: []result.Change{ch}, Evidence: ev(map[string]any{"snapshot_id": string(snap.ID), "state": snap.State}), Limits: limits, NextActions: []string{"inspect-snapshots"}})
}
