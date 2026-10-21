package skills

import (
	"context"
	"fmt"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// aiSummary is verify-change view ai_summary: Forward's own AI explains a difference, in words. The kinds are config (what changed in the configurations), impact (what the change does to the network), failure (why checks fail) and overview (a name, description and
// tags for a change set). It works on any two snapshots, a prediction and its base included. It is generation, not a read: Forward's AI service receives the diff, so the view is the opt-in, the AI organization properties are read first, and the text is labelled generated.
func aiSummary(ctx context.Context, s *fwd.Session, in verifyChangeInput, kind string) (result.Result, error) {
	if kind == "" {
		kind = "impact"
	}
	switch kind {
	case "config", "impact", "failure", "overview":
	default:
		return result.Result{}, fmt.Errorf("%w: summary_kind must be config, impact, failure or overview", ErrInvalidInput)
	}
	cx := fwd.Context(in.NetworkID, nil)
	// the organization properties that switch Forward's AI on: AI_ALLOWED for all, then the one for this kind
	need := map[string]string{"config": "predict_ai_assist", "impact": "predict_ai_assist", "overview": "predict_ai_assist", "failure": "predict_failure_summary_assist"}[kind]
	eff, perr := s.EffectiveOrgConfig(ctx)
	var limits []string
	if perr != nil {
		limits = append(limits, "the organization properties could not be read ("+perr.Error()+"), so whether Forward's AI is on was not checked; Forward decides")
	} else {
		for _, p := range []string{"ai_allowed", need} {
			if v, ok := eff[p]; ok && strings.EqualFold(v, "false") {
				return result.NewUnknown(verifyChangeName, fmt.Sprintf("Forward's AI is off for this organization (%s is false), so no summary was generated", strings.ToUpper(p)), cx,
					[]string{"nothing was sent to Forward's AI service; an organization administrator or Forward support turns " + strings.ToUpper(p) + " on, where it is allowed", "verify-change view impact measures the same difference without AI"}, result.Options{})
			}
		}
	}
	detail := map[string]any{"summary_kind": kind, "generated_by": "Forward AI"}
	var text string
	var snapshotID *string
	switch kind {
	case "overview":
		if in.ChangeSetID == "" {
			return result.Result{}, fmt.Errorf("%w: summary_kind overview needs change_set_id", ErrInvalidInput)
		}
		o, err := s.AIPredictOverview(ctx, in.NetworkID, in.ChangeSetID)
		if err != nil {
			return aiFailed(cx, err)
		}
		detail["name"], detail["description"], detail["tags"] = o.Name, o.Description, o.Tags
		detail["change_set_id"] = in.ChangeSetID
		text = o.Name + ": " + o.Description
	default:
		rc, err := resolveChange(ctx, s, in.changeInput)
		if err == errNoSelector {
			return result.NewError(verifyChangeName, "give change_set_id, or before_snapshot_id and after_snapshot_id, to say which difference to summarize", cx), nil
		}
		if err != nil {
			return result.Result{}, err
		}
		cx = fwd.Context(in.NetworkID, rc.After)
		if rc.BeforeID == "" || !fwd.IsReady(rc.Before) || !fwd.IsReady(rc.After) {
			return result.NewUnknown(verifyChangeName, "There is nothing to summarize: a snapshot is missing or not processed", cx, append([]string{"nothing was sent to Forward's AI service"}, rc.Limits...), result.Options{})
		}
		afterID := fwd.SnapshotID(cx)
		snapshotID = fwd.SnapshotIDPtr(rc.After)
		detail["before_snapshot_id"], detail["after_snapshot_id"] = rc.BeforeID, afterID
		switch kind {
		case "config":
			text, err = s.AISummarizeConfigDiff(ctx, rc.BeforeID, afterID)
		case "impact":
			text, err = s.AISummarizeChangeImpact(ctx, rc.BeforeID, afterID)
		case "failure":
			return result.NewUnknown(verifyChangeName, "The failure summary is not available yet: the Go SDK has no wrapper for Forward's failure-summary route, so none was generated", cx,
				append([]string{"nothing was sent to Forward's AI service; the wrapper has been requested from the SDK", "verify-change view verify lists the checks that got worse without AI"}, rc.Limits...), result.Options{})
		}
		if err != nil {
			return aiFailed(cx, err)
		}
		detail["summary"] = text
	}
	if kind == "overview" {
		detail["summary"] = text
	}
	limits = append(limits,
		"this is generated text from Forward's AI, not a measurement: it can miss or misstate a difference, so check it against verify-change view impact (the counted differences) before relying on it",
		"the diff content was sent to Forward's AI service to produce it; this view runs only when asked for, and an organization that does not allow AI never reaches it",
		"the organization properties AI_ALLOWED and "+strings.ToUpper(need)+" must be on (read first)")
	return result.Build(verifyChangeName, result.OK, "Forward AI "+kind+" summary: "+aiFirstLine(text), result.Deterministic, cx,
		result.Options{Limits: limits, Evidence: []result.Evidence{result.NewEvidence(result.EvPredict, "aiSummary", snapshotID, detail, "generated by Forward AI, labelled")}, NextActions: []string{"verify-change"}})
}

func aiFailed(cx result.Context, err error) (result.Result, error) {
	return result.NewUnknown(verifyChangeName, "Forward's AI did not return a summary: "+err.Error(), cx,
		[]string{"nothing is known from this view; Forward refuses the call when its AI is off or the login lacks the role, and fails it when the AI service is unreachable", "verify-change view impact measures the difference without AI"}, result.Options{})
}

func aiFirstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
