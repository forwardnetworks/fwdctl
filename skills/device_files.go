package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const deviceFilesName = "inspect-device-files"

func init() { Register(deviceFilesName, inspectDeviceFiles) }

type deviceFilesInput struct {
	NetworkID  string `json:"network_id"`
	SnapshotID string `json:"snapshot_id"`
	Device     string `json:"device"`
	Mode       string `json:"mode"`
	File       string `json:"file"`
	Pattern    string `json:"pattern"`
	Context    int    `json:"context"`
	StartLine  int    `json:"start_line"`
	MaxLines   int    `json:"max_lines"`
	MaxMatches int    `json:"max_matches"`
	NoRedact   bool   `json:"no_redact"`
}

// A raw file is text a harness pays for by the token, so both what is downloaded and what is returned are capped.
const (
	maxFileDownload   = 4 << 20
	defaultReadLines  = 150
	maxReadLines      = 500
	defaultMatches    = 10
	maxMatches        = 50
	maxContextLines   = 5
	maxRenderedLength = 400
)

// redactor hides the value after a secret-bearing keyword. It is best effort: the answer says so.
var redactor = regexp.MustCompile(`(?i)\b(password|secret|encrypted-password|authentication-key|pre-shared-key|preshared-key|private-key|key-string|md5|sha256|token|passphrase)(\s+(?:\d\s+)?|\s*[:=]\s*)("[^"]*"|\S+)`)

// An SNMP community is a secret, a BGP community is routing policy. Only redact "community" where it is SNMP: a
// line that says snmp, or a Junos "community NAME {" block opener.
var (
	snmpCommunity = regexp.MustCompile(`(?i)(\bcommunity\s+)(\S+)`)
	junosSNMPOpen = regexp.MustCompile(`^\s*community\s+\S+\s*\{\s*$`)
)

func redact(line string) (string, bool) {
	out := redactor.ReplaceAllString(line, "${1}${2}<redacted>")
	if strings.Contains(strings.ToLower(line), "snmp") || junosSNMPOpen.MatchString(line) {
		out = snmpCommunity.ReplaceAllString(out, "${1}<redacted>")
	}
	return out, out != line
}

func clipLine(l string) string {
	if len(l) > maxRenderedLength {
		return l[:maxRenderedLength] + "…"
	}
	return l
}

func inspectDeviceFiles(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in deviceFilesInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	snap, err := resolveSnapshot(ctx, s, in.NetworkID, in.SnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	cx := fwd.Context(in.NetworkID, snap)
	if snap != nil && !fwd.IsReady(snap) {
		return notReadySnapshot(deviceFilesName, cx, snap, "")
	}
	if !fwd.IsReady(snap) {
		return result.NewUnknown(deviceFilesName, "No processed snapshot is available to read", cx,
			[]string{"no processed snapshot; nothing was read"}, result.Options{NextActions: []string{"investigate-collection-failure"}})
	}
	sid := fwd.SnapshotID(cx)
	var limits []string
	if cx.State == "predicted" {
		limits = append(limits, "read from a predicted snapshot: files describe a prediction, not collected state")
	}
	if in.Mode == "list" {
		return listDeviceFiles(ctx, s, in, cx, sid, limits)
	}
	if in.File == "" {
		return result.NewError(deviceFilesName, "mode "+in.Mode+" needs file; use mode list to see the names", cx), nil
	}
	var re *regexp.Regexp
	if in.Mode == "search" {
		if in.Pattern == "" {
			return result.NewError(deviceFilesName, "mode search needs pattern", cx), nil
		}
		if re, err = regexp.Compile(in.Pattern); err != nil {
			return result.NewError(deviceFilesName, "pattern is not a valid regular expression: "+err.Error(), cx), nil
		}
	}
	data, truncated, err := s.DeviceFile(ctx, in.NetworkID, in.Device, in.File, sid, maxFileDownload)
	if fwd.NotFound(err) {
		return result.NewUnknown(deviceFilesName, fmt.Sprintf("Forward has no file %q for device %q in this snapshot", in.File, in.Device), cx,
			append(limits, "device or file not found; names are exact. Use mode list to see the files"), result.Options{})
	}
	if err != nil {
		return result.Result{}, err
	}
	if truncated {
		limits = append(limits, fmt.Sprintf("the file is larger than %d MiB; only its first %d MiB was read", maxFileDownload>>20, maxFileDownload>>20))
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(data) == 0 {
		return result.NewUnknown(deviceFilesName, fmt.Sprintf("File %q is empty", in.File), cx,
			append(limits, "the collected file has no content: the command may have produced nothing, or the collection was partial"), result.Options{NextActions: []string{"investigate-collection-failure"}})
	}
	redacted := 0
	show := func(l string) string {
		if !in.NoRedact {
			if r, ok := redact(l); ok {
				redacted++
				l = r
			}
		}
		return clipLine(l)
	}
	base := map[string]any{"device": in.Device, "file": in.File, "total_lines": len(lines)}
	var finding string
	if in.Mode == "search" {
		finding = searchLines(lines, re, in, show, base, &limits)
	} else {
		finding = readLines(lines, in, show, base, &limits)
	}
	if redacted > 0 {
		limits = append(limits, fmt.Sprintf("%d line(s) had a secret value replaced with <redacted> (best effort; set no_redact to see them)", redacted))
	} else if !in.NoRedact {
		limits = append(limits, "secret values are redacted on a best-effort basis; review before sharing")
	}
	next := []string{"check-network-compliance"}
	if base["matches"] == 0 {
		return result.NewUnknown(deviceFilesName, finding, cx,
			append(limits, "no line matched; the setting may be absent, spelled differently in this vendor's syntax, or in another file"), result.Options{NextActions: next})
	}
	return result.Build(deviceFilesName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, NextActions: next,
		Evidence: []result.Evidence{result.NewEvidence(result.EvConfig, "downloadDeviceFile", cx.SnapshotID, base, finding)}})
}

func listDeviceFiles(ctx context.Context, s *fwd.Session, in deviceFilesInput, cx result.Context, sid string, limits []string) (result.Result, error) {
	files, err := s.DeviceFiles(ctx, in.NetworkID, in.Device, sid)
	if fwd.NotFound(err) {
		return result.NewUnknown(deviceFilesName, fmt.Sprintf("Forward has no device %q in this snapshot", in.Device), cx,
			append(limits, "device not found; names are exact (use inspect-inventory kind devices)"), result.Options{NextActions: []string{"inspect-inventory"}})
	}
	if err != nil {
		return result.Result{}, err
	}
	if len(files) == 0 {
		return result.NewUnknown(deviceFilesName, fmt.Sprintf("Forward holds no collected files for %q", in.Device), cx,
			append(limits, "no files: the device was not collected, or its files were not kept"), result.Options{NextActions: []string{"investigate-collection-failure"}})
	}
	rows := make([]map[string]any, 0, len(files))
	for _, f := range files {
		rows = append(rows, map[string]any{"name": f.Name, "bytes": f.Bytes, "command": f.Command})
	}
	only := len(files) == 1 && strings.EqualFold(files[0].Name, "snapshot_time.txt")
	if only {
		limits = append(limits, "this device lists only snapshot_time.txt: that is what Forward keeps as files for a cloud account (the cloud collector's raw JSON, such as the instance lists, is not exposed as a device file), so \"did the collector receive instances, and in what shape?\" cannot be answered from here. Use inspect-inventory kinds cloud_accounts, cloud_subnets and cloud_instances for what Forward modelled, and investigate-collection-failure view exceptions for errors the collector logged")
	}
	return result.Build(deviceFilesName, result.OK, fmt.Sprintf("%d collected files for %s", len(files), in.Device), result.Deterministic, cx,
		result.Options{Limits: limits,
			Evidence: []result.Evidence{result.NewEvidence(result.EvConfig, "listDeviceFiles", cx.SnapshotID, map[string]any{"device": in.Device, "files": rows}, fmt.Sprintf("%d files", len(files)))}})
}

func readLines(lines []string, in deviceFilesInput, show func(string) string, base map[string]any, limits *[]string) string {
	if in.MaxLines <= 0 {
		in.MaxLines = defaultReadLines
	}
	in.MaxLines = min(in.MaxLines, maxReadLines)
	start := max(in.StartLine, 1)
	if start > len(lines) {
		base["matches"] = 0
		*limits = append(*limits, fmt.Sprintf("start_line %d is past the end of the %d-line file", start, len(lines)))
		return fmt.Sprintf("Line %d is beyond the end of %s (%d lines)", start, in.File, len(lines))
	}
	end := min(start-1+in.MaxLines, len(lines))
	out := make([]string, 0, end-start+1)
	for i := start - 1; i < end; i++ {
		out = append(out, fmt.Sprintf("%d: %s", i+1, show(lines[i])))
	}
	base["matches"] = len(out)
	base["from_line"], base["to_line"], base["lines"] = start, end, out
	if end < len(lines) {
		*limits = append(*limits, fmt.Sprintf("lines %d-%d of %d shown. Continue with start_line=%d.", start, end, len(lines), end+1))
	}
	return fmt.Sprintf("Lines %d-%d of %d in %s", start, end, len(lines), in.File)
}

func searchLines(lines []string, re *regexp.Regexp, in deviceFilesInput, show func(string) string, base map[string]any, limits *[]string) string {
	if in.MaxMatches <= 0 {
		in.MaxMatches = defaultMatches
	}
	in.MaxMatches = min(in.MaxMatches, maxMatches)
	ctxN := min(max(in.Context, 0), maxContextLines)
	total := 0
	var hits []map[string]any
	for i, l := range lines {
		if !re.MatchString(l) {
			continue
		}
		total++
		if len(hits) == in.MaxMatches {
			continue
		}
		var around []string
		for j := max(i-ctxN, 0); j <= min(i+ctxN, len(lines)-1); j++ {
			around = append(around, fmt.Sprintf("%d: %s", j+1, show(lines[j])))
		}
		hits = append(hits, map[string]any{"line": i + 1, "context": around})
	}
	base["pattern"], base["matches"], base["hits"] = in.Pattern, total, hits
	if total > len(hits) {
		*limits = append(*limits, fmt.Sprintf("%d lines match; the first %d are shown (raise max_matches or narrow the pattern)", total, len(hits)))
	}
	if total == 0 {
		return fmt.Sprintf("No line in %s matches %q", in.File, in.Pattern)
	}
	return fmt.Sprintf("%d line(s) in %s match %q", total, in.File, in.Pattern)
}
