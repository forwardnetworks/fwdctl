package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Self-update: `fwdctl update` replaces the running binary with the newest release; a once-a-day notice tells you when one exists; and
// FWDCTL_AUTO_UPDATE=1 applies it by itself. The release is read from GitHub (FWDCTL_UPDATE_API overrides, for tests and mirrors), the
// archive for this OS and architecture is downloaded, and it is checked against the release's SHA256SUMS before anything is replaced.
// A private repository needs a token (GITHUB_TOKEN or GH_TOKEN) to read releases; a public one does not.

const defaultUpdateAPI = "https://api.github.com/repos/forwardnetworks/fwdctl"

type releaseInfo struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"url"` // the API address of the asset, readable with a token
	} `json:"assets"`
}

type updater struct {
	api    string
	token  string
	client *http.Client
	goos   string
	goarch string
}

func newUpdater() *updater {
	api := os.Getenv("FWDCTL_UPDATE_API")
	if api == "" {
		api = defaultUpdateAPI
	}
	tok := os.Getenv("GITHUB_TOKEN")
	if tok == "" {
		tok = os.Getenv("GH_TOKEN")
	}
	return &updater{api: strings.TrimRight(api, "/"), token: tok, client: &http.Client{Timeout: 60 * time.Second}, goos: runtime.GOOS, goarch: runtime.GOARCH}
}

func (u *updater) get(ctx context.Context, url, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	if u.token != "" {
		req.Header.Set("Authorization", "Bearer "+u.token)
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound && u.token == "" {
		return nil, errors.New("the release could not be read (404): the repository may be private; set GITHUB_TOKEN to read it")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 200<<20))
}

// release reads one release: the latest, or the one tagged.
func (u *updater) release(ctx context.Context, tag string) (*releaseInfo, error) {
	url := u.api + "/releases/latest"
	if tag != "" {
		url = u.api + "/releases/tags/" + tag
	}
	b, err := u.get(ctx, url, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	var r releaseInfo
	if err := json.Unmarshal(b, &r); err != nil || r.TagName == "" {
		return nil, fmt.Errorf("the release answer was not understood: %v", err)
	}
	return &r, nil
}

func (r *releaseInfo) asset(name string) string {
	for _, a := range r.Assets {
		if a.Name == name {
			return a.URL
		}
	}
	return ""
}

// archiveName is the release archive for this machine.
func (u *updater) archiveName(tag string) string {
	ext := ".tar.gz"
	if u.goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("fwdctl_%s_%s_%s%s", tag, u.goos, u.goarch, ext)
}

// parseVersion reads vMAJOR.MINOR.PATCH[-pre]; ok is false for anything else (a dev build).
func parseVersion(v string) (nums [3]int, pre string, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	core, pre, _ := strings.Cut(v, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return nums, "", false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nums, "", false
		}
		nums[i] = n
	}
	return nums, pre, true
}

// newer reports whether candidate is a later release than current. A pre-release is earlier than its release.
func newer(candidate, current string) bool {
	a, ap, aok := parseVersion(candidate)
	b, bp, bok := parseVersion(current)
	if !aok || !bok {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return bp != "" && ap == ""
}

// verify checks a file's SHA-256 against the line for its name in a SHA256SUMS file.
func verify(sums []byte, name string, data []byte) error {
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	for _, l := range strings.Split(string(sums), "\n") {
		f := strings.Fields(l)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			if strings.EqualFold(f[0], got) {
				return nil
			}
			return fmt.Errorf("the checksum of %s does not match SHA256SUMS: refusing to install it", name)
		}
	}
	return fmt.Errorf("%s is not listed in SHA256SUMS", name)
}

// extractBinary pulls the fwdctl executable out of a release archive.
func extractBinary(archive []byte, goos string) ([]byte, error) {
	want := "fwdctl"
	if goos == "windows" {
		want = "fwdctl.exe"
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if filepath.Base(f.Name) == want {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(rc)
			}
		}
		return nil, errors.New("the archive holds no " + want)
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, errors.New("the archive holds no " + want)
		}
		if err != nil {
			return nil, err
		}
		if filepath.Base(h.Name) == want {
			return io.ReadAll(tr)
		}
	}
}

// install replaces exe with the new bytes. On Windows a running executable cannot be overwritten but can be renamed, so the old one
// is moved aside first; elsewhere the new file is renamed over it, which is atomic.
func install(exe string, bin []byte, goos string) error {
	tmp := exe + ".new"
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		return err
	}
	if goos == "windows" {
		old := exe + ".old"
		_ = os.Remove(old)
		if err := os.Rename(exe, old); err != nil {
			_ = os.Remove(tmp)
			return err
		}
		if err := os.Rename(tmp, exe); err != nil {
			_ = os.Rename(old, exe)
			return err
		}
		return nil
	}
	if err := os.Rename(tmp, exe); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// updateTo downloads, verifies and installs a release over exe. It reports the tag installed.
func (u *updater) updateTo(ctx context.Context, rel *releaseInfo, exe string) (string, error) {
	name := u.archiveName(rel.TagName)
	au, su := rel.asset(name), rel.asset("SHA256SUMS")
	if au == "" || su == "" {
		return "", fmt.Errorf("release %s has no %s (or no SHA256SUMS): there is no build for %s/%s", rel.TagName, name, u.goos, u.goarch)
	}
	archive, err := u.get(context.Background(), au, "application/octet-stream")
	if err != nil {
		return "", err
	}
	sums, err := u.get(ctx, su, "application/octet-stream")
	if err != nil {
		return "", err
	}
	if err := verify(sums, name, archive); err != nil {
		return "", err
	}
	bin, err := extractBinary(archive, u.goos)
	if err != nil {
		return "", err
	}
	if err := install(exe, bin, u.goos); err != nil {
		return "", fmt.Errorf("could not replace %s: %w (is it writable? a system install may need sudo)", exe, err)
	}
	return rel.TagName, nil
}

// updateCmd is `fwdctl update [--check] [--version vX.Y.Z] [--force]`.
func updateCmd(args []string, stdout, stderr io.Writer) int {
	check, force, tag := false, false, ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--check":
			check = true
		case "--force":
			force = true
		case "--version":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "usage: fwdctl update [--check] [--version vX.Y.Z] [--force]")
				return usage
			}
			i++
			tag = args[i]
		default:
			fmt.Fprintln(stderr, "usage: fwdctl update [--check] [--version vX.Y.Z] [--force]")
			return usage
		}
	}
	u := newUpdater()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	rel, err := u.release(ctx, tag)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	_, _, devBuild := parseVersion(version)
	switch {
	case !devBuild && !force:
		fmt.Fprintf(stderr, "this is a development build (%s); an update would replace it with %s. Use --force to do that.\n", version, rel.TagName)
		return 1
	case !force && tag == "" && !newer(rel.TagName, version):
		fmt.Fprintf(stdout, "fwdctl %s is up to date (latest release %s)\n", version, rel.TagName)
		return 0
	case !force && tag != "" && !newer(rel.TagName, version):
		fmt.Fprintf(stderr, "%s is not newer than %s; use --force to install it anyway\n", rel.TagName, version)
		return 1
	}
	if check {
		fmt.Fprintf(stdout, "fwdctl %s is available (this is %s); run: %s\n", rel.TagName, version, updateHint())
		return 2
	}
	if brewManaged() {
		fmt.Fprintf(stderr, "fwdctl was installed by Homebrew, which owns this file: run `brew update && brew upgrade fwdctl` (a tap carries each release shortly after it ships)\n")
		return 1
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	got, err := u.updateTo(ctx, rel, exe)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "updated fwdctl %s -> %s (%s)\n", version, got, exe)
	return 0
}

// brewManaged reports whether the running binary lives in a Homebrew prefix (Cellar, Homebrew, linuxbrew). Brew owns such a file: replacing it
// in place would leave brew's records wrong and the next brew upgrade or cleanup would undo it.
func brewManaged() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	for _, m := range []string{"/Cellar/", "/Homebrew/", "/linuxbrew/"} {
		if strings.Contains(exe, m) {
			return true
		}
	}
	return false
}

// updateHint is the command that updates this install.
func updateHint() string {
	if brewManaged() {
		return "brew upgrade fwdctl"
	}
	return "fwdctl update"
}

// updateNotice is the once-a-day line on stderr that says a newer release exists. It never runs for a development build, in a script
// (stderr is not a terminal), for the language server, or when FWDCTL_NO_UPDATE_CHECK is set, and it gives up quickly if the network is slow.
func updateNotice(cmd string, stderr *os.File) {
	switch cmd {
	case "lsp", "update", "version", "--version", "help", "-h", "--help", "docs", "completion":
		return
	}
	if os.Getenv("FWDCTL_NO_UPDATE_CHECK") != "" {
		return
	}
	if _, _, ok := parseVersion(version); !ok {
		return
	}
	if st, err := stderr.Stat(); err != nil || st.Mode()&os.ModeCharDevice == 0 {
		return
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return
	}
	file := filepath.Join(dir, "fwdctl", "update-check.json")
	var c struct {
		Checked time.Time `json:"checked"`
		Latest  string    `json:"latest"`
	}
	if b, err := os.ReadFile(file); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	if time.Since(c.Checked) > 24*time.Hour {
		u := newUpdater()
		u.client.Timeout = 1500 * time.Millisecond
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if rel, err := u.release(ctx, ""); err == nil {
			c.Latest = rel.TagName
		}
		c.Checked = time.Now()
		_ = os.MkdirAll(filepath.Dir(file), 0o755)
		b, _ := json.Marshal(c)
		_ = os.WriteFile(file, b, 0o600)
	}
	if newer(c.Latest, version) {
		fmt.Fprintf(stderr, "fwdctl %s is available (this is %s). Run: %s\n", c.Latest, version, updateHint())
	}
}

// autoUpdate applies a newer release before the command runs, when FWDCTL_AUTO_UPDATE=1. A failure is only a warning: the command still runs.
func autoUpdate(cmd string, stderr io.Writer) {
	if os.Getenv("FWDCTL_AUTO_UPDATE") != "1" {
		return
	}
	switch cmd {
	case "lsp", "update", "version", "--version", "help", "-h", "--help", "docs", "completion":
		return
	}
	if _, _, ok := parseVersion(version); !ok {
		return
	}
	if brewManaged() {
		return
	}
	u := newUpdater()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	rel, err := u.release(ctx, "")
	if err != nil || !newer(rel.TagName, version) {
		return
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return
	}
	if got, err := u.updateTo(ctx, rel, exe); err != nil {
		fmt.Fprintf(stderr, "fwdctl: automatic update to %s failed: %v\n", rel.TagName, err)
	} else {
		fmt.Fprintf(stderr, "fwdctl: updated %s -> %s (takes effect on the next run)\n", version, got)
	}
}
