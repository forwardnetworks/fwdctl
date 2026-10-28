package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const verifyChangeName = "verify-change"

func init() { Register(verifyChangeName, verifyChange) }

type expectation struct {
	SrcIP    string          `json:"src_ip"`
	From     string          `json:"from"`
	DstIP    string          `json:"dst_ip"`
	Protocol json.RawMessage `json:"protocol"`
	SrcPort  string          `json:"src_port"`
	DstPort  string          `json:"dst_port"`
	Expect   string          `json:"expect"`
}

type verifyChangeInput struct {
	changeInput
	// View is "verify" (the default, also spelled "verdict": judge the change), "describe" (read a change set: what it edits, whether it has
	// been predicted, which of its checks got worse) or "impact" (measure how far it reaches and judge nothing).
	View         string        `json:"view"`
	Expectations []expectation `json:"expectations"`
	// SummaryKind belongs to view ai_summary: config, impact (default), failure or overview.
	SummaryKind string `json:"summary_kind"`
	// CommitID belongs to views history and config: one saved version of the change set.
	CommitID string `json:"commit_id"`
	// ObjectKind belongs to view objects; OS and Protocol to view commands.
	ObjectKind string `json:"object_kind"`
	OS         string `json:"os"`
	Protocol   string `json:"protocol"`
}

// Inputs by view (an input of another view is rejected). describe reads one change set; verify and impact compare a before and an after.
var verifyChangeViewInputs = map[string][]string{
	"verify":     {"change_set_id", "run_predict", "connectivity_timeout_seconds", "before_snapshot_id", "after_snapshot_id", "expectations"},
	"impact":     {"change_set_id", "run_predict", "connectivity_timeout_seconds", "before_snapshot_id", "after_snapshot_id", "device"},
	"describe":   {"change_set_id", "device"},
	"list":       {},
	"scope":      {},
	"preflight":  {},
	"paths":      {"change_set_id", "after_snapshot_id", "connectivity_timeout_seconds"},
	"ai_summary": {"change_set_id", "before_snapshot_id", "after_snapshot_id", "summary_kind"},
	"history":    {"change_set_id", "commit_id"},
	"config":     {"change_set_id", "device", "commit_id"},
	"affected":   {"after_snapshot_id"},
	"bgp_diffs":  {"change_set_id", "device", "commit_id"},
	"objects":    {"change_set_id", "device", "object_kind"},
	"checks":     {"change_set_id"},
	"commands":   {"os", "protocol"},
}

func verifyChange(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in verifyChangeInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	view := in.View
	if view == "" || view == "verdict" {
		view = "verify"
	}
	switch view {
	case "verify", "impact", "describe", "list", "scope", "preflight", "paths", "ai_summary", "history", "config", "affected", "bgp_diffs", "objects", "checks", "commands":
	default:
		return result.Result{}, fmt.Errorf("%w: view must be verify, describe, impact, list, scope, preflight, paths, ai_summary, history, config, affected, bgp_diffs, objects, checks or commands", ErrInvalidInput)
	}
	if err := rejectForeignInputs(raw, verifyChangeName, "view", view, verifyChangeViewInputs); err != nil {
		return result.Result{}, err
	}
	switch view {
	case "ai_summary":
		return aiSummary(ctx, s, in, in.SummaryKind)
	case "history":
		return changeSetHistory(ctx, s, in)
	case "config":
		return changeSetConfig(ctx, s, in)
	case "affected":
		return predictedAffected(ctx, s, in)
	case "bgp_diffs":
		return predictBGPDiffs(ctx, s, in)
	case "objects":
		return predictObjects(ctx, s, in)
	case "checks":
		return predictChecks(ctx, s, in)
	case "commands":
		return predictCommands(ctx, s, in)
	case "list":
		return listChangeSets(ctx, s, in.NetworkID)
	case "scope":
		return predictScope(ctx, s, in.NetworkID, "")
	case "preflight":
		return predictPreflight(ctx, s, in.NetworkID)
	case "paths":
		return interestingPaths(ctx, s, raw)
	case "impact":
		return analyzeBlastRadius(ctx, s, raw)
	case "describe":
		return reviewChangeSet(ctx, s, raw)
	}
	rc, err := resolveChange(ctx, s, in.changeInput)
	if err == errNoSelector {
		return result.NewError(verifyChangeName, err.Error(), fwd.Context(in.NetworkID, nil)), nil
	}
	if err != nil {
		return result.Result{}, err
	}
	cx := fwd.Context(in.NetworkID, rc.After)
	limits := append([]string(nil), rc.Limits...)
	if rc.BeforeID == "" || !fwd.IsReady(rc.After) {
		if len(limits) == 0 {
			limits = []string{"the after snapshot is missing or not processed; nothing was compared"}
		}
		return result.NewUnknown(verifyChangeName, "There is nothing to compare, so the change cannot be verified", cx, limits, result.Options{})
	}
	if !fwd.IsReady(rc.Before) {
		return result.NewUnknown(verifyChangeName, "The before snapshot is not processed, so the change cannot be verified", cx,
			[]string{"the before snapshot is not processed; nothing was compared"}, result.Options{})
	}
	afterID := fwd.SnapshotID(cx)
	sid := fwd.SnapshotIDPtr(rc.After)
	var evidence []result.Evidence
	var failed, undetermined, next []string

	conn, err := s.SubnetConnectivity(ctx, rc.BeforeID, afterID, time.Duration(orDefault(in.ConnectivitySecs, 300))*time.Second)
	if err != nil {
		return result.Result{}, err
	}
	switch {
	case conn.Unavailable != "":
		limits = append(limits, "the connectivity comparison is not available: "+conn.Unavailable+"; connectivity impact is unmeasured")
	case !conn.Settled:
		limits = append(limits, "the connectivity comparison did not settle in time; its counts are not evidence")
	case !conn.Compared:
		limits = append(limits, "no subnet pairs were compared (no locations defined?); connectivity impact is unmeasured")
	default:
		evidence = append(evidence, result.NewEvidence(result.EvPredict, "getSubnetConnectivityDiff", sid, map[string]any{
			"newly_isolated": conn.NewlyIsolated, "newly_connected": conn.NewlyConnected, "modified": conn.Modified, "total_pairs": conn.TotalPairs},
			fmt.Sprintf("%d newly isolated, %d newly connected of %d pairs", conn.NewlyIsolated, conn.NewlyConnected, conn.TotalPairs)))
	}

	regs, err := s.CheckRegressions(ctx, rc.BeforeID, afterID)
	if err != nil {
		return result.Result{}, err
	}
	sort.SliceStable(regs, func(i, j int) bool { return regs[i].ViolationsAfter > regs[j].ViolationsAfter })
	for i, r := range regs {
		failed = append(failed, fmt.Sprintf("check %q regressed", r.Name))
		if i >= maxCitedRegressions {
			continue
		}
		evidence = append(evidence, result.NewEvidence(result.EvPolicy, "getChecks", sid, map[string]any{
			"check_id": r.CheckID, "name": r.Name, "status_before": nilIfEmpty(r.StatusBefore), "status_after": r.StatusAfter,
			"violations_before": r.ViolationsBefore, "violations_after": r.ViolationsAfter},
			fmt.Sprintf("%s: %s -> %s", r.Name, orNone(r.StatusBefore), r.StatusAfter)))
	}

	met := 0
	for _, e := range in.Expectations {
		doc, err := s.SearchPaths(ctx, in.NetworkID, fwd.PathQuery{DstIP: e.DstIP, SrcIP: e.SrcIP, From: e.From,
			Protocol: protocolText(e.Protocol), SrcPort: e.SrcPort, DstPort: e.DstPort, Intent: "PREFER_DELIVERED", SnapshotID: afterID})
		if err != nil {
			return result.Result{}, err
		}
		actual := fwd.Verdict(doc.Paths, doc.TimedOut)
		classes := make([]string, 0, len(doc.Paths))
		for _, p := range doc.Paths {
			classes = append(classes, fwd.Classify(p))
		}
		evidence = append(evidence, result.NewEvidence(result.EvPath, "getPaths", sid, map[string]any{
			"dst_ip": e.DstIP, "src_ip": nilIfEmpty(e.SrcIP), "from": nilIfEmpty(e.From), "protocol": nilIfEmpty(protocolText(e.Protocol)),
			"dst_port": nilIfEmpty(e.DstPort), "expected": e.Expect, "actual": actual, "classifications": classes},
			fmt.Sprintf("expected %s, observed %s", e.Expect, actual)))
		switch {
		case actual == "undetermined":
			undetermined = append(undetermined, e.DstIP+": could not be determined")
		case actual == e.Expect:
			met++
		default:
			failed = append(failed, fmt.Sprintf("%s: expected %s, observed %s", e.DstIP, e.Expect, actual))
			next = append([]string{"investigate-reachability"}, next...)
		}
	}
	next = dedupe(next)

	switch {
	case len(failed) > 0:
		if len(regs) > maxCitedRegressions {
			limits = append(limits, fmt.Sprintf("%d checks regressed; the %d with the most violations are cited", len(regs), maxCitedRegressions))
		}
		finding := strings.Join(failed, "; ")
		if shown, total := result.CapRow(failed, 5); total > len(shown) {
			finding = strings.Join(shown, "; ") + fmt.Sprintf("; and %d more", total-len(shown))
		}
		return result.Build(verifyChangeName, result.Failed, finding, result.Deterministic, cx,
			result.Options{Limits: limits, Evidence: evidence, NextActions: next})
	case len(undetermined) > 0:
		return result.NewUnknown(verifyChangeName, "Some expected outcomes could not be determined", cx, append(limits, undetermined...),
			result.Options{Evidence: evidence, NextActions: next})
	case len(in.Expectations) == 0:
		return result.NewUnknown(verifyChangeName, "No expectations were given, so the change was not judged", cx,
			append(limits, "no expected outcome was given; the impact is reported, not judged"), result.Options{Evidence: evidence, NextActions: next})
	}
	return result.Build(verifyChangeName, result.OK, fmt.Sprintf("All %d expected outcomes hold and no check regressed", met),
		result.Deterministic, cx, result.Options{Limits: limits, Evidence: evidence, NextActions: next})
}

// maxCitedRegressions bounds the regressions cited as evidence; all of them still fail the verdict.
const maxCitedRegressions = 10

func orDefault(n, d int) int {
	if n <= 0 {
		return d
	}
	return n
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// listChangeSets is view list: the change sets a network holds. None is an answer (Predict was never used here, or its change sets were deleted); an unlicensed organization is a licence limit, not an empty list.
// maxListPredictions bounds the per-change-set reads view list makes for predicted snapshots.
const maxListPredictions = 50

func listChangeSets(ctx context.Context, s *fwd.Session, networkID string) (result.Result, error) {
	cx := result.Context{NetworkID: networkID, State: "current"}
	list, err := s.ChangeSets(ctx, networkID)
	if err != nil {
		if r, ok := denialResult(verifyChangeName, err, cx, "listing the network's change sets"); ok {
			return r, nil
		}
		return result.Result{}, err
	}
	rows := make([]map[string]any, 0, len(list))
	unfinishedPred := false
	progressReads := 0
	for i, c := range list {
		row := map[string]any{"id": string(c.ID), "name": c.Name, "description": c.Description, "base_snapshot_id": string(c.SnapshotID), "tags": c.Tags}
		if i < maxListPredictions { // one read per change set, so only the first ones carry their predicted snapshots
			if ps, perr := s.PredictedSnapshots(ctx, networkID, string(c.ID)); perr != nil {
				row["predictions_unreadable"] = perr.Error()
			} else {
				sort.SliceStable(ps, func(a, b int) bool { return ps[a].CreatedAt > ps[b].CreatedAt })
				preds := make([]map[string]any, 0, len(ps))
				for _, p := range ps {
					prow := map[string]any{"id": string(p.ID), "state": p.State, "finished": predictionFinished(p.State), "created_at": nilIfEmpty(p.CreatedAt)}
					if !predictionFinished(p.State) && progressReads < maxProgressReads {
						progressReads++
						prow["processing"] = processingMap(s.Processing(ctx, string(p.ID)))
					}
					preds = append(preds, prow)
					if !predictionFinished(p.State) {
						unfinishedPred = true
					}
				}
				row["predicted_snapshots"] = preds
			}
		}
		if i < maxListPredictions { // the list never carries the per-device changes: read the head commit, and the draft where nothing is committed
			content := s.ChangeContent(ctx, networkID, string(c.ID))
			if content.CommittedErr == nil {
				row["devices_edited"] = len(forward.EditedDevices(content.Committed))
			}
			if len(forward.EditedDevices(content.Committed)) == 0 && content.DraftErr == nil {
				row["draft_devices"] = len(forward.EditedDevices(content.Draft))
			}
		}
		rows = append(rows, row)
	}
	shown, omitted := result.Cap(rows, 100, "change sets", "describe one with view describe")
	limits := []string{"this lists the change sets Forward holds now; a deleted change set is gone, so none here does not prove Predict was never used",
		fmt.Sprintf("predicted_snapshots (newest first) are read for the first %d change sets only; a change set's checks are read with view describe (change_set_id)", maxListPredictions),
		"devices_edited counts the devices of the change set's last COMMITTED version, read from its head commit (the change-set list itself never carries the per-device changes), and counts a BGP-only change set's devices; a change set staged but not yet committed or run shows 0 there, and draft_devices (read where nothing is committed) counts the devices of this login's uncommitted draft; view describe lists what is staged on each device"}
	if unfinishedPred {
		limits = append(limits, "a predicted snapshot reports UNPROCESSED while it is still being created or computed (inspect-snapshots shows its stage, such as CREATION or COMPUTING): only PROCESSED, FAILED, CANCELED and TIMED_OUT are finished (see each prediction's finished flag)")
	}
	finding := fmt.Sprintf("%d change set(s) on network %s", len(rows), networkID)
	if len(rows) == 0 {
		finding = fmt.Sprintf("No change set on network %s", networkID)
	}
	return result.Build(verifyChangeName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, Omitted: omitted, NextActions: []string{"edit-change-set"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvPredict, "listChangeSets", nil, map[string]any{"view": "list", "total": len(rows), "change_sets": shown}, finding)}})
}
