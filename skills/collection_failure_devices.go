package skills

import (
	"context"
	"fmt"
	"sort"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const (
	defaultFailureRows = 50
	maxFailureRows     = 200
)

// failedDevicesQuery lists the devices that did not collect or did not process, with the error Forward recorded and the platform
// (a device that failed before it could be identified has a null platform).
const failedDevicesQuery = `stage(r) =
  when r is
    collectionFailed(e) -> "collection";
    processingFailed(e) -> "processing";
    completed -> "";

reason(r) =
  when r is
    collectionFailed(e) -> toString(e);
    processingFailed(e) -> toString(e);
    completed -> "";

foreach device in network.devices
let result = device.snapshotInfo.result
where stage(result) != ""
select {
  device: device.name,
  stage: stage(result),
  reason: reason(result),
  vendor: device.platform.vendor,
  os: device.platform.os,
  osVersion: device.platform.osVersion,
  model: device.platform.model,
  collectionIp: device.snapshotInfo.collectionIp
}`

// failureType strips the enum prefix NQE prints ("DeviceCollectionError.TIMEOUT" is TIMEOUT).
func failureType(reason string) string {
	if i := strings.LastIndexByte(reason, '.'); i >= 0 {
		return reason[i+1:]
	}
	return reason
}

// firstLine is the head of a stack trace: the exception class and message, without the frames.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

// failureDevices is view devices: which devices failed, why, and (for processing failures, when the caller may read them) Forward's
// exception for them. It filters by category (credentials, network_path, device_session, unclassified, processing), type or device.
func failureDevices(ctx context.Context, s *fwd.Session, in collectionInput, cx result.Context) (result.Result, error) {
	sid := cx.SnapshotID
	rows, _, trunc, err := s.RunNQEAll(ctx, in.NetworkID, fwd.SnapshotID(cx), failedDevicesQuery, maxModelRows)
	if err != nil {
		return result.Result{}, err
	}
	var limits []string
	if trunc {
		limits = append(limits, fmt.Sprintf("the device read hit its %d-row bound", maxModelRows))
	}
	byType, byCategory := map[string]int{}, map[string]int{}
	var kept []map[string]any
	processingKept := 0
	for _, r := range rows {
		typ := failureType(str(r["reason"]))
		cat := "processing"
		if str(r["stage"]) == "collection" {
			cat = fwd.FailureCategory(typ)
		}
		byType[typ]++
		byCategory[cat]++
		if (in.Failure != "" && !strings.EqualFold(cat, in.Failure) && !strings.EqualFold(typ, in.Failure)) ||
			(in.Device != "" && !strings.Contains(strings.ToLower(str(r["device"])), strings.ToLower(in.Device))) {
			continue
		}
		if cat == "processing" {
			processingKept++
		}
		kept = append(kept, map[string]any{"device": r["device"], "stage": r["stage"], "category": cat, "type": typ, "vendor": r["vendor"], "os": r["os"],
			"os_version": r["osVersion"], "model": r["model"], "collection_ip": r["collectionIp"]})
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if a, b := str(kept[i]["type"]), str(kept[j]["type"]); a != b {
			return a < b
		}
		return str(kept[i]["device"]) < str(kept[j]["device"])
	})
	// the metrics count every failed device; the model lists only devices Forward built a record for
	m, merr := s.SnapshotMetrics(ctx, fwd.SnapshotID(cx))
	want := -1
	if merr == nil {
		want = sum(m.CollectionFailures) + sum(m.ProcessingFailures)
		if want != len(rows) {
			limits = append(limits, fmt.Sprintf("the snapshot's metrics count %d failed device(s) but the device model lists %d; a device that failed before Forward could record it is in the count only, so the difference is not named here", want, len(rows)))
		}
	}
	if len(rows) == 0 {
		if want == 0 && m.SuccessfulDevices > 0 {
			return result.Build(collectionFailureName, result.OK, fmt.Sprintf("No device failed: the device model lists none and the snapshot's metrics count none (%d collected)", m.SuccessfulDevices),
				result.Deterministic, cx, result.Options{Limits: limits, Evidence: []result.Evidence{result.NewEvidence(result.EvCollection, "runNqeQuery", sid, map[string]any{"total_failed": 0, "successful_devices": m.SuccessfulDevices}, "")}})
		}
		limits = append(limits, "the device model lists no failed device, and the metrics do not confirm none failed; this is not proof every device collected (see the summary view)")
		return result.NewUnknown(collectionFailureName, "No device in the model is recorded as failed", cx, limits, result.Options{NextActions: []string{"inspect-collection"}})
	}
	finding := fmt.Sprintf("%d device(s) failed (%s)", len(rows), typesText(byType))
	if in.Failure != "" || in.Device != "" {
		finding = fmt.Sprintf("%d of %d failed device(s) match the filter", len(kept), len(rows))
	}
	detail := map[string]any{"total_failed": len(rows), "by_type": byType, "by_category": byCategory, "matching": len(kept), "offset": in.Offset}
	win, wl, ok := window(kept, in.Limit, in.Offset, defaultFailureRows, maxFailureRows, "failed devices")
	if ok {
		detail["devices"] = win
		limits = append(limits, wl...)
	} else if len(kept) > 0 {
		return result.NewUnknown(collectionFailureName, fmt.Sprintf("Offset %d is beyond the %d matching devices", in.Offset, len(kept)), cx, []string{"offset is past the end of the list"}, result.Options{})
	}
	// Forward's exception for each processing failure type: the device names and the head of its stack trace (the parser's message)
	if processingKept > 0 {
		exc, readable, eerr := s.SnapshotExceptions(ctx, fwd.SnapshotID(cx))
		switch {
		case eerr != nil:
			return result.Result{}, eerr
		case !readable:
			limits = append(limits, "the exception message for processing failures was not read (it needs the DEBUG_SNAPSHOTS permission the caller lacks)")
		default:
			var cited []map[string]any
			for _, e := range exc {
				if f := in.Failure; f != "" && !strings.EqualFold(f, "processing") && !strings.Contains(strings.ToUpper(e.Type), strings.ToUpper(f)) {
					continue
				}
				devs := e.Devices
				if len(devs) > 25 {
					devs = devs[:25]
				}
				cited = append(cited, map[string]any{"type": e.Type, "occurrences": e.Occurrences, "devices": devs, "message": firstLine(e.StackTrace)})
			}
			sort.SliceStable(cited, func(i, j int) bool { return cited[i]["occurrences"].(int) > cited[j]["occurrences"].(int) })
			if len(cited) > 10 {
				limits = append(limits, fmt.Sprintf("%d kinds of exception; the 10 most frequent are cited", len(cited)))
				cited = cited[:10]
			}
			if len(cited) == 0 {
				msg := "Forward's exception list holds no record for these processing failures, so no exception message, class or line exists to read"
				if byType["PARSER_EXCEPTION"] > 0 {
					msg += ". That is expected: Forward marks a supported device PARSER_EXCEPTION whenever it fails to process, even when it stored no exception"
				}
				if byType["LICENSE_EXHAUSTED"] > 0 {
					msg += ". LICENSE_EXHAUSTED is a licence limit, not a parse problem: the devices past the licence were not processed"
				}
				limits = append(limits, msg+". The failed devices come from the device model; collectionError on a device is sometimes the root cause (see their category), and the raw files are in inspect-device-files")
				break
			}
			detail["exceptions"] = cited
			limits = append(limits, "exceptions name the device and the exception class and message; Forward's API gives no line or file region of the device's raw config for a parser exception, so read the device's files with inspect-device-files")
		}
	}
	limits = append(limits, "category groups the error type: credentials, network_path, device_session, unclassified (collection), processing (parse or model). Failed devices are read from the snapshot's device model")
	return result.Build(collectionFailureName, result.Failed, finding, result.Deterministic, cx, result.Options{Limits: limits,
		NextActions: []string{"inspect-device-files", "inspect-collection"},
		Evidence:    []result.Evidence{result.NewEvidence(result.EvCollection, "runNqeQuery", sid, detail, finding)}})
}

// unmodelledNeighbors is view neighbors: the neighbours Forward sees (CDP, LLDP, iBGP, OSPF) but does not model, each marked when a
// modelled device peers with it over BGP, which is how an unmodelled upstream shows up.
func unmodelledNeighbors(ctx context.Context, s *fwd.Session, in collectionInput, cx result.Context) (result.Result, error) {
	missing, err := s.MissingDevices(ctx, in.NetworkID, fwd.SnapshotID(cx))
	if err != nil {
		return result.Result{}, err
	}
	if len(missing) == 0 {
		return result.NewUnknown(collectionFailureName, "Forward lists no unmodelled neighbour", cx,
			[]string{"an empty list is what Forward returned for this snapshot; it does not prove nothing is missing beyond what its neighbours advertise"}, result.Options{})
	}
	var limits []string
	byAddr, byName := map[string][]map[string]any{}, map[string][]map[string]any{}
	bgp, _, trunc, err := s.RunNQEAll(ctx, in.NetworkID, fwd.SnapshotID(cx), bgpNeighborQuery, maxModelRows)
	bgpKnown := err == nil
	if err != nil {
		limits = append(limits, "bgp_peer could not be derived (the BGP neighbors could not be read: "+err.Error()+"), so it is null")
	} else if trunc {
		bgpKnown = false
		limits = append(limits, "bgp_peer could not be derived: the BGP neighbor read hit its row bound, so it is null")
	}
	for _, r := range bgp {
		p := map[string]any{"device": r["device"], "vrf": r["vrf"], "peer_as": r["peerAS"], "state": r["state"]}
		byAddr[str(r["peer"])] = append(byAddr[str(r["peer"])], p)
		if n := str(r["peerDevice"]); n != "" {
			byName[n] = append(byName[n], p)
		}
	}
	rows := make([]map[string]any, 0, len(missing))
	peers := 0
	for _, d := range missing {
		row := map[string]any{"name": d.Name, "vendor": d.Vendor, "type": d.Type, "discovery_method": d.DiscoveryMethod, "ip_addresses": d.IPAddresses, "seen_by": d.Neighbors, "bgp_peer": nil}
		if bgpKnown {
			sessions := append([]map[string]any{}, byName[d.Name]...)
			for _, ip := range d.IPAddresses {
				sessions = append(sessions, byAddr[ip]...)
			}
			row["bgp_peer"] = len(sessions) > 0
			if len(sessions) > 0 {
				peers++
				row["bgp_sessions"] = sessions
			}
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i]["bgp_peer"] == true, rows[j]["bgp_peer"] == true
		return a && !b
	})
	win, wl, ok := window(rows, in.Limit, in.Offset, defaultFailureRows, maxFailureRows, "unmodelled neighbours")
	if !ok {
		return result.NewUnknown(collectionFailureName, fmt.Sprintf("Offset %d is beyond the %d neighbours", in.Offset, len(rows)), cx, []string{"offset is past the end of the list"}, result.Options{})
	}
	limits = append(limits, wl...)
	limits = append(limits, "BGP peers come first; bgp_peer matches a neighbour's name or addresses to a modelled device's BGP neighbor address, so a peer reached by an address Forward did not list is not matched. A peer that is unmodelled is usually the upstream or edge")
	finding := fmt.Sprintf("%d neighbour device(s) are seen but not modelled; %d of them are BGP peers of a modelled device", len(missing), peers)
	if !bgpKnown {
		finding = fmt.Sprintf("%d neighbour device(s) are seen but not modelled; whether they are BGP peers could not be derived", len(missing))
	}
	d := map[string]any{"total": len(missing), "bgp_peers": peers, "offset": in.Offset, "neighbors": win}
	return result.Build(collectionFailureName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits,
		NextActions: []string{"inspect-bgp-neighbors", "plan-synthetic-device"},
		Evidence:    []result.Evidence{result.NewEvidence(result.EvCollection, "getMissingDevices", cx.SnapshotID, d, finding)}})
}

const (
	maxLogBytes     = 512 << 10
	defaultLogLines = 100
	maxLogLines     = 500
)

// slowCollection is view slow: each device's collection duration and its slowest command, slowest first. Forward keeps one slowest
// command per device, not every command, and nothing for an imported or partially collected snapshot.
func slowCollection(ctx context.Context, s *fwd.Session, in collectionInput, cx result.Context) (result.Result, error) {
	m, err := s.CollectionMetrics(ctx, in.NetworkID, fwd.SnapshotID(cx))
	if fwd.NotFound(err) {
		return result.NewUnknown(collectionFailureName, "Forward holds no collection metrics for this snapshot", cx,
			[]string{"collection metrics are not saved for an imported or partially collected snapshot, so nothing was measured"}, result.Options{})
	}
	if err != nil {
		return result.Result{}, err
	}
	if m == nil || len(m.Devices) == 0 {
		return result.NewUnknown(collectionFailureName, "Forward returned no per-device collection metrics", cx,
			[]string{"no device rows came back; an empty answer says nothing about speed or errors"}, result.Options{})
	}
	rows := make([]map[string]any, 0, len(m.Devices))
	var durs []int64
	timed := 0
	for _, d := range m.Devices {
		if in.Device != "" && !strings.Contains(strings.ToLower(d.DeviceName), strings.ToLower(in.Device)) {
			continue
		}
		row := map[string]any{"device": d.DeviceName, "source_type": d.SourceType, "device_type": d.DeviceType, "connection": d.ConnTypeDisplayName,
			"slowest_command": d.SlowestCommand, "jump_server": nilIfEmpty(d.JumpServer), "error": noneIsNil(d.Error)}
		row["collection_ms"], row["slowest_command_ms"] = nil, nil
		if d.CollectionDurationMillis != nil {
			row["collection_ms"] = *d.CollectionDurationMillis
			durs = append(durs, *d.CollectionDurationMillis)
			timed++
		}
		if d.SlowestCommandDurationMillis != nil {
			row["slowest_command_ms"] = *d.SlowestCommandDurationMillis
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return result.NewUnknown(collectionFailureName, "No device matches the filter", cx, []string{"names are matched as a substring; the snapshot has " + fmt.Sprint(len(m.Devices)) + " device rows"}, result.Options{})
	}
	ms := func(r map[string]any, k string) int64 {
		if v, ok := r[k].(int64); ok {
			return v
		}
		return -1
	}
	sort.SliceStable(rows, func(i, j int) bool { return ms(rows[i], "collection_ms") > ms(rows[j], "collection_ms") })
	sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
	stats := map[string]any{"devices": len(rows), "with_duration": timed}
	if len(durs) > 0 {
		stats["median_ms"], stats["p95_ms"], stats["max_ms"] = durs[len(durs)/2], durs[(len(durs)*95)/100], durs[len(durs)-1]
	}
	errs := 0
	for _, r := range rows {
		if r["error"] != nil {
			errs++
		}
	}
	stats["with_error"] = errs
	var limits []string
	win, wl, ok := window(rows, in.Limit, in.Offset, defaultFailureRows, maxFailureRows, "devices")
	if !ok {
		return result.NewUnknown(collectionFailureName, fmt.Sprintf("Offset %d is beyond the %d devices", in.Offset, len(rows)), cx, []string{"offset is past the end of the list"}, result.Options{})
	}
	limits = append(limits, wl...)
	limits = append(limits, "durations are milliseconds; Forward keeps only each device's slowest command (not every command), and a device with no duration has no recorded collection. error is the collection and processing error merged, so it is every error class, not only failures. For what a device did, read its log (view logs).")
	finding := fmt.Sprintf("%d device(s); slowest collection %s", len(rows), rows[0]["device"])
	if v, ok := rows[0]["collection_ms"].(int64); ok {
		finding += fmt.Sprintf(" at %.1fs", float64(v)/1000)
	}
	d := map[string]any{"stats": stats, "offset": in.Offset, "devices": win}
	return result.Build(collectionFailureName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits,
		NextActions: []string{"inspect-collection", "inspect-device-files"},
		Evidence:    []result.Evidence{result.NewEvidence(result.EvCollection, "getCollectionMetrics", cx.SnapshotID, d, finding)}})
}

// deviceLog is view logs: a window of one device's collection log at or above a level (default WARN).
func deviceLog(ctx context.Context, s *fwd.Session, in collectionInput, cx result.Context) (result.Result, error) {
	level := strings.ToUpper(in.Failure)
	if level == "" {
		level = "WARN"
	}
	switch level {
	case "TRACE", "DEBUG", "INFO", "WARN", "ERROR":
	default:
		return result.Result{}, fmt.Errorf("%w: for view logs, failure is the minimum log level: TRACE, DEBUG, INFO, WARN or ERROR", ErrInvalidInput)
	}
	if strings.TrimSpace(in.Device) == "" {
		return result.Result{}, fmt.Errorf("%w: view logs needs device (the name the device was collected under)", ErrInvalidInput)
	}
	text, trunc, err := s.SnapshotLog(ctx, fwd.SnapshotID(cx), in.Device, level, maxLogBytes)
	if fwd.NotFound(err) || fwd.NotAcceptable(err) {
		return result.NewUnknown(collectionFailureName, "Forward holds no collection log for this snapshot", cx,
			[]string{"Forward answered 404 or 406 for the log: an imported, forked or reprocessed snapshot keeps no collection log (a 406 is Forward refusing its own JSON error page for a text request, so the real cause is not stated); nothing was read. The log of the snapshot that was collected is the one to ask for"}, result.Options{NextActions: []string{"inspect-snapshots"}})
	}
	if err != nil {
		return result.Result{}, err
	}
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return result.NewUnknown(collectionFailureName, fmt.Sprintf("No %s-or-higher log lines for %s", level, in.Device), cx,
			[]string{"the log is empty at this level, or the device name is not the one it was collected under (Forward matches the requested name exactly): that is not proof the collection was clean. Try failure INFO, or find the name with view devices or slow"}, result.Options{})
	}
	lines := strings.Split(text, "\n")
	win, wl, ok := window(lines, in.Limit, in.Offset, defaultLogLines, maxLogLines, "log lines")
	if !ok {
		return result.NewUnknown(collectionFailureName, fmt.Sprintf("Offset %d is beyond the %d log lines read", in.Offset, len(lines)), cx, []string{"offset is past the end of the log read"}, result.Options{})
	}
	limits := wl
	if trunc {
		limits = append(limits, fmt.Sprintf("the log was cut at %d KiB; later lines were not read", maxLogBytes>>10))
	}
	limits = append(limits, "log lines are Forward's own collection log for this device at "+level+" and above; they can quote commands and device output, so do not paste them into an issue or a public place")
	finding := fmt.Sprintf("%d %s-or-higher log line(s) for %s", len(lines), level, in.Device)
	d := map[string]any{"device": in.Device, "level": level, "lines_read": len(lines), "offset": in.Offset, "lines": win}
	return result.Build(collectionFailureName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits,
		NextActions: []string{"inspect-device-files", "inspect-collection"},
		Evidence:    []result.Evidence{result.NewEvidence(result.EvCollection, "getSnapshotLogs", cx.SnapshotID, d, finding)}})
}

// noneIsNil maps Forward's "NONE" (no error) and "" to nil.
func noneIsNil(e string) any {
	if e == "" || strings.EqualFold(e, "NONE") {
		return nil
	}
	return e
}

// collectorExceptions is view exceptions: the exceptions the collectors hit while collecting the snapshot (deduplicated by Forward), each with the head of its stack trace, how many
// times it happened, and which devices (or cloud accounts) it happened on. It is where an error a collector ignored (a quota call that returned 400, a permission that was missing)
// shows up even though the collection finished and the account reads as collected.
func collectorExceptions(ctx context.Context, s *fwd.Session, in collectionInput, cx result.Context) (result.Result, error) {
	lim := int32(200)
	occ := int32(50)
	ex, _, err := s.Client.Snapshots.CollectionExceptions(ctx, fwd.SnapshotID(cx), forward.CollectionExceptionOptions{Limit: &lim, OccurrencesLimit: &occ})
	if err != nil {
		if dr, ok := denialResult(collectionFailureName, err, cx, "read the collectors' exceptions"); ok {
			return dr, nil
		}
		return result.Result{}, err
	}
	var rows []map[string]any
	for _, e := range ex.Exceptions {
		devs := map[string]int{}
		for _, o := range e.Occurrences {
			if o.DeviceName != "" {
				devs[o.DeviceName]++
			}
		}
		if in.Device != "" {
			hit := false
			for d := range devs {
				if strings.Contains(strings.ToLower(d), strings.ToLower(in.Device)) {
					hit = true
				}
			}
			if !hit {
				continue
			}
		}
		names := make([]string, 0, len(devs))
		for d := range devs {
			names = append(names, d)
		}
		sort.Strings(names)
		if len(names) > 25 {
			names = names[:25]
		}
		row := map[string]any{"message": firstLine(e.StackTrace), "occurrences": e.TotalOccurrences, "devices": names}
		if e.CollectorVersion != "" {
			row["collector_version"] = e.CollectorVersion
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i]["occurrences"].(int) > rows[j]["occurrences"].(int) })
	limits := []string{"these are the exceptions the collectors logged while collecting this snapshot, deduplicated by Forward; a collection can finish, and an account read as collected, with some of them in the log (an ignored warning from one API call, for example). The message is the first line of the stack trace; it can quote what the collector was doing, so keep it out of public places",
		"reading them needs the permission to view collector exceptions (a network administrator)"}
	if ex.Total > len(ex.Exceptions) {
		limits = append(limits, fmt.Sprintf("%d distinct exceptions exist; the first %d are read", ex.Total, len(ex.Exceptions)))
	}
	if len(rows) == 0 {
		if in.Device != "" {
			return result.NewUnknown(collectionFailureName, fmt.Sprintf("No collector exception names a device or account containing %q", in.Device), cx,
				append(limits, fmt.Sprintf("%d distinct exception(s) exist in all", ex.Total)), result.Options{})
		}
		return result.Build(collectionFailureName, result.OK, "The collectors logged no exception for this snapshot", result.Deterministic, cx, result.Options{Limits: limits,
			Evidence: []result.Evidence{result.NewEvidence(result.EvCollection, "collectionExceptions", cx.SnapshotID, map[string]any{"total": ex.Total, "exceptions": []any{}}, "")}})
	}
	win, wl, ok := window(rows, in.Limit, in.Offset, 20, 100, "exceptions")
	if !ok {
		return result.NewUnknown(collectionFailureName, fmt.Sprintf("Offset %d is beyond the %d exceptions", in.Offset, len(rows)), cx, []string{"offset is past the end of the list"}, result.Options{})
	}
	finding := fmt.Sprintf("%d distinct collector exception(s); most frequent: %v (%v time(s))", len(rows), win[0]["message"], win[0]["occurrences"])
	return result.Build(collectionFailureName, result.Failed, finding, result.Deterministic, cx, result.Options{Limits: append(limits, wl...), NextActions: []string{"inspect-collection", "inspect-device-files"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvCollection, "collectionExceptions", cx.SnapshotID, map[string]any{"total": ex.Total, "offset": in.Offset, "exceptions": win}, finding)}})
}
