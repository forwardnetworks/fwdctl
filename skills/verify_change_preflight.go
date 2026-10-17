package skills

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// predictPreflight is verify-change view preflight: the checklist for a Predict demo or a first change set, in one call. It is the scope (license facet, release, flags, platform support, out-of-scope families) plus whether the BGP devices
// collect received BGP advertisements (modify and withdraw silently do nothing without it), whether the base snapshot is a fresh collection, and the change sets the network already has.
func predictPreflight(ctx context.Context, s *fwd.Session, networkID string) (result.Result, error) {
	sc, err := buildScope(ctx, s, networkID, "")
	if err != nil {
		return result.Result{}, err
	}
	if sc.notReady != nil {
		return *sc.notReady, nil
	}
	limits := sc.limits
	d := map[string]any{"release": sc.detail["release"], "version": sc.detail["version"], "scope": sc.detail}

	snap := map[string]any{"id": fwd.SnapshotID(sc.cx)}
	if sc.snap != nil {
		snap["state"], snap["trigger"] = sc.snap.State, sc.snap.ProcessingTrigger
		created := snapCreated(*sc.snap)
		snap["created_at"] = created
		if t, perr := time.Parse(time.RFC3339, created); perr == nil {
			age := time.Since(t)
			snap["age_hours"] = int(age.Hours())
			if age > 7*24*time.Hour {
				snap["note"] = "older than a week: collect again before predicting, so the prediction starts from the network as it is"
			}
		}
		snap["collected"] = strings.EqualFold(sc.snap.ProcessingTrigger, "COLLECTION") || sc.snap.CollectionTaskID != ""
	}
	d["base_snapshot"] = snap

	if cs, cerr := s.ChangeSets(ctx, networkID); cerr != nil {
		limits = append(limits, "the existing change sets could not be listed ("+cerr.Error()+")")
	} else {
		d["existing_change_sets"] = len(cs)
	}

	adv := map[string]any{"bgp_devices": len(sc.bgpDevs)}
	if devs, derr := s.ClassicDevices(ctx, networkID); derr != nil {
		limits = append(limits, "the collection devices could not be read ("+derr.Error()+"), so received-advertisement collection is not counted")
	} else {
		collecting := 0
		for _, dv := range devs {
			if sc.bgpDevs[dv.Name] && dv.CollectBGPAdvertisements != nil && *dv.CollectBGPAdvertisements {
				collecting++
			}
		}
		adv["collecting_received_advertisements"] = collecting
		adv["match"] = "by device name between the BGP devices found in config and the classic collection devices; a BGP device collected another way (a cloud or controller setup) is not counted as collecting"
	}
	adv["warning"] = "modifying or withdrawing a received BGP advertisement needs received-advertisement collection on the device (off by default); without it the route is left unchanged in the predicted snapshot and no warning is shown, so a prediction can look like it did nothing"
	d["bgp_advertisement_collection"] = adv

	finding := "Predict preflight: " + strings.TrimPrefix(sc.finding, "Predict fit: ")
	if n, ok := d["existing_change_sets"].(int); ok {
		finding += fmt.Sprintf("; %d existing change set(s)", n)
	}
	if c, ok := adv["collecting_received_advertisements"].(int); ok && len(sc.bgpDevs) > 0 {
		finding += fmt.Sprintf("; received BGP advertisements are collected on %d of %d BGP devices", c, len(sc.bgpDevs))
	}
	return result.Build(verifyChangeName, sc.status, finding, result.Deterministic, sc.cx, result.Options{Limits: limits, NextActions: []string{"edit-change-set", "inspect-collection"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvPredict, "predictPreflight", sc.cx.SnapshotID, map[string]any{"view": "preflight", "preflight": d}, finding)}})
}
