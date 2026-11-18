package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/skills"
)

const (
	exportDefaultJobs     = 4
	exportMaxJobs         = 16
	exportDefaultTotalMiB = 2048
	exportDeviceRows      = 100000
)

// exportDevicesQuery names the devices of a snapshot (the same list the model has, whatever the vendor).
const exportDevicesQuery = "foreach d in network.devices select {name: d.name}"

// devFileExport is one collected file in the manifest; Status is written, too_large or error.
type devFileExport struct {
	Name          string `json:"name"`
	Command       string `json:"command,omitempty"`
	Path          string `json:"path,omitempty"`
	Bytes         int    `json:"bytes,omitempty"`
	Lines         int    `json:"lines,omitempty"`
	SHA256        string `json:"sha256,omitempty"`
	RedactedLines int    `json:"redacted_lines,omitempty"`
	Status        string `json:"status"`
	Error         string `json:"error,omitempty"`
	listedBytes   int64
}

// devExportDevice is one device: Dir is the folder under the export folder (a safe name; the real name is Device). Status is ok, no_files or list_error.
type devExportDevice struct {
	Device string          `json:"device"`
	Dir    string          `json:"dir,omitempty"`
	Status string          `json:"status"`
	Error  string          `json:"error,omitempty"`
	Files  []devFileExport `json:"files"`
}

var unsafeNameChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// safeName turns a device or file name into one path element: any run of characters outside letters, digits, dot, underscore and hyphen becomes "_", and a name that is empty, "." or ".."
// (or only dots) is made visible. The real name is always kept in the manifest.
func safeName(name string) string {
	s := strings.Trim(unsafeNameChars.ReplaceAllString(name, "_"), " ")
	if len(s) > 120 {
		s = s[:120]
	}
	if strings.Trim(s, ".") == "" {
		s = "_" + s
	}
	return s
}

// uniqueName returns safeName(name), or that with a short hash of the real name when another name already took it (two names can map to one safe name).
func uniqueName(name string, taken map[string]bool) string {
	s := safeName(name)
	if taken[strings.ToLower(s)] {
		h := sha256.Sum256([]byte(name))
		base := s + "-" + hex.EncodeToString(h[:4])
		s = base
		for i := 2; taken[strings.ToLower(s)]; i++ { // a literal name equal to the hashed one: keep going until a free name
			s = fmt.Sprintf("%s-%d", base, i)
		}
	}
	taken[strings.ToLower(s)] = true
	return s
}

func (a *app) deviceFileExportCmd() *cobra.Command {
	var out, snapshot, match string
	var devices []string
	var noRedact, dryRun bool
	var maxMiB, totalMiB, jobs int
	c := &cobra.Command{
		Use:   "export NETWORK_ID --out DIR [--snapshot ID] [--device NAME]... [--files REGEX] [--dry-run] [--no-redact] [--max-mib N] [--max-total-mib N] [--jobs N]",
		Short: "write every collected file of every device (or the devices you name) to a new folder, one folder per device, with a manifest",
		Long: "Writes the files Forward collected for the devices of one snapshot to DIR: DIR/DEVICE/FILE for each file, and DIR/manifest.json. The files come from the device's own file listing, so a platform " +
			"that keeps several (a Palo Alto firewall or Panorama with its configuration and its command outputs, a vendor with extra show output) gets all of them under the names Forward stores them by, and a " +
			"vendor that adds a file type later is covered with no change here. This is the raw collected text (the same files inspect-device-files reads), not the NQE model of the config.\n\n" +
			"Devices: every device of the snapshot (the NQE name list), or only those you name with --device (repeat it). --files REGEX keeps only the files whose NAME matches (for example --files 'config', which also keeps ltm_config.txt and partition_config.txt, or --files 'config|panos|running|startup|candidate|policy'); " +
			"without it every collected file is written, command outputs included, which can be large: run --dry-run first, it lists what would be written and the total size and writes nothing.\n\n" +
			"DIR must not exist yet (an existing path is never touched); folders are created mode 700 and files mode 600. A device or file name is made safe for a path (anything outside letters, digits, dot, underscore and hyphen becomes _, a clash gets a short hash); manifest.json keeps the real names, the path written, size, line count, sha256, and how many lines were redacted.\n\n" +
			"Limits: a file larger than --max-mib (default 64, at most 512) is NOT written (its manifest status is too_large), so a cut file is never mistaken for the whole one; the export refuses to start when the files to write total more than --max-total-mib (default 2048) by the listing, before anything is written. Downloads run --jobs at a time (default 4, at most 16).\n\n" +
			"Redaction: by default a secret value (passwords, keys, SNMP communities and similar) that follows its keyword on the same line (password X, secret X, community X, key X) is replaced with <redacted>, best effort, the same line rules as inspect-device-files. It does NOT see a secret inside an XML or JSON element (for example a PAN-OS <phash> or <password> element), so treat an export of such files as unredacted and review it before sharing; --no-redact writes the files exactly as collected: a RAW export is UNREDACTED and can hold secrets, so treat the folder as sensitive. A session started with FWDCTL_FORCE_REDACT refuses --no-redact.\n\n" +
			"Failures are listed, never hidden: a device with no files, a listing that failed, a file that failed or was too large each appear in the manifest and the summary, and the exit status is 1 (partial) when any did, 0 when every file was written. The last line on stdout is a JSON summary: status, out, snapshot_id, devices, files_written, bytes, redacted_lines, problems, manifest.",
		Example: "  fwdctl device-file export 12345 --out ./configs --dry-run\n  fwdctl device-file export 12345 --out ./configs --files 'config|panos|running|startup|candidate|policy'\n  fwdctl device-file export 12345 --out ./fw --device fw-1 --device fw-2",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			networkID := args[0]
			if out == "" {
				return a.fail("usage: --out DIR is required (a folder that does not exist yet)")
			}
			if maxMiB < 1 || maxMiB > downloadMaxMiB {
				return a.fail("usage: --max-mib is between 1 and %d", downloadMaxMiB)
			}
			if totalMiB < 1 {
				return a.fail("usage: --max-total-mib is at least 1")
			}
			if jobs < 1 || jobs > exportMaxJobs {
				return a.fail("usage: --jobs is between 1 and %d", exportMaxJobs)
			}
			var re *regexp.Regexp
			if match != "" {
				var err error
				if re, err = regexp.Compile(match); err != nil {
					return a.fail("usage: --files is not a valid regular expression: %v", err)
				}
			}
			root, err := filepath.Abs(out)
			if err != nil {
				return a.fail("%v", err)
			}
			if _, err := os.Lstat(root); err == nil {
				return a.fail("%s already exists; nothing was written (choose a new folder, this command never touches an existing path)", root)
			}
			if st, err := os.Stat(filepath.Dir(root)); err != nil || !st.IsDir() {
				return a.fail("the folder %s does not exist; create it first", filepath.Dir(root))
			}
			sess, err := a.session()
			if err != nil {
				return a.fail("%v", err)
			}
			if noRedact && sess.ForceRedact() {
				return a.fail("this session keeps redaction on (FWDCTL_FORCE_REDACT); --no-redact is refused")
			}
			ctx := cmd.Context()
			sid := snapshot
			if sid == "" {
				snap, err := sess.LatestProcessed(ctx, networkID)
				if err != nil {
					return a.fail("%v", err)
				}
				if snap == nil {
					return a.fail("network %s has no processed snapshot to read; nothing was written (give --snapshot to name one)", networkID)
				}
				sid = string(snap.ID)
			}
			names := append([]string(nil), devices...)
			if len(names) == 0 {
				rows, _, truncated, err := sess.RunNQEAll(ctx, networkID, sid, exportDevicesQuery, exportDeviceRows)
				if err != nil {
					return a.fail("the device list of snapshot %s could not be read (%v); nothing was written, name devices with --device to skip the list", sid, err)
				}
				if truncated {
					return a.fail("the snapshot has more than %d devices; nothing was written (name them with --device, or ask for a smaller set)", exportDeviceRows)
				}
				for _, r := range rows {
					if n, ok := r["name"].(string); ok && n != "" {
						names = append(names, n)
					}
				}
			}
			sort.Strings(names)
			names = dedupeSorted(names)
			if len(names) == 0 {
				return a.fail("snapshot %s has no devices; nothing was written", sid)
			}

			plan := listExportFiles(ctx, sess, networkID, sid, names, re, jobs)
			var planned int
			var plannedBytes int64
			for _, d := range plan {
				planned += len(d.Files)
				for _, f := range d.Files {
					plannedBytes += f.listedBytes
				}
			}
			if dryRun {
				return a.printExportSummary(root, sid, plan, planned, plannedBytes, true, 0, 0, 0, "")
			}
			if plannedBytes > int64(totalMiB)<<20 {
				return a.fail("the %d files to write total about %d MiB, more than --max-total-mib %d; nothing was written (narrow it with --files or --device, or raise --max-total-mib; --dry-run lists them)", planned, plannedBytes>>20, totalMiB)
			}
			if planned == 0 {
				return a.fail("no file to write: no device has a collected file%s; nothing was written (--dry-run shows what each device lists)", map[bool]string{true: " matching --files", false: ""}[re != nil])
			}
			if err := os.Mkdir(root, 0o700); err != nil {
				return a.fail("could not create %s: %v", root, err)
			}
			redacted, written, bytes := writeExport(ctx, sess, networkID, sid, root, plan, !noRedact, maxMiB, totalMiB, jobs)
			manifest := map[string]any{"snapshot_id": sid, "network_id": networkID, "redacted": !noRedact, "files_filter": match, "devices": plan}
			mb, _ := json.MarshalIndent(manifest, "", "  ")
			mpath := filepath.Join(root, "manifest.json")
			if err := os.WriteFile(mpath, append(mb, '\n'), 0o600); err != nil {
				return a.fail("the files were written but %s could not be: %v", mpath, err)
			}
			return a.printExportSummary(root, sid, plan, written, bytes, false, redacted, written, bytes, mpath)
		},
	}
	c.Flags().StringVar(&out, "out", "", "the folder to create (must not exist; mode 700)")
	c.Flags().StringVar(&snapshot, "snapshot", "", "snapshot id (default: the newest PROCESSED collected snapshot)")
	c.Flags().StringArrayVar(&devices, "device", nil, "a device to export (repeat for several); default: every device of the snapshot")
	c.Flags().StringVar(&match, "files", "", "keep only the files whose name matches this regular expression (default: every collected file)")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "list what would be written and the total size; write nothing")
	c.Flags().BoolVar(&noRedact, "no-redact", false, "write the files exactly as collected: UNREDACTED, may hold secrets")
	c.Flags().IntVar(&maxMiB, "max-mib", downloadDefaultMiB, fmt.Sprintf("do not write a file larger than this many MiB (1 to %d); it is listed as too_large", downloadMaxMiB))
	c.Flags().IntVar(&totalMiB, "max-total-mib", exportDefaultTotalMiB, "refuse to start when the files to write total more than this many MiB by the listing")
	c.Flags().IntVar(&jobs, "jobs", exportDefaultJobs, fmt.Sprintf("downloads at a time (1 to %d)", exportMaxJobs))
	return c
}

func dedupeSorted(s []string) []string {
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

// listExportFiles lists each device's collected files (jobs at a time), keeps those matching re, and returns the devices in name order; a listing that failed is recorded, not dropped.
func listExportFiles(ctx context.Context, s *fwd.Session, networkID, sid string, names []string, re *regexp.Regexp, jobs int) []devExportDevice {
	devs := make([]devExportDevice, len(names))
	taken := map[string]bool{"manifest.json": true} // a device folder must never take the manifest's name
	for i, n := range names {
		devs[i] = devExportDevice{Device: n, Dir: uniqueName(n, taken), Status: "ok", Files: []devFileExport{}}
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, jobs)
	for i := range devs {
		wg.Add(1)
		sem <- struct{}{}
		go func(d *devExportDevice) {
			defer wg.Done()
			defer func() { <-sem }()
			files, err := s.DeviceFiles(ctx, networkID, d.Device, sid)
			if err != nil {
				d.Status, d.Error = "list_error", err.Error()
				if fwd.NotFound(err) {
					d.Error = "Forward has no such device in this snapshot"
				}
				return
			}
			fileTaken := map[string]bool{}
			sort.Slice(files, func(a, b int) bool { return files[a].Name < files[b].Name })
			for _, f := range files {
				if re != nil && !re.MatchString(f.Name) {
					continue
				}
				d.Files = append(d.Files, devFileExport{Name: f.Name, Command: f.Command, Path: uniqueName(f.Name, fileTaken), listedBytes: int64(f.Bytes), Status: "planned"})
			}
			if len(d.Files) == 0 {
				d.Status = "no_files"
				if len(files) > 0 {
					d.Error = fmt.Sprintf("%d file(s) listed, none matches --files", len(files))
				} else {
					d.Error = "Forward lists no collected file for this device"
				}
			}
		}(&devs[i])
	}
	wg.Wait()
	return devs
}

// writeExport downloads and writes the planned files (jobs at a time) under root/DEVICE/FILE and records each file's outcome in the plan. It returns the redacted line count, the number of files
// written and their bytes.
func writeExport(ctx context.Context, s *fwd.Session, networkID, sid, root string, plan []devExportDevice, redact bool, maxMiB, totalMiB, jobs int) (redacted, written int, bytes int64) {
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, jobs)
	for di := range plan {
		d := &plan[di]
		if len(d.Files) == 0 {
			continue
		}
		dir := filepath.Join(root, d.Dir)
		if err := os.Mkdir(dir, 0o700); err != nil {
			for fi := range d.Files {
				d.Files[fi].Status, d.Files[fi].Error, d.Files[fi].Path = "error", "could not create the device folder: "+err.Error(), ""
			}
			continue
		}
		for fi := range d.Files {
			f := &d.Files[fi]
			rel := filepath.Join(d.Dir, f.Path)
			wg.Add(1)
			sem <- struct{}{}
			go func(dev string, f *devFileExport, rel string) {
				defer wg.Done()
				defer func() { <-sem }()
				name := f.Path
				f.Path = ""
				mu.Lock()
				over := bytes > int64(totalMiB)<<20
				mu.Unlock()
				if over { // the listing's sizes did not add up (a file listed with no size): stop at the limit, and say which files were not written
					f.Status, f.Error = "error", fmt.Sprintf("not written: the files already written passed --max-total-mib %d", totalMiB)
					return
				}
				data, truncated, err := s.DeviceFile(ctx, networkID, dev, f.Name, sid, maxMiB<<20)
				switch {
				case err != nil:
					f.Status, f.Error = "error", err.Error()
					return
				case truncated:
					f.Status, f.Error = "too_large", fmt.Sprintf("larger than %d MiB, not written; raise --max-mib (at most %d)", maxMiB, downloadMaxMiB)
					return
				}
				n := 0
				if redact {
					data, n = skills.RedactText(data)
				}
				path := filepath.Join(root, filepath.Dir(rel), name)
				fh, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
				if err != nil {
					f.Status, f.Error = "error", "could not create the file: "+err.Error()
					return
				}
				if _, werr := fh.Write(data); werr != nil {
					fh.Close()
					os.Remove(path)
					f.Status, f.Error = "error", "writing failed, the partial file was removed: "+werr.Error()
					return
				}
				if err := fh.Close(); err != nil {
					os.Remove(path)
					f.Status, f.Error = "error", "closing failed, the file was removed: "+err.Error()
					return
				}
				sum := sha256.Sum256(data)
				lines := 0
				if len(data) > 0 {
					lines = strings.Count(string(data), "\n")
					if data[len(data)-1] != '\n' {
						lines++
					}
				}
				f.Status, f.Path, f.Bytes, f.Lines, f.SHA256, f.RedactedLines = "written", rel, len(data), lines, hex.EncodeToString(sum[:]), n
				mu.Lock()
				redacted += n
				written++
				bytes += int64(len(data))
				mu.Unlock()
			}(d.Device, f, rel)
		}
	}
	wg.Wait()
	return redacted, written, bytes
}

// printExportSummary prints the last-line JSON and returns the exit status: 0 when every planned file was written (or on a dry run with nothing wrong), 1 (partial) otherwise.
func (a *app) printExportSummary(root, sid string, plan []devExportDevice, files int, bytes int64, dry bool, redacted, written int, writtenBytes int64, manifest string) error {
	problems := []map[string]any{} // an empty list, never null
	for _, d := range plan {
		if d.Status != "ok" {
			problems = append(problems, map[string]any{"device": d.Device, "status": d.Status, "error": d.Error})
		}
		for _, f := range d.Files {
			if f.Status == "error" || f.Status == "too_large" {
				problems = append(problems, map[string]any{"device": d.Device, "file": f.Name, "status": f.Status, "error": f.Error})
			}
		}
	}
	status := "ok"
	if len(problems) > 0 {
		status = "partial"
	}
	res := map[string]any{"status": status, "out": root, "snapshot_id": sid, "devices": len(plan), "problems": problems}
	unknown := 0
	for _, d := range plan {
		for _, f := range d.Files {
			if f.listedBytes == 0 {
				unknown++
			}
		}
	}
	if unknown > 0 {
		res["files_with_unknown_size"] = unknown // the listing gave no size, so --max-total-mib could not count them
	}
	if dry {
		res["dry_run"] = true
		res["files_planned"], res["bytes_planned"] = files, bytes
		listing := make([]map[string]any, 0, len(plan))
		for _, d := range plan {
			fs := make([]map[string]any, 0, len(d.Files))
			for _, f := range d.Files {
				fs = append(fs, map[string]any{"name": f.Name, "bytes": f.listedBytes, "command": f.Command})
			}
			listing = append(listing, map[string]any{"device": d.Device, "status": d.Status, "files": fs})
		}
		res["plan"] = listing
	} else {
		res["files_written"], res["bytes"], res["redacted_lines"], res["manifest"] = written, writtenBytes, redacted, manifest
	}
	b, _ := json.Marshal(res)
	fmt.Fprintln(a.out, string(b))
	if status != "ok" {
		return exitCode(1)
	}
	return nil
}
