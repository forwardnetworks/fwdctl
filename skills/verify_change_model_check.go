package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// modelCheck is verify-change view model_check: Forward's own comparison of how well Predict's model of a BASE snapshot reproduces what the
// snapshot collected, as the overall score and a score per protocol (the "precision and recall for my snapshot" an SE asks for before a Predict
// proof of concept). Experimental: it calls a Forward /api/internal/ route (not in the published API), read from Forward's source by the SDK
// session. It is a POST that recomputes the comparison on every call: no result is stored and no audit event is written, but Forward may start
// (and cache the derived results of) the snapshot's protocol-modelling jobs, and it costs compute.
func modelCheck(ctx context.Context, s *fwd.Session, in verifyChangeInput) (result.Result, error) {
	var snap *forward.Snapshot
	var err error
	if in.AfterSnapshotID != "" {
		snap, err = s.Snapshot(ctx, in.NetworkID, in.AfterSnapshotID)
	} else {
		snap, err = s.LatestProcessed(ctx, in.NetworkID)
	}
	if err != nil {
		return result.Result{}, err
	}
	cx := fwd.Context(in.NetworkID, snap)
	if snap == nil || !fwd.IsReady(snap) {
		return result.NewUnknown(verifyChangeName, "There is no processed snapshot to score", cx,
			[]string{"no snapshot was scored: the snapshot is missing or not processed"}, result.Options{NextActions: []string{"inspect-snapshots"}})
	}
	if fwd.IsPredicted(snap) {
		return result.NewUnknown(verifyChangeName, fmt.Sprintf("Snapshot %s is a prediction; model_check scores Predict's model of a collected (base) snapshot", snap.ID), cx,
			[]string{"Forward's verification runs in mode=base only; give the collected snapshot a prediction was made from"}, result.Options{NextActions: []string{"verify-change"}})
	}
	sid := fwd.SnapshotID(cx)
	v, _, verr := s.Client.Predict.VerifyPredictModeling(ctx, sid, false)
	if verr != nil {
		if r, ok := denialResult(verifyChangeName, verr, cx, "scoring Predict's model of the snapshot (it needs the MANAGE_PROTOCOL_MODELING permission)"); ok {
			return r, nil
		}
		if fwd.ErrorKind(verr) == fwd.KindTimeout {
			return result.NewUnknown(verifyChangeName, fmt.Sprintf("Forward did not answer the model check of snapshot %s in time", sid), cx,
				[]string{"the comparison recomputes on every call and the first call on a snapshot also builds cached modelling data, so it can take minutes on a large snapshot; raise FORWARD_TIMEOUT (for example 900s) and run it again: " + verr.Error()},
				result.Options{NextActions: []string{"verify-change"}})
		}
		if st := fwd.Status(verr); st == 401 || st == 403 {
			return result.NewUnknown(verifyChangeName, fmt.Sprintf("Forward refused the model check of snapshot %s (%d)", sid, st), cx,
				[]string{"the check needs the MANAGE_PROTOCOL_MODELING permission (the network ADMIN role) and the Predict licence; Forward answered: " + verr.Error() + "; nothing was scored"}, result.Options{NextActions: []string{"inspect-access"}})
		}
		return result.Result{}, verr
	}
	overall := scoreMap(v.Score)
	byProto := map[string]any{}
	protos := make([]string, 0, len(v.ScoresByProtocol))
	for p := range v.ScoresByProtocol {
		protos = append(protos, p)
	}
	sort.Strings(protos)
	for _, p := range protos {
		byProto[p] = scoreMap(v.ScoresByProtocol[p])
	}
	limits := []string{
		"experimental: this is Forward's own comparison of Predict's model of this base snapshot with the entries it collected, from a Forward /api/internal/ route that is not in the published API (read from Forward's source by the SDK session); the score fields are shown as Forward returns them",
		"the scores are percentages from 0 to 100 (precision is matching entries over modelled entries, recall is matching entries over collected entries, f1 their harmonic mean); the overall score covers only the protocols enabled by the PREDICT_MODEL_* organization properties, so a low recall on a default setup can reflect how little of the collected state is modelled rather than model error (not verified: read it next to view scope); the entry counts behind the scores are not returned",
		"it needs the MANAGE_PROTOCOL_MODELING permission (the network ADMIN role) and the Predict licence; it recomputes on every call, can start Forward's protocol-modelling jobs and cache derived results for the snapshot, and can take minutes on a large snapshot (raise FORWARD_TIMEOUT)",
	}
	detail := map[string]any{"view": "model_check", "snapshot_id": sid, "score": overall, "scores_by_protocol": byProto}
	p, r, f1, numeric := scoreNumbers(overall)
	if !numeric {
		limits = append(limits, "Forward's overall score did not carry precision, recall and f1 as finite numbers, so there is no measurement to report; the fields it did return are in the evidence")
		return result.NewUnknown(verifyChangeName, fmt.Sprintf("Forward returned no usable score for snapshot %s", sid), cx, limits,
			result.Options{NextActions: []string{"inspect-snapshots"}, Evidence: []result.Evidence{result.NewEvidence(result.EvPredict, "predictModelVerification", cx.SnapshotID, detail, "")}})
	}
	if p == 0 && r == 0 && f1 == 0 {
		limits = append(limits, "the overall score is zero in every field: that is what a snapshot with no collected routing state also scores, so it is no basis for a result")
		return result.NewUnknown(verifyChangeName, fmt.Sprintf("Forward returned a zero score for snapshot %s, which is no basis for a result", sid), cx, limits,
			result.Options{NextActions: []string{"inspect-snapshots"}, Evidence: []result.Evidence{result.NewEvidence(result.EvPredict, "predictModelVerification", cx.SnapshotID, detail, "")}})
	}
	finding := fmt.Sprintf("Predict's model of snapshot %s scores %s overall", sid, briefScore(overall))
	if len(protos) > 0 {
		finding += " (per protocol: " + strings.Join(protos, ", ") + ")"
	}
	return result.Build(verifyChangeName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, NextActions: []string{"verify-change"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvPredict, "predictModelVerification", cx.SnapshotID, detail, finding)}})
}

// scoreMap reads one score object as Forward sends it (the SDK leaves it raw). A value that is not an object comes back under "raw".
func scoreMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return map[string]any{"raw": string(raw)}
	}
	return m
}

// scoreNumbers reads precision, recall and f1 (any case) from a score; ok only when all three are present as finite numbers.
func scoreNumbers(m map[string]any) (p, r, f1 float64, ok bool) {
	got := map[string]float64{}
	for k, v := range m {
		x, isNum := v.(float64)
		if !isNum || math.IsNaN(x) || math.IsInf(x, 0) {
			continue
		}
		got[strings.ToLower(k)] = x
	}
	var a, b, c bool
	p, a = got["precision"]
	r, b = got["recall"]
	f1, c = got["f1"]
	return p, r, f1, a && b && c
}

// briefScore names the precision, recall and f1 of a score (percentages) when it carries them (any case), else says the fields are listed in the evidence.
func briefScore(m map[string]any) string {
	var parts []string
	for _, want := range []string{"precision", "recall", "f1"} {
		for k, v := range m {
			if strings.EqualFold(k, want) {
				if f, ok := v.(float64); ok {
					parts = append(parts, fmt.Sprintf("%s %.3g%%", want, f))
				}
			}
		}
	}
	if len(parts) == 0 {
		return "(see the evidence for its fields)"
	}
	return strings.Join(parts, ", ")
}
