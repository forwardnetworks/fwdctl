package skills

import (
	"context"
	"fmt"
	"sort"
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
	collects := map[string]bool{}
	devs, derr := s.ClassicDevices(ctx, networkID)
	if derr != nil {
		limits = append(limits, "the collection devices could not be read ("+derr.Error()+"), so received-advertisement collection is not counted")
	} else {
		collecting := 0
		for _, dv := range devs {
			on := dv.CollectBGPAdvertisements != nil && *dv.CollectBGPAdvertisements
			collects[dv.Name] = on
			if sc.bgpDevs[dv.Name] && on {
				collecting++
			}
		}
		adv["collecting_received_advertisements"] = collecting
		adv["match"] = "by device name between the BGP devices found in config and the classic collection devices; a BGP device collected another way (a cloud or controller setup) is not counted as collecting"
	}
	// the network's own requirement groups: devices Forward requires BGP advertisement collection on (gateways to adjacent networks, internet and intranet nodes, L3 VPNs fed by BGP)
	if reqs, _, rerr := s.Client.Predict.BGPCollectionRequirements(ctx, networkID); rerr != nil {
		limits = append(limits, "the network's BGP collection requirements could not be read ("+rerr.Error()+"), so which devices Forward requires to collect BGP advertisements is not listed")
	} else {
		seen := map[string]bool{}
		var required, notCollecting, notClassic []string
		for _, g := range reqs {
			for _, n := range g.Devices {
				if seen[n] {
					continue
				}
				seen[n] = true
				required = append(required, n)
				if derr == nil {
					if on, known := collects[n]; !known {
						notClassic = append(notClassic, n)
					} else if !on {
						notCollecting = append(notCollecting, n)
					}
				}
			}
		}
		sort.Strings(required)
		sort.Strings(notCollecting)
		sort.Strings(notClassic)
		if required == nil {
			required = []string{}
		}
		adv["required_by_network"] = required
		adv["required_note"] = "a required device counts as collecting when collectBgpAdvertisements is on; the table and peer type each requirement group needs are kept by Forward and not checked here"
		if derr == nil {
			if notCollecting == nil {
				notCollecting = []string{}
			}
			adv["required_not_collecting"] = notCollecting
			if len(notClassic) > 0 {
				adv["required_not_checked"] = notClassic
				limits = append(limits, fmt.Sprintf("%d device(s) the network requires to collect BGP advertisements have no classic collection device of that name (a controller or cloud setup, or a name in another form), so whether they collect was not checked; required_not_checked lists them", len(notClassic)))
			}
			if len(notCollecting) > 0 {
				limits = append(limits, fmt.Sprintf("%d device(s) the network requires to collect BGP advertisements have collection off; required_not_collecting lists them all", len(notCollecting)))
			}
		}
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
