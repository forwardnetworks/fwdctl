package skills

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
)

// exportSelect picks the devices of an export by what they ARE instead of by name: their vendor and/or OS, or the rows of an NQE query (text, or a saved query by id at a commit)
// that has a device name column. It is resolved to exact device names (glob characters escaped) on the snapshot being exported, so the export file holds exactly those devices
// (Forward adds a selected device's related devices: a cloud account's devices, a parent and its virtual contexts).
type exportSelect struct {
	Vendor   string `json:"vendor"`
	OS       string `json:"os"`
	Query    string `json:"query"`
	QueryID  string `json:"query_id"`
	CommitID string `json:"commit_id"`
	// As is include (the default: export only the selected devices) or exclude (export everything but them).
	As string `json:"as"`
}

const exportSelectMaxDevices = 50000

const exportSelectByPlatform = `@query
query(vendorName: String, osName: String) =
foreach device in network.devices
where (vendorName == "" || toString(device.platform.vendor) == vendorName) && (osName == "" || toString(device.platform.os) == osName)
select {name: device.name}`

// exportSelectNameColumns are the column names a selecting query may use for the device name.
var exportSelectNameColumns = []string{"name", "Name", "device", "Device", "deviceName", "Device name", "device_name"}

// resolveExportSelect returns the selected device names, escaped so each matches only itself as an export glob. An empty selection is an error: an empty include list would export every device.
func resolveExportSelect(ctx context.Context, s *fwd.Session, networkID, snapshotID string, sel *exportSelect) (names []string, how string, err error) {
	byPlatform := sel.Vendor != "" || sel.OS != ""
	byQuery := strings.TrimSpace(sel.Query) != "" || sel.QueryID != ""
	switch {
	case byPlatform && byQuery, !byPlatform && !byQuery:
		return nil, "", fmt.Errorf("%w: select takes vendor and/or os (device platform), or a query / query_id (an NQE query with a device name column), not both and not neither", ErrInvalidInput)
	case strings.TrimSpace(sel.Query) != "" && sel.QueryID != "":
		return nil, "", fmt.Errorf("%w: select takes query or query_id, not both", ErrInvalidInput)
	case sel.CommitID != "" && sel.QueryID == "":
		return nil, "", fmt.Errorf("%w: select.commit_id goes with select.query_id", ErrInvalidInput)
	case sel.As != "" && sel.As != "include" && sel.As != "exclude":
		return nil, "", fmt.Errorf("%w: select.as is include (the default) or exclude", ErrInvalidInput)
	}
	run := fwd.NQERun{SnapshotID: snapshotID}
	switch {
	case byPlatform:
		run.Query = exportSelectByPlatform
		run.Parameters = map[string]any{"vendorName": strings.ToUpper(strings.TrimSpace(sel.Vendor)), "osName": strings.ToUpper(strings.TrimSpace(sel.OS))}
		how = "vendor " + firstNonEmpty(sel.Vendor, "any") + ", os " + firstNonEmpty(sel.OS, "any")
	case sel.QueryID != "":
		run.QueryID, run.CommitID = sel.QueryID, sel.CommitID
		how = "saved query " + sel.QueryID + " at commit " + firstNonEmpty(sel.CommitID, "head")
	default:
		run.Query = sel.Query
		how = "the given NQE query"
	}
	rows, _, truncated, err := s.RunNQEAllWith(ctx, networkID, run, exportSelectMaxDevices)
	if err != nil {
		return nil, how, err
	}
	if truncated {
		return nil, how, fmt.Errorf("%w: the selection returned more than %d rows; narrow it (a partial list would export the wrong devices)", ErrInvalidInput, exportSelectMaxDevices)
	}
	seen := map[string]bool{}
	for _, r := range rows {
		col := ""
		for _, c := range exportSelectNameColumns {
			if _, ok := r[c]; ok {
				col = c
				break
			}
		}
		if col == "" {
			var have []string
			for k := range r {
				have = append(have, k)
			}
			sort.Strings(have)
			return nil, how, fmt.Errorf("%w: the selecting query needs a device name column (%s); it returned %s", ErrInvalidInput, strings.Join(exportSelectNameColumns, ", "), strings.Join(have, ", "))
		}
		if n, ok := r[col].(string); ok && n != "" {
			seen[n] = true
		}
	}
	for n := range seen {
		names = append(names, escapeExportGlob(n))
	}
	sort.Strings(names)
	return names, how, nil
}

// escapeExportGlob makes a device name match only itself as an export glob (the glob characters are * ? [ and the escape \).
func escapeExportGlob(name string) string {
	var b strings.Builder
	for _, r := range name {
		if strings.ContainsRune(`*?[\`, r) {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
