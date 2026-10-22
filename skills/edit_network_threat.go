package skills

import (
	"context"
	"errors"
	"fmt"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
)

// threatWriteLimits label every threat exposure write: the feature is experimental, switched on by an organization property that only Forward support changes, licence- and permission-gated, and served only by newer builds.
var threatWriteLimits = []string{
	"EXPERIMENTAL: Forward's threat exposure feature is experimental and its shapes change between builds. It is enabled by the organization property THREAT_EXPOSURE (not an organization administrator's to change), needs the security licence facet, and a build that does not serve its routes answers 404",
	"permissions: EDIT_THREAT_EXPOSURE (definitions), EVALUATE_THREAT_EXPOSURE (an analysis), EDIT_ASSET_CRITICALITIES (criticality); an organization administrator is expected to hold them, a 403 says which is missing",
}

type threatDefinitionIn struct {
	ExposurePoints []struct {
		ExposurePoint string `json:"exposure_point"`
		Level         string `json:"level"`
	} `json:"exposure_points"`
	Locations          []string `json:"locations"`
	Criticalities      []string `json:"criticalities"`
	Subnets            []string `json:"subnets"`
	MaxDurationSeconds int64    `json:"max_duration_seconds"`
	SnapshotID         string   `json:"snapshot_id"`
}

func (d threatDefinitionIn) points() []forward.ExposurePointAndLevel {
	var out []forward.ExposurePointAndLevel
	for _, p := range d.ExposurePoints {
		out = append(out, forward.ExposurePointAndLevel{ExposurePoint: p.ExposurePoint, Level: p.Level})
	}
	return out
}

func findThreatDefinition(ctx context.Context, s *fwd.Session, networkID, name string) (*forward.ThreatExposureDefinition, error) {
	infos, _, err := s.Client.ThreatExposure.ListDefinitions(ctx, networkID)
	if err != nil {
		return nil, err
	}
	for i := range infos {
		if infos[i].Definition.Name == name {
			d := infos[i].Definition
			return &d, nil
		}
	}
	return nil, nil
}

// planThreatExposure manages threat exposure definitions of a network by name: create, update, delete, and evaluate (start an analysis on a snapshot). EXPERIMENTAL.
func planThreatExposure(ctx context.Context, s *fwd.Session, in editNetworkInput) (*networkPlan, error) {
	if in.NetworkID == "" || strings.TrimSpace(in.Name) == "" {
		return nil, fmt.Errorf("%w: a threat exposure definition needs network_id and name (the definition's name)", ErrInvalidInput)
	}
	te := s.Client.ThreatExposure
	switch in.Action {
	case "create":
		var def threatDefinitionIn
		if err := decodeDefinition(in.Definition, &def, "exposure_points ([{exposure_point, level CRITICAL|SEVERE|HIGH|MODERATE}], 1 to 4), max_duration_seconds, and optionally locations, criticalities (BUSINESS_CRITICAL, CRITICAL_INFRA, CROWN_JEWEL), subnets"); err != nil {
			return nil, err
		}
		nd := forward.NewThreatExposureDefinition{Name: in.Name, ExposurePoints: def.points(), Locations: def.Locations, Criticalities: def.Criticalities, Subnets: def.Subnets, MaxDurationSeconds: def.MaxDurationSeconds}
		if err := nd.Validate(); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
		limits := append([]string{}, threatWriteLimits...)
		if cur, err := findThreatDefinition(ctx, s, in.NetworkID, in.Name); err == nil && cur != nil {
			limits = append(limits, fmt.Sprintf("a definition named %q already exists (id %s): this would add another with the same name", in.Name, cur.ID))
		}
		return &networkPlan{target: fmt.Sprintf("create threat exposure definition %q on network %s (EXPERIMENTAL)", in.Name, in.NetworkID), action: "create_threat_exposure_definition", before: nil, after: nd, reversible: true,
			undo: fmt.Sprintf("edit-network object threat_exposure action delete name %q with its confirm", in.Name), limits: limits,
			do: func(ctx context.Context) error {
				_, _, err := te.CreateDefinition(ctx, in.NetworkID, nd)
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				cur, err := findThreatDefinition(ctx, s, in.NetworkID, in.Name)
				return cur != nil, map[string]any{"listed": cur != nil}, err
			}}, nil
	case "update", "delete", "evaluate":
		cur, err := findThreatDefinition(ctx, s, in.NetworkID, in.Name)
		if err != nil {
			return nil, err
		}
		if cur == nil {
			return nil, nil
		}
		before, _ := fwd.Generic(cur)
		limits := append([]string{}, threatWriteLimits...)
		switch in.Action {
		case "update":
			var def struct {
				Name           *string `json:"name"`
				ExposurePoints []struct {
					ExposurePoint string `json:"exposure_point"`
					Level         string `json:"level"`
				} `json:"exposure_points"`
				Locations          *[]string `json:"locations"`
				Criticalities      *[]string `json:"criticalities"`
				Subnets            *[]string `json:"subnets"`
				MaxDurationSeconds *int64    `json:"max_duration_seconds"`
			}
			if err := decodeDefinition(in.Definition, &def, "name, exposure_points, locations, criticalities, subnets, max_duration_seconds (an empty list clears that filter: an empty filter selects all critical assets)"); err != nil {
				return nil, err
			}
			patch := forward.ThreatExposureDefinitionPatch{Name: def.Name, MaxDurationSeconds: def.MaxDurationSeconds}
			for _, p := range def.ExposurePoints {
				patch.ExposurePoints = append(patch.ExposurePoints, forward.ExposurePointAndLevel{ExposurePoint: p.ExposurePoint, Level: p.Level})
			}
			if def.Locations != nil {
				patch.Locations = append([]string{}, *def.Locations...)
			}
			if def.Criticalities != nil {
				patch.Criticalities = append([]string{}, *def.Criticalities...)
			}
			if def.Subnets != nil {
				patch.Subnets = append([]string{}, *def.Subnets...)
			}
			if patch.Name == nil && patch.ExposurePoints == nil && patch.Locations == nil && patch.Criticalities == nil && patch.Subnets == nil && patch.MaxDurationSeconds == nil {
				return nil, fmt.Errorf("%w: update must change at least one field", ErrInvalidInput)
			}
			if err := validateThreatPatch(patch); err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
			}
			return &networkPlan{target: fmt.Sprintf("update threat exposure definition %q on network %s (EXPERIMENTAL)", in.Name, in.NetworkID), action: "update_threat_exposure_definition", before: before, after: def, reversible: true,
				undo: "update it again with the before values (the definition before is in this result)", limits: limits,
				do: func(ctx context.Context) error {
					_, err := te.UpdateDefinition(ctx, in.NetworkID, cur.ID, patch)
					return err
				}}, nil
		case "delete":
			if len(in.Definition) > 0 {
				return nil, fmt.Errorf("%w: delete takes name and confirm only", ErrInvalidInput)
			}
			return &networkPlan{target: fmt.Sprintf("delete threat exposure definition %q (id %s) from network %s (EXPERIMENTAL)", in.Name, cur.ID, in.NetworkID), action: "delete_threat_exposure_definition", before: before, after: nil, reversible: false,
				undo: "none: create it again from the before values in this result (its analysis results are not kept)", confirm: in.Name, limits: limits,
				do: func(ctx context.Context) error {
					_, err := te.DeleteDefinition(ctx, in.NetworkID, cur.ID)
					return err
				},
				verify: func(ctx context.Context) (bool, any, error) {
					gone, err := findThreatDefinition(ctx, s, in.NetworkID, in.Name)
					return gone == nil, map[string]any{"still_listed": gone != nil}, err
				}}, nil
		default: // evaluate
			var def threatDefinitionIn
			if err := decodeDefinition(in.Definition, &def, "snapshot_id (the snapshot to analyze: it must be processed to the reachability stage)"); err != nil {
				return nil, err
			}
			if strings.TrimSpace(def.SnapshotID) == "" {
				return nil, fmt.Errorf("%w: evaluate needs definition.snapshot_id, the snapshot to analyze", ErrInvalidInput)
			}
			limits = append(limits, "this starts analysis jobs and returns at once (409 if the definition is already being analyzed against that snapshot); read the progress and then the results with inspect-vulnerabilities threat_exposure_experimental views status, critical_assets, direct and transitive")
			return &networkPlan{target: fmt.Sprintf("start the analysis of threat exposure definition %q on snapshot %s of network %s (EXPERIMENTAL)", in.Name, def.SnapshotID, in.NetworkID), action: "evaluate_threat_exposure", before: before, after: map[string]any{"evaluation": "started", "snapshot_id": def.SnapshotID},
				reversible: false, undo: "none: there is no cancel here; the results for that snapshot are replaced when the analysis finishes", limits: limits,
				do: func(ctx context.Context) error {
					_, err := te.Evaluate(ctx, in.NetworkID, cur.ID, def.SnapshotID)
					return err
				}}, nil
		}
	}
	return nil, fmt.Errorf("%w: object threat_exposure takes action create, update, delete or evaluate", ErrInvalidInput)
}

func validateThreatPatch(p forward.ThreatExposureDefinitionPatch) error {
	if p.MaxDurationSeconds != nil && *p.MaxDurationSeconds <= 0 {
		return errors.New("max_duration_seconds must be positive")
	}
	if p.Name != nil && strings.TrimSpace(*p.Name) == "" {
		return errors.New("name must not be blank")
	}
	if p.ExposurePoints != nil {
		nd := forward.NewThreatExposureDefinition{Name: "x", MaxDurationSeconds: 1, ExposurePoints: p.ExposurePoints, Criticalities: nil}
		if err := nd.Validate(); err != nil {
			return err
		}
	}
	for _, c := range p.Criticalities {
		nd := forward.NewThreatExposureDefinition{Name: "x", MaxDurationSeconds: 1, ExposurePoints: []forward.ExposurePointAndLevel{{ExposurePoint: "x", Level: forward.ExposureHigh}}, Criticalities: []string{c}}
		if err := nd.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// planAssetCriticality sets or removes the criticality of named assets (edge assets and endpoints alike), which is what a threat exposure definition selects its critical assets by. EXPERIMENTAL.
func planAssetCriticality(ctx context.Context, s *fwd.Session, in editNetworkInput) (*networkPlan, error) {
	if in.NetworkID == "" {
		return nil, fmt.Errorf("%w: asset criticality needs network_id", ErrInvalidInput)
	}
	var def struct {
		Names []string `json:"names"`
		Level string   `json:"level"`
	}
	if err := decodeDefinition(in.Definition, &def, "names (the assets' names) and, to assign, level (BUSINESS_CRITICAL, CRITICAL_INFRA or CROWN_JEWEL)"); err != nil {
		return nil, err
	}
	var names []string
	for _, n := range def.Names {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("%w: give definition.names, the assets to change", ErrInvalidInput)
	}
	// what is known before: edge assets are listed with their criticality; an endpoint's is not listed
	before := map[string]any{}
	if assets, _, err := s.Client.EdgeAssets.List(ctx, in.NetworkID); err == nil {
		for _, a := range assets {
			for _, n := range names {
				if a.Name == n {
					before[n] = a.Criticality
				}
			}
		}
	}
	limits := append([]string{}, threatWriteLimits...)
	limits = append(limits, "the before value is known only for edge assets (the list has no endpoints); an endpoint's current criticality is not shown")
	te := s.Client.EdgeAssets
	switch in.Action {
	case "assign":
		switch def.Level {
		case forward.CriticalityBusinessCritical, forward.CriticalityCriticalInfra, forward.CriticalityCrownJewel:
		default:
			return nil, fmt.Errorf("%w: level must be BUSINESS_CRITICAL, CRITICAL_INFRA or CROWN_JEWEL", ErrInvalidInput)
		}
		return &networkPlan{target: fmt.Sprintf("set the criticality of %s to %s on network %s (EXPERIMENTAL)", strings.Join(names, ", "), def.Level, in.NetworkID), action: "set_asset_criticality", before: before, after: map[string]any{"names": names, "level": def.Level},
			reversible: true, undo: "assign the before level again (listed for edge assets), or action delete to remove it", limits: limits,
			do: func(ctx context.Context) error {
				_, err := te.SetCriticality(ctx, in.NetworkID, names, def.Level)
				return err
			}}, nil
	case "delete":
		return &networkPlan{target: fmt.Sprintf("remove the criticality of %s on network %s (EXPERIMENTAL): they stop being critical assets of every threat exposure definition", strings.Join(names, ", "), in.NetworkID), action: "remove_asset_criticality", before: before, after: nil,
			reversible: false, undo: "assign the level again (the before value is shown only for edge assets)", confirm: strings.Join(names, ","), limits: limits,
			do: func(ctx context.Context) error {
				_, err := te.RemoveCriticality(ctx, in.NetworkID, names)
				return err
			}}, nil
	}
	return nil, fmt.Errorf("%w: object asset_criticality takes action assign or delete", ErrInvalidInput)
}
