package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

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
	// Device belongs to the describe view: a firewall whose rule diff to include.
	Device string `json:"device"`
}

// Inputs by view (an input of another view is rejected). describe reads one change set; verify and impact compare a before and an after.
var verifyChangeViewInputs = map[string][]string{
	"verify":   {"change_set_id", "run_predict", "connectivity_timeout_seconds", "before_snapshot_id", "after_snapshot_id", "expectations"},
	"impact":   {"change_set_id", "run_predict", "connectivity_timeout_seconds", "before_snapshot_id", "after_snapshot_id"},
	"describe": {"change_set_id", "device"},
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
	case "verify", "impact", "describe":
	default:
		return result.Result{}, fmt.Errorf("%w: view must be verify, describe or impact", ErrInvalidInput)
	}
	if err := rejectForeignInputs(raw, verifyChangeName, "view", view, verifyChangeViewInputs); err != nil {
		return result.Result{}, err
	}
	switch view {
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
