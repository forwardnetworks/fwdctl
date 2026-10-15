package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	forward "github.com/forwardnetworks/forward-go-sdk"
	"github.com/spf13/cobra"
)

// collectorPackages are the client package types a user login can download from Forward. The collector installer is the regular package (linux: the .sh installer, windows: .exe, osx: .dmg);
// the headless ones are plain archives (.tar.gz, .zip) for a manual install, and fwd.app serves them where an on-prem appserver does not.
var collectorPackages = map[string]forward.ClientPackageType{
	"headless-linux": forward.ClientPackageHeadlessLinux, "headless-windows": forward.ClientPackageHeadlessWindowsX64,
	"linux": forward.ClientPackageLinux, "windows": forward.ClientPackageWindowsX64, "osx": forward.ClientPackageOSX,
}

type packageDownloader func(ctx context.Context, t forward.ClientPackageType, w io.Writer) (*forward.ClientPackage, error)

func (a *app) collectorCmd() *cobra.Command {
	c := parent(&cobra.Command{
		Use: "collector", GroupID: "skills", Short: "get the Forward collector software for a host",
		Example: "  fwdctl collector download --out ~/collector",
		Long: "Downloads Forward's collector software with the logged-in credential, so a new collector host needs no browser. Registering the collector (its username and key) and attaching it to a network " +
			"are separate steps: Settings > Collectors in Forward, then `fwdctl run edit-network` with object collector action assign.",
	})
	c.AddCommand(a.collectorDownloadCmd())
	return c
}

func (a *app) collectorDownloadCmd() *cobra.Command {
	var kind, out string
	var force bool
	var timeout time.Duration
	c := &cobra.Command{
		Use: "download [--type linux] [--out DIR]", Short: "download the collector installer from Forward and print its name, size and SHA-256",
		Long: "Streams the collector installer for --type (linux: fwd-unix-VERSION.sh, windows: .exe, osx: .dmg; default linux; headless-linux and headless-windows are the archives for a manual install) from the connected Forward into --out " +
			"(default the current directory), named as Forward names it. It needs a user login or API token: a collector's own credential is refused (403). An existing file is not overwritten without --force. " +
			"The last line on stdout is one JSON object: file, bytes, sha256, type, forward_release. Compare the sha256 with the one Forward publishes before you run the package.",
		Example: "  fwdctl collector download --out ~/collector\n  fwdctl collector download --type windows --out ./dl",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			t, ok := collectorPackages[kind]
			if !ok {
				return a.fail("--type %q is not one of linux, windows, osx, headless-linux, headless-windows", kind)
			}
			sess, err := a.session()
			if err != nil {
				return a.fail("%v", err)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			dl := func(ctx context.Context, t forward.ClientPackageType, w io.Writer) (*forward.ClientPackage, error) {
				p, _, err := sess.Client.SoftwareCentral.DownloadClientPackage(ctx, t, w)
				return p, err
			}
			release := ""
			if v, verr := sess.Version(ctx); verr == nil {
				release = v.Release
			}
			if code := downloadCollector(ctx, dl, t, out, force, release, a.out, a.err); code != 0 {
				return a.exit(code)
			}
			return nil
		},
	}
	c.Flags().StringVar(&kind, "type", "linux", "package: linux (.sh installer), windows (.exe), osx (.dmg), headless-linux (.tar.gz) or headless-windows (.zip)")
	c.Flags().StringVar(&out, "out", ".", "directory to write the package into (created if missing)")
	c.Flags().BoolVar(&force, "force", false, "overwrite a file of the same name")
	c.Flags().DurationVar(&timeout, "timeout", 30*time.Minute, "give up after this long")
	return c
}

// downloadCollector writes the package to a temporary file in dir and renames it to Forward's file name only when the download finished, so a cut connection never leaves a half package under the real name.
func downloadCollector(ctx context.Context, dl packageDownloader, t forward.ClientPackageType, dir string, force bool, release string, stdout, stderr io.Writer) int {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 3
	}
	tmp, err := os.CreateTemp(dir, ".fwd-collector-*.part")
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 3
	}
	defer os.Remove(tmp.Name())
	pkg, err := dl(ctx, t, tmp)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		fmt.Fprintf(stderr, "error: the download failed (the login must be a user or API token, not a collector's): %v\n", err)
		return 3
	}
	name := filepath.Base(strings.TrimSpace(pkg.FileName))
	if name == "." || name == string(filepath.Separator) || name == "" {
		name = "fwd-collector-" + strings.ToLower(string(t))
	}
	final := filepath.Join(dir, name)
	if _, serr := os.Stat(final); serr == nil && !force {
		fmt.Fprintf(stderr, "error: %s exists; use --force to replace it\n", final)
		return 64
	}
	if err := os.Rename(tmp.Name(), final); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 3
	}
	_ = os.Chmod(final, 0o644)
	line, _ := json.Marshal(map[string]any{"file": final, "bytes": pkg.Size, "sha256": pkg.SHA256, "type": string(t), "forward_release": release})
	fmt.Fprintf(stdout, "%s\n", line)
	return 0
}
