package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/skills"
)

const (
	downloadDefaultMiB = 64
	downloadMaxMiB     = 512
)

func (a *app) deviceFileCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "device-file",
		Short: "download one collected device file whole, to a new local file",
		Long: "inspect-device-files shows a file in parts and cuts a long line at 400 characters. This command writes the whole file to a path you name, with no line cap, " +
			"for the cases where the cut hides what you need (a very long list, a long policy line).",
		Example: "  fwdctl device-file download 12345 core-1 config.txt --out ./core-1.config.txt",
	}
	c.AddCommand(a.deviceFileDownloadCmd())
	return c
}

func (a *app) deviceFileDownloadCmd() *cobra.Command {
	var out, snapshot string
	var noRedact bool
	var maxMiB int
	c := &cobra.Command{
		Use:   "download NETWORK_ID DEVICE FILE --out PATH [--snapshot ID] [--no-redact] [--max-mib N]",
		Short: "write one collected device file, whole, to a new local file",
		Long: "Downloads one collected file of a device (the names inspect-device-files mode list shows, for example config.txt) and writes it, whole and with no line cap, to --out.\n\n" +
			"--out must be a path that does not exist yet: an existing file is never overwritten. The file is created readable by you only (mode 600). --snapshot is the snapshot to read; without it the newest PROCESSED collected snapshot is used.\n\n" +
			"Size limit: --max-mib (default 64, at most 512). A file larger than the limit is NOT written, so a cut file is never mistaken for the whole one; raise --max-mib and run it again.\n\n" +
			"Redaction: by default a secret value (passwords, keys, SNMP communities and similar) is replaced with <redacted> on every line, the same best-effort rules as inspect-device-files; the output counts the lines changed. " +
			"--no-redact writes the file exactly as collected: a RAW download is UNREDACTED and can hold secrets, so treat the file as sensitive and delete it when done. " +
			"A session started with FWDCTL_FORCE_REDACT refuses --no-redact.\n\n" +
			"The last line on stdout is a JSON object: status, path, bytes, lines, redacted_lines, redacted, snapshot_id and sha256 (of what was written).",
		Example: "  fwdctl device-file download 12345 core-1 config.txt --out ./core-1.config.txt\n  fwdctl device-file download 12345 core-1 config.txt --snapshot 67890 --out ./core-1.raw.txt --no-redact",
		Args:    cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			networkID, device, file := args[0], args[1], args[2]
			if out == "" {
				return a.fail("usage: --out PATH is required (a path that does not exist yet)")
			}
			if maxMiB < 1 || maxMiB > downloadMaxMiB {
				return a.fail("usage: --max-mib is between 1 and %d", downloadMaxMiB)
			}
			path, err := filepath.Abs(out)
			if err != nil {
				return a.fail("%v", err)
			}
			if _, err := os.Lstat(path); err == nil {
				return a.fail("%s already exists; nothing was written (choose a new path, this command never overwrites)", path)
			}
			if st, err := os.Stat(filepath.Dir(path)); err != nil || !st.IsDir() {
				return a.fail("the folder %s does not exist; create it first", filepath.Dir(path))
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
			data, truncated, err := sess.DeviceFile(ctx, networkID, device, file, sid, maxMiB<<20)
			if err != nil {
				if fwd.NotFound(err) {
					return a.fail("Forward has no file %q for device %q in snapshot %s (inspect-device-files mode list shows the names); nothing was written", file, device, sid)
				}
				return a.fail("%v", err)
			}
			if truncated {
				return a.fail("the file is larger than %d MiB, so it was not written; raise --max-mib (at most %d) and run again", maxMiB, downloadMaxMiB)
			}
			redactedLines := 0
			if !noRedact {
				data, redactedLines = skills.RedactText(data)
			}
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				return a.fail("could not create %s: %v", path, err)
			}
			if _, werr := f.Write(data); werr != nil {
				f.Close()
				os.Remove(path)
				return a.fail("writing %s failed, the partial file was removed: %v", path, werr)
			}
			if err := f.Close(); err != nil {
				os.Remove(path)
				return a.fail("closing %s failed, the file was removed: %v", path, err)
			}
			sum := sha256.Sum256(data)
			lines := 0
			if len(data) > 0 {
				lines = strings.Count(string(data), "\n")
				if data[len(data)-1] != '\n' {
					lines++
				}
			}
			res := map[string]any{"status": "ok", "path": path, "bytes": len(data), "lines": lines, "redacted": !noRedact, "redacted_lines": redactedLines,
				"snapshot_id": sid, "sha256": hex.EncodeToString(sum[:])}
			if noRedact {
				res["warning"] = "raw download: this file is unredacted and can hold secrets"
			}
			b, _ := json.Marshal(res)
			fmt.Fprintln(a.out, string(b))
			return nil
		},
	}
	c.Flags().StringVar(&out, "out", "", "the file to create (must not exist; mode 600)")
	c.Flags().StringVar(&snapshot, "snapshot", "", "snapshot id (default: the newest PROCESSED collected snapshot)")
	c.Flags().BoolVar(&noRedact, "no-redact", false, "write the file exactly as collected: UNREDACTED, may hold secrets")
	c.Flags().IntVar(&maxMiB, "max-mib", downloadDefaultMiB, fmt.Sprintf("refuse a file larger than this many MiB (1 to %d); nothing is written then", downloadMaxMiB))
	return c
}
