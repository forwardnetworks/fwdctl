package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
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
		// a name that is no device in a snapshot has no changed file either: that is not "unchanged", so say which snapshot lacks it
		var missing []string
		for _, side := range []struct{ label, id string }{{"before", in.BeforeSnapshotID}, {"after", in.AfterSnapshotID}} {
			files, ferr := s.DeviceFiles(ctx, in.NetworkID, in.Device, side.id)
			if ferr == nil && len(files) == 0 || ferr != nil && fwd.Status(ferr) == 404 {
				missing = append(missing, side.label)
			}
		}
		if len(missing) > 0 {
			var near []string
			prefix := strings.ToLower(in.Device) + "_"
			for _, c := range changed {
				if strings.HasPrefix(strings.ToLower(c.Device), prefix) {
					near = append(near, c.Device)
				}
			}
			lim := append(limits, fmt.Sprintf("%s is not a device in the %s snapshot, so nothing was compared and this is not 'unchanged'; names must match exactly", in.Device, strings.Join(missing, " or ")))
			if len(near) > 0 {
				shown, cut := result.Cap(near, 20, "devices whose name starts with the one given", "check inspect-inventory for the exact names")
				lim = append(lim, "devices of that name plus an underscore suffix (for example a firewall's virtual systems) DO have changed files: "+strings.Join(shown, ", "))
				for _, o := range cut {
					lim = append(lim, o.Text())
				}
			}
			return result.NewUnknown(configDiffName, fmt.Sprintf("%s is not a device in the %s snapshot, so no comparison was made", in.Device, strings.Join(missing, " or ")), cx, lim,
				result.Options{NextActions: []string{"inspect-inventory"}})
		}
		noneDetail := map[string]any{"device": in.Device, "file_type": in.FileType, "changed_files": 0}
		lim := append(limits, "Forward lists no changed file for this device; a device absent from a snapshot also appears this way (check with inspect-inventory)")
		sizeNote := fileInventory(ctx, s, in).attach(noneDetail, &lim, "", nil)
		return result.Build(configDiffName, result.OK, fmt.Sprintf("No %s file of %s changed between the snapshots", in.FileType, in.Device)+sizeNote, result.Deterministic, cx,
			result.Options{Limits: lim, NextActions: []string{"inspect-inventory"}, Evidence: []result.Evidence{result.NewEvidence(result.EvConfig, "diffFiles", fwd.SnapshotIDPtr(after), noneDetail, "no changed files")}})
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
	direct := false
	if !found && in.File != "" {
		// Forward's changed-file list can leave out a file whose size changed; a file named by the caller is compared by its text, from both snapshots
		if inv := fileInventory(ctx, s, in); inv.err == nil {
			if _, ok := inv.byName[in.File]; ok {
				direct, found = true, true
				limits = append(limits, fmt.Sprintf("Forward's changed-file list does not include %s; its text was compared directly", in.File))
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
	if direct {
		afterName, beforeName = in.File, in.File
	} else if pick.NameA != "" {
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
	addedShown, addedCut := result.Cap(added, in.MaxLines, "added lines", fmt.Sprintf("raise max_lines (at most %d)", maxDiffLines))
	removedShown, removedCut := result.Cap(removed, in.MaxLines, "removed lines", fmt.Sprintf("raise max_lines (at most %d)", maxDiffLines))
	omitted := append(addedCut, removedCut...)
	detail := map[string]any{"device": in.Device, "file": afterName, "added_count": len(added), "removed_count": len(removed),
		"added": show(addedShown), "removed": show(removedShown)}
	if lc, unpaired := listChanges(added, removed, in.NoRedact); len(lc) > 0 || unpaired > 0 {
		detail["list_changes"] = lc
		limits = append(limits, "added and removed lines are cut at 400 characters; list_changes gives the entries added and removed inside a long `[ ... ]` list, pairing the lines that share most of their entries (at most 5 pairs, 100 entries each way)")
		if unpaired > 0 {
			limits = append(limits, fmt.Sprintf("%d long `[ ... ]` line(s) had no counterpart on the other side, so only the cut text shows for them", unpaired))
		}
	}
	inv := fileInventory(ctx, s, in)
	finding0 := inv.attach(detail, &limits, afterName, offered)
	limits = append(limits, "lines are compared as a set, so a line that only moved is not reported; line numbers are from the file where the line appears")
	if redacted > 0 {
		limits = append(limits, fmt.Sprintf("%d shown line(s) had a secret replaced with <redacted>; a changed secret shows as the same text on both sides", redacted))
	}
	finding := fmt.Sprintf("%s %s: %d line(s) added, %d removed", in.Device, afterName, len(added), len(removed)) + finding0
	if total == 0 {
		finding = fmt.Sprintf("%s %s: no line differs, though Forward lists the file as changed", in.Device, afterName)
		limits = append(limits, "Forward marks the file changed but no line differs: only ordering or whitespace changed, or the change is past the size cap")
	}
	return result.Build(configDiffName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, Omitted: omitted, NextActions: []string{"inspect-device-files", "verify-change"},
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

// maxListPairs and maxListTokens bound list_changes: it is for the lines the 400-character cut hides, not a full dump.
const (
	maxListPairs  = 5
	maxListTokens = 100
)

// listTokens returns the entries inside the first `[` to the last `]` of a line (split on spaces; a trailing comma or semicolon is dropped), or nil
// when the line has no bracketed list.
func listTokens(l string) []string {
	i, j := strings.Index(l, "["), strings.LastIndex(l, "]")
	if i < 0 || j <= i {
		return nil
	}
	var out []string
	for _, f := range strings.Fields(l[i+1 : j]) {
		if f = strings.Trim(f, ",;"); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// listChanges pairs each long added `[ ... ]` line with the removed one that shares most of its entries and reports the entries added and removed.
// Only lines longer than the display cut are considered: a shorter line is already shown whole. It returns the pairs and how many long lists found no
// partner.
func listChanges(added, removed []numbered, noRedact bool) ([]map[string]any, int) {
	type cand struct {
		n      int
		length int
		toks   []string
	}
	collect := func(ls []numbered) []cand {
		var out []cand
		for _, l := range ls {
			t := l.text
			if len(t) <= maxRenderedLength {
				continue
			}
			if !noRedact {
				if r, ok := redact(t); ok {
					t = r
				}
			}
			if toks := listTokens(t); len(toks) > 0 {
				out = append(out, cand{l.n, len(l.text), toks})
			}
		}
		return out
	}
	adds, rems := collect(added), collect(removed)
	used := make([]bool, len(rems))
	var pairs []map[string]any
	unpaired := 0
	for _, a := range adds {
		aset := map[string]bool{}
		for _, t := range a.toks {
			aset[t] = true
		}
		best, bestScore := -1, 0.0
		for i, r := range rems {
			if used[i] {
				continue
			}
			common := 0
			rset := map[string]bool{}
			for _, t := range r.toks {
				if !rset[t] {
					rset[t] = true
					if aset[t] {
						common++
					}
				}
			}
			if union := len(aset) + len(rset) - common; union > 0 {
				if score := float64(common) / float64(union); score > bestScore {
					best, bestScore = i, score
				}
			}
		}
		if best < 0 || bestScore < 0.5 {
			unpaired++
			continue
		}
		used[best] = true
		r := rems[best]
		rset := map[string]bool{}
		for _, t := range r.toks {
			rset[t] = true
		}
		var addedToks, removedToks []string
		for _, t := range a.toks {
			if !rset[t] {
				addedToks = append(addedToks, t)
			}
		}
		for _, t := range r.toks {
			if !aset[t] {
				removedToks = append(removedToks, t)
			}
		}
		if len(pairs) == maxListPairs {
			unpaired++
			continue
		}
		addShown, addTotal := result.CapRow(addedToks, maxListTokens)
		remShown, remTotal := result.CapRow(removedToks, maxListTokens)
		pairs = append(pairs, map[string]any{"line_before": r.n, "line_after": a.n, "length_before": r.length, "length_after": a.length, "entries_before": len(r.toks), "entries_after": len(a.toks),
			"added": addShown, "added_total": addTotal, "removed": remShown, "removed_total": remTotal})
	}
	unpaired += len(rems) - countTrue(used)
	return pairs, unpaired
}

func countTrue(b []bool) int {
	n := 0
	for _, v := range b {
		if v {
			n++
		}
	}
	return n
}

// fileInv is the device's collected files in both snapshots, set side by side by name and size. Forward's changed-file list can leave out a file whose size changed
// (it did, for a 3.6 MB config), so the sizes are read from the file lists themselves.
type fileInv struct {
	rows   []map[string]any // added, removed and size-changed files, largest change first
	byName map[string]map[string]any
	same   int
	err    error
}

const maxInventoryRows = 30

func fileInventory(ctx context.Context, s *fwd.Session, in configDiffInput) fileInv {
	inv := fileInv{byName: map[string]map[string]any{}}
	if in.Device == "" {
		return inv
	}
	b, err := s.DeviceFiles(ctx, in.NetworkID, in.Device, in.BeforeSnapshotID)
	if err != nil {
		inv.err = err
		return inv
	}
	a, err := s.DeviceFiles(ctx, in.NetworkID, in.Device, in.AfterSnapshotID)
	if err != nil {
		inv.err = err
		return inv
	}
	before, after := map[string]int64{}, map[string]int64{}
	for _, f := range b {
		before[f.Name] = f.Bytes
	}
	for _, f := range a {
		after[f.Name] = f.Bytes
	}
	names := map[string]bool{}
	for n := range before {
		names[n] = true
	}
	for n := range after {
		names[n] = true
	}
	for n := range names {
		bb, inB := before[n]
		ab, inA := after[n]
		row := map[string]any{"file": n}
		switch {
		case inB && !inA:
			row["status"], row["before_bytes"] = "removed", bb
		case !inB && inA:
			row["status"], row["after_bytes"] = "added", ab
		case bb != ab:
			row["status"], row["before_bytes"], row["after_bytes"], row["delta_bytes"] = "size_changed", bb, ab, ab-bb
		default:
			inv.same++
			inv.byName[n] = nil
			continue
		}
		inv.byName[n] = row
		inv.rows = append(inv.rows, row)
	}
	sort.SliceStable(inv.rows, func(i, j int) bool { return invMagnitude(inv.rows[i]) > invMagnitude(inv.rows[j]) })
	return inv
}

func invMagnitude(r map[string]any) int64 {
	switch r["status"] {
	case "size_changed":
		d, _ := r["delta_bytes"].(int64)
		if d < 0 {
			d = -d
		}
		return d
	case "removed":
		b, _ := r["before_bytes"].(int64)
		return b
	default:
		b, _ := r["after_bytes"].(int64)
		return b
	}
}

// attach puts the file inventory into detail and the limits, and returns the sentence to add to the finding. diffed is the file this call compared and offered the files Forward's
// changed-file list gave; a size-changed file that is neither is named as not compared, with how to compare it.
func (inv fileInv) attach(detail map[string]any, limits *[]string, diffed string, offered []string) string {
	if inv.err != nil {
		*limits = append(*limits, "the device's file lists could not be read, so file sizes were not compared: "+inv.err.Error())
		return ""
	}
	if len(inv.rows) == 0 {
		return ""
	}
	inList := map[string]bool{}
	for _, o := range offered {
		inList[o] = true
	}
	var rows []map[string]any
	var notDiffed []string
	var summary []string
	for _, r := range inv.rows {
		name, _ := r["file"].(string)
		row := map[string]any{}
		for k, v := range r {
			row[k] = v
		}
		switch {
		case name == diffed:
			row["diffed"] = true
		case r["status"] == "size_changed" && inList[name]:
			row["diffed"] = false
			row["why_not_diffed"] = "Forward lists it as changed; one file is diffed per call, set file to compare this one"
			notDiffed = append(notDiffed, name)
		case r["status"] == "size_changed":
			row["diffed"] = false
			row["why_not_diffed"] = "Forward's changed-file list does not include it although its size changed; set file to compare its text directly"
			notDiffed = append(notDiffed, name)
		default:
			row["diffed"] = false
			row["why_not_diffed"] = "present in only one snapshot, so there is nothing to compare line by line"
		}
		rows = append(rows, row)
		if len(summary) < 4 {
			switch r["status"] {
			case "size_changed":
				summary = append(summary, fmt.Sprintf("%s %+d B", name, r["delta_bytes"]))
			case "removed":
				summary = append(summary, name+" removed")
			default:
				summary = append(summary, name+" added")
			}
		}
	}
	shown, cut := result.Cap(rows, maxInventoryRows, "files whose size or presence changed", "narrow with file")
	detail["file_inventory"] = map[string]any{"changed": len(inv.rows), "unchanged_size": inv.same, "files": shown}
	for _, o := range cut {
		*limits = append(*limits, o.Text())
	}
	if len(notDiffed) > 0 {
		*limits = append(*limits, fmt.Sprintf("%d file(s) changed in size and were not compared line by line (%s): set file to one of them", len(notDiffed), strings.Join(notDiffed, ", ")))
	}
	*limits = append(*limits, "file sizes come from each snapshot's file list; a file that kept its size but changed content shows only if Forward's changed-file list names it")
	return "; file sizes: " + strings.Join(summary, ", ")
}
