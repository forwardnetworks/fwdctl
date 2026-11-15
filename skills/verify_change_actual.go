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

// verify-change view actual: did the change do what Predict said? For each device the change set edits (or the one named), the IPv4 forwarding
// entries are read on three snapshots: the change set's base, its predicted snapshot, and after_snapshot_id, collected after the change was really
// made. Base to predicted is what Predict said would change; base to actual is what did. Each prefix that changed on either side is compared,
// next hops included. Read-only; it never runs a prediction.

const maxActualDevices = 10
const maxActualRows = 100

const actualLimit = "compares IPv4 forwarding entries (prefix and next hops, address and egress interface, per VRF; whether a predicted snapshot names interfaces as the collected one does is not yet seen live) of the named or edited devices only; route attributes (AS path, preference), other devices and IPv6 are not compared. Anything that changed between the base and the after-change collection for another reason (another change, a flap, a later collection) shows as actual_only: read those rows before blaming the prediction"

// routeChange describes how one vrf/prefix moved between two reads: "" (same), "added", "removed" or "next_hops".
func routeChange(before, after map[string]string, key string) string {
	b, hb := before[key]
	a, ha := after[key]
	switch {
	case !hb && ha:
		return "added"
	case hb && !ha:
		return "removed"
	case hb && ha && b != a:
		return "next_hops"
	}
	return ""
}

func predictedVsActual(ctx context.Context, s *fwd.Session, in verifyChangeInput) (result.Result, error) {
	if in.ChangeSetID == "" || in.AfterSnapshotID == "" {
		return result.Result{}, fmt.Errorf("%w: view actual needs change_set_id (the prediction) and after_snapshot_id (the snapshot collected after the change was really made)", ErrInvalidInput)
	}
	baseID, err := s.BaseSnapshotID(ctx, in.NetworkID, in.ChangeSetID)
	if err != nil {
		return result.Result{}, err
	}
	predicted, err := s.PredictedSnapshot(ctx, in.NetworkID, in.ChangeSetID, false)
	if err != nil {
		return result.Result{}, err
	}
	actual, err := s.Snapshot(ctx, in.NetworkID, in.AfterSnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	cx := fwd.Context(in.NetworkID, actual)
	limits := []string{actualLimit}
	switch {
	case predicted == nil:
		return result.NewUnknown(verifyChangeName, "The change set has no processed predicted snapshot, so there is no prediction to compare; run it first (verify-change run_predict, or edit-change-set run)", cx, limits, result.Options{NextActions: []string{"verify-change"}})
	case actual == nil || !fwd.IsReady(actual):
		return result.NewUnknown(verifyChangeName, fmt.Sprintf("Snapshot %s (after the change) is not processed, so nothing was compared", in.AfterSnapshotID), cx, limits, result.Options{NextActions: []string{"inspect-snapshots"}})
	case string(actual.ID) == baseID:
		return result.NewUnknown(verifyChangeName, "after_snapshot_id is the change set's base snapshot: give the snapshot collected after the change was made", cx, limits, result.Options{})
	case string(actual.ID) == string(predicted.ID) || fwd.IsPredicted(actual):
		return result.NewUnknown(verifyChangeName, "after_snapshot_id is a predicted snapshot: give the snapshot collected after the change was made", cx, limits, result.Options{})
	}
	predID := string(predicted.ID)
	devices := []string{}
	if in.Device != "" {
		devices = append(devices, in.Device)
	} else {
		content := s.ChangeContent(ctx, in.NetworkID, in.ChangeSetID)
		devices = forward.EditedDevices(content.Committed)
		if len(devices) == 0 {
			devices = forward.EditedDevices(content.Draft)
		}
		sort.Strings(devices)
	}
	if len(devices) == 0 {
		return result.NewUnknown(verifyChangeName, "The change set edits no device that could be read, and no device was named; give device", cx, limits, result.Options{})
	}
	devices, omittedDevices := result.Cap(devices, maxActualDevices, "edited devices compared", "name one with device to compare another")
	var rows []map[string]any
	counts := map[string]int{}
	judged := 0
	for _, dev := range devices {
		b, e1 := devicePrefixMap(ctx, s, in.NetworkID, baseID, dev)
		p, e2 := devicePrefixMap(ctx, s, in.NetworkID, predID, dev)
		a, e3 := devicePrefixMap(ctx, s, in.NetworkID, string(actual.ID), dev)
		if e1 != nil || e2 != nil || e3 != nil {
			limits = append(limits, fmt.Sprintf("%s was not compared: its forwarding entries could not be read on every snapshot (%v)", dev, firstErr(e1, e2, e3)))
			continue
		}
		if len(b) == 0 || len(p) == 0 || len(a) == 0 {
			limits = append(limits, fmt.Sprintf("%s was not compared: it has no IPv4 forwarding entry in one of the three snapshots (base %d, predicted %d, after %d entries), so it was not proven read", dev, len(b), len(p), len(a)))
			continue
		}
		if !alignHops(b, p, a) {
			limits = append(limits, fmt.Sprintf("%s: next hops were compared on addresses only, because a snapshot carries no egress interface names", dev))
		}
		judged++
		keys := map[string]bool{}
		for _, m := range []map[string]string{b, p, a} {
			for k := range m {
				keys[k] = true
			}
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			pc, ac := routeChange(b, p, k), routeChange(b, a, k)
			if pc == "" && ac == "" {
				continue
			}
			verdict := "differs"
			switch {
			case pc != "" && ac == "":
				verdict = "predicted_only"
			case pc == "" && ac != "":
				verdict = "actual_only"
			case pc == ac && p[k] == a[k]:
				verdict = "match"
			}
			counts[verdict]++
			vrf, prefix, _ := strings.Cut(k, "\x00")
			rows = append(rows, map[string]any{"device": dev, "vrf": vrf, "prefix": prefix, "verdict": verdict,
				"predicted_change": nilIfEmpty(pc), "actual_change": nilIfEmpty(ac),
				"next_hops_base": nilIfEmpty(b[k]), "next_hops_predicted": nilIfEmpty(p[k]), "next_hops_actual": nilIfEmpty(a[k])})
		}
	}
	shown, omittedRows := result.Cap(rows, maxActualRows, "prefixes that changed on either side", "name one device with device to see its rows")
	detail := map[string]any{"base_snapshot_id": baseID, "predicted_snapshot_id": predID, "actual_snapshot_id": string(actual.ID), "devices": devices,
		"devices_compared": judged, "counts": counts, "rows": shown}
	opts := result.Options{Limits: limits, Omitted: append(omittedDevices, omittedRows...), NextActions: []string{"verify-change"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvPredict, "predictedVsActual", fwd.SnapshotIDPtr(actual), detail, "")}}
	summary := fmt.Sprintf("%d match, %d differ, %d predicted only, %d actual only", counts["match"], counts["differs"], counts["predicted_only"], counts["actual_only"])
	switch {
	case judged == 0:
		return result.NewUnknown(verifyChangeName, "No device could be compared (see the limits)", cx, limits, opts)
	case len(rows) == 0:
		return result.NewUnknown(verifyChangeName, fmt.Sprintf("No forwarding entry changed on either side on %d device(s): neither the prediction nor the real change moved a route there, so nothing about the change was compared", judged), cx, limits, opts)
	case counts["differs"] == 0 && counts["predicted_only"] == 0 && counts["match"] == 0:
		return result.NewUnknown(verifyChangeName, fmt.Sprintf("Predict changed no forwarding entry on these %d device(s), but %d changed in the after-change snapshot: another cause or a prediction gap; read the rows", judged, counts["actual_only"]), cx, limits, opts)
	case counts["differs"] == 0 && counts["predicted_only"] == 0:
		finding := fmt.Sprintf("The prediction matches what happened on %d device(s): %s", judged, summary)
		if counts["actual_only"] > 0 {
			finding += "; the actual_only rows changed without being predicted (another cause, or a prediction gap): read them"
		}
		return result.Build(verifyChangeName, result.OK, finding, result.Deterministic, cx, opts)
	}
	return result.Build(verifyChangeName, result.Failed, fmt.Sprintf("The prediction differs from what happened on %d device(s): %s", judged, summary), result.Deterministic, cx, opts)
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
